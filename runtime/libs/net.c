// Ferret runtime: net (TCP transport)

#define _POSIX_C_SOURCE 200809L

#include <stdlib.h>
#include <stdint.h>
#include <stdbool.h>
#include <string.h>
#include <errno.h>
#include <limits.h>
#include <stdio.h>

#ifdef _WIN32
#define WIN32_LEAN_AND_MEAN
#include <winsock2.h>
#include <ws2tcpip.h>
typedef SOCKET ferret_socket_t;
#define FERRET_INVALID_SOCKET INVALID_SOCKET
#define ferret_close_socket closesocket
#else
#include <unistd.h>
#include <sys/types.h>
#include <sys/socket.h>
#include <netdb.h>
#include <sys/time.h>
#include <arpa/inet.h>
typedef int ferret_socket_t;
#define FERRET_INVALID_SOCKET (-1)
#define ferret_close_socket close
#endif

#include "../core/alloc.h"
#include "../core/array.h"
#include "../core/type_system.h"

typedef struct {
    int64_t handle;
    const char* addr;
} ferret_std_net_TcpListener;

typedef struct {
    int64_t handle;
    const char* local;
    const char* remote;
} ferret_std_net_TcpConn;

static char* ferret_net_strdup(const char* s) {
    if (!s) {
        return NULL;
    }
    size_t len = strlen(s);
    char* out = (char*)ferret_alloc(len + 1);
    if (!out) {
        return NULL;
    }
    memcpy(out, s, len + 1);
    return out;
}

static void ferret_net_result_err_at(void* out, const char* msg, size_t tag_offset) {
    if (!out) {
        return;
    }
    char** str_ptr = (char**)out;
    uint8_t* tag_ptr = (uint8_t*)((char*)out + tag_offset);
    *str_ptr = (char*)(msg ? msg : "unknown error");
    *tag_ptr = 0;
}

static void ferret_net_result_err(void* out, const char* msg) {
    ferret_net_result_err_at(out, msg, 8);
}

static void ferret_net_result_err_listener(void* out, const char* msg) {
    ferret_net_result_err_at(out, msg, 16);
}

static void ferret_net_result_err_conn(void* out, const char* msg) {
    ferret_net_result_err_at(out, msg, 24);
}

static void ferret_net_result_ok_bool(void* out, bool value) {
    if (!out) {
        return;
    }
    bool* val_ptr = (bool*)out;
    uint8_t* tag_ptr = (uint8_t*)((char*)out + 8);
    *val_ptr = value;
    *tag_ptr = 1;
}

static void ferret_net_result_ok_i32(void* out, int32_t value) {
    if (!out) {
        return;
    }
    int32_t* val_ptr = (int32_t*)out;
    uint8_t* tag_ptr = (uint8_t*)((char*)out + 8);
    *val_ptr = value;
    *tag_ptr = 1;
}

static void ferret_net_result_err_bytes(void* out, const char* msg) {
    ferret_net_result_err_at(out, msg, 24);
}

static void ferret_net_result_ok_bytes(void* out, ferret_array_t* arr) {
    if (!out) {
        return;
    }
    ferret_array_t** arr_ptr = (ferret_array_t**)out;
    uint8_t* tag_ptr = (uint8_t*)((char*)out + 24);
    *arr_ptr = arr;
    *tag_ptr = 1;
}

#ifdef _WIN32
static void ferret_net_init_winsock(void) {
    static bool initialized = false;
    if (initialized) {
        return;
    }
    WSADATA wsa;
    if (WSAStartup(MAKEWORD(2, 2), &wsa) == 0) {
        initialized = true;
    }
}
#endif

static bool ferret_net_split_addr(const char* addr, char** host_out, char** port_out) {
    if (!addr || !host_out || !port_out) {
        return false;
    }

    const char* host_start = addr;
    const char* host_end = NULL;
    const char* port_start = NULL;

    if (addr[0] == '[') {
        const char* close = strchr(addr, ']');
        if (!close) {
            return false;
        }
        host_start = addr + 1;
        host_end = close;
        if (close[1] != ':') {
            return false;
        }
        port_start = close + 2;
    } else {
        const char* colon = strrchr(addr, ':');
        if (!colon) {
            return false;
        }
        host_end = colon;
        port_start = colon + 1;
    }

    if (!port_start || *port_start == '\0') {
        return false;
    }

    size_t host_len = (size_t)(host_end - host_start);
    char* host = (char*)malloc(host_len + 1);
    if (!host) {
        return false;
    }
    if (host_len > 0) {
        memcpy(host, host_start, host_len);
    }
    host[host_len] = '\0';

    char* port = (char*)malloc(strlen(port_start) + 1);
    if (!port) {
        free(host);
        return false;
    }
    memcpy(port, port_start, strlen(port_start) + 1);

    *host_out = host;
    *port_out = port;
    return true;
}

static char* ferret_net_join_host_port(const char* host, const char* port) {
    if (!host || !port) {
        return ferret_net_strdup("");
    }
    bool is_ipv6 = strchr(host, ':') != NULL;
    size_t host_len = strlen(host);
    size_t port_len = strlen(port);
    size_t extra = is_ipv6 ? 3 : 1;
    size_t total = host_len + port_len + extra + 1;
    char* out = (char*)ferret_alloc(total);
    if (!out) {
        return NULL;
    }
    if (is_ipv6) {
        snprintf(out, total, "[%s]:%s", host, port);
    } else {
        snprintf(out, total, "%s:%s", host, port);
    }
    return out;
}

static void ferret_net_socket_addrs(ferret_socket_t sock, char** local_out, char** remote_out) {
    if (local_out) {
        *local_out = NULL;
    }
    if (remote_out) {
        *remote_out = NULL;
    }

    struct sockaddr_storage addr;
    socklen_t len = sizeof(addr);

    if (local_out && getsockname(sock, (struct sockaddr*)&addr, &len) == 0) {
        char host[256] = {0};
        char serv[64] = {0};
        if (getnameinfo((struct sockaddr*)&addr, len, host, sizeof(host), serv, sizeof(serv), NI_NUMERICHOST | NI_NUMERICSERV) == 0) {
            *local_out = ferret_net_join_host_port(host, serv);
        }
    }
    if (local_out && *local_out == NULL) {
        *local_out = ferret_net_strdup("");
    }

    len = sizeof(addr);
    if (remote_out && getpeername(sock, (struct sockaddr*)&addr, &len) == 0) {
        char host[256] = {0};
        char serv[64] = {0};
        if (getnameinfo((struct sockaddr*)&addr, len, host, sizeof(host), serv, sizeof(serv), NI_NUMERICHOST | NI_NUMERICSERV) == 0) {
            *remote_out = ferret_net_join_host_port(host, serv);
        }
    }
    if (remote_out && *remote_out == NULL) {
        *remote_out = ferret_net_strdup("");
    }
}

// Listen on TCP address (str!TcpListener)
void ferret_std_net_ListenTcp(void* out, const char* addr) {
    if (!out) {
        return;
    }
    if (!addr) {
        ferret_net_result_err_listener(out, "addr is null");
        return;
    }

#ifdef _WIN32
    ferret_net_init_winsock();
#endif

    char* host = NULL;
    char* port = NULL;
    if (!ferret_net_split_addr(addr, &host, &port)) {
        ferret_net_result_err_listener(out, "invalid addr");
        return;
    }

    struct addrinfo hints;
    memset(&hints, 0, sizeof(hints));
    hints.ai_socktype = SOCK_STREAM;
    hints.ai_family = AF_UNSPEC;
    hints.ai_flags = AI_PASSIVE;

    struct addrinfo* res = NULL;
    int rc = getaddrinfo((host && host[0] != '\0') ? host : NULL, port, &hints, &res);
    free(host);
    free(port);
    if (rc != 0 || !res) {
        ferret_net_result_err_listener(out, "getaddrinfo failed");
        return;
    }

    ferret_socket_t server_fd = FERRET_INVALID_SOCKET;
    struct addrinfo* it = res;
    for (; it != NULL; it = it->ai_next) {
        server_fd = socket(it->ai_family, it->ai_socktype, it->ai_protocol);
        if (server_fd == FERRET_INVALID_SOCKET) {
            continue;
        }
        int opt = 1;
        setsockopt(server_fd, SOL_SOCKET, SO_REUSEADDR, (const char*)&opt, sizeof(opt));
        if (bind(server_fd, it->ai_addr, (int)it->ai_addrlen) == 0) {
            if (listen(server_fd, 16) == 0) {
                break;
            }
        }
        ferret_close_socket(server_fd);
        server_fd = FERRET_INVALID_SOCKET;
    }
    freeaddrinfo(res);

    if (server_fd == FERRET_INVALID_SOCKET) {
        ferret_net_result_err_listener(out, "listen failed");
        return;
    }

    int64_t* handle_ptr = (int64_t*)out;
    char** addr_ptr = (char**)((char*)out + 8);
    uint8_t* tag_ptr = (uint8_t*)((char*)out + 16);
    *handle_ptr = (int64_t)(intptr_t)server_fd;
    *addr_ptr = ferret_net_strdup(addr);
    *tag_ptr = 1;
}

// Dial TCP address (str!TcpConn)
void ferret_std_net_DialTcp(void* out, const char* addr) {
    if (!out) {
        return;
    }
    if (!addr) {
        ferret_net_result_err_conn(out, "addr is null");
        return;
    }

#ifdef _WIN32
    ferret_net_init_winsock();
#endif

    char* host = NULL;
    char* port = NULL;
    if (!ferret_net_split_addr(addr, &host, &port)) {
        ferret_net_result_err_conn(out, "invalid addr");
        return;
    }

    struct addrinfo hints;
    memset(&hints, 0, sizeof(hints));
    hints.ai_socktype = SOCK_STREAM;
    hints.ai_family = AF_UNSPEC;

    struct addrinfo* res = NULL;
    int rc = getaddrinfo(host, port, &hints, &res);
    free(host);
    free(port);
    if (rc != 0 || !res) {
        ferret_net_result_err_conn(out, "getaddrinfo failed");
        return;
    }

    ferret_socket_t sock = FERRET_INVALID_SOCKET;
    struct addrinfo* it = res;
    for (; it != NULL; it = it->ai_next) {
        sock = socket(it->ai_family, it->ai_socktype, it->ai_protocol);
        if (sock == FERRET_INVALID_SOCKET) {
            continue;
        }
        if (connect(sock, it->ai_addr, (int)it->ai_addrlen) == 0) {
            break;
        }
        ferret_close_socket(sock);
        sock = FERRET_INVALID_SOCKET;
    }
    freeaddrinfo(res);

    if (sock == FERRET_INVALID_SOCKET) {
        ferret_net_result_err_conn(out, "connect failed");
        return;
    }

    int64_t* handle_ptr = (int64_t*)out;
    char** local_ptr = (char**)((char*)out + 8);
    char** remote_ptr = (char**)((char*)out + 16);
    uint8_t* tag_ptr = (uint8_t*)((char*)out + 24);
    *handle_ptr = (int64_t)(intptr_t)sock;
    ferret_net_socket_addrs(sock, local_ptr, remote_ptr);
    *tag_ptr = 1;
}

// Accept TCP connection (str!TcpConn)
void ferret_std_net_Accept(void* out, const ferret_std_net_TcpListener* listener) {
    if (!out) {
        return;
    }
    if (!listener || listener->handle == 0) {
        ferret_net_result_err_conn(out, "invalid listener");
        return;
    }
    ferret_socket_t server_fd = (ferret_socket_t)(intptr_t)listener->handle;
    ferret_socket_t client = accept(server_fd, NULL, NULL);
    if (client == FERRET_INVALID_SOCKET) {
        ferret_net_result_err_conn(out, "accept failed");
        return;
    }

    int64_t* handle_ptr = (int64_t*)out;
    char** local_ptr = (char**)((char*)out + 8);
    char** remote_ptr = (char**)((char*)out + 16);
    uint8_t* tag_ptr = (uint8_t*)((char*)out + 24);
    *handle_ptr = (int64_t)(intptr_t)client;
    ferret_net_socket_addrs(client, local_ptr, remote_ptr);
    *tag_ptr = 1;
}

// Close listener
void ferret_std_net_CloseListener(const ferret_std_net_TcpListener* listener) {
    if (!listener || listener->handle == 0) {
        return;
    }
    ferret_close_socket((ferret_socket_t)(intptr_t)listener->handle);
}

// Close connection
void ferret_std_net_CloseConn(const ferret_std_net_TcpConn* conn) {
    if (!conn || conn->handle == 0) {
        return;
    }
    ferret_close_socket((ferret_socket_t)(intptr_t)conn->handle);
}

// Read bytes
void ferret_std_net_Read(void* out, const ferret_std_net_TcpConn* conn, int32_t maxBytes) {
    if (!out) {
        return;
    }
    if (!conn || conn->handle == 0) {
        ferret_net_result_err_bytes(out, "invalid connection");
        return;
    }
    if (maxBytes <= 0) {
        ferret_net_result_err_bytes(out, "maxBytes must be > 0");
        return;
    }
    ferret_socket_t sock = (ferret_socket_t)(intptr_t)conn->handle;
    uint8_t* buf = (uint8_t*)ferret_alloc((size_t)maxBytes);
    if (!buf) {
        ferret_net_result_err_bytes(out, "out of memory");
        return;
    }
    int r = (int)recv(sock, (char*)buf, maxBytes, 0);
    if (r < 0) {
        ferret_net_result_err_bytes(out, "recv failed");
        return;
    }
    if (r == 0) {
        ferret_array_t* empty = ferret_array_new(sizeof(uint8_t), 0, (ferret_type_info_t*)&ferret_type_byte);
        ferret_net_result_ok_bytes(out, empty);
        return;
    }
    ferret_array_t* arr = ferret_array_from_data(buf, r, r, sizeof(uint8_t), (ferret_type_info_t*)&ferret_type_byte);
    ferret_net_result_ok_bytes(out, arr);
}

// Write bytes
void ferret_std_net_Write(void* out, const ferret_std_net_TcpConn* conn, ferret_array_t* data) {
    if (!out) {
        return;
    }
    if (!conn || conn->handle == 0) {
        ferret_net_result_err(out, "invalid connection");
        return;
    }
    if (!data || data->length == 0) {
        ferret_net_result_ok_i32(out, 0);
        return;
    }
    ferret_socket_t sock = (ferret_socket_t)(intptr_t)conn->handle;
    int32_t total = 0;
    uint8_t* ptr = (uint8_t*)data->data;
    int32_t remaining = data->length;
    while (remaining > 0) {
        int sent = (int)send(sock, (const char*)ptr, remaining, 0);
        if (sent <= 0) {
            ferret_net_result_err(out, "send failed");
            return;
        }
        total += sent;
        remaining -= sent;
        ptr += sent;
    }
    ferret_net_result_ok_i32(out, total);
}

// Write string
void ferret_std_net_WriteStr(void* out, const ferret_std_net_TcpConn* conn, const char* data) {
    if (!out) {
        return;
    }
    if (!conn || conn->handle == 0) {
        ferret_net_result_err(out, "invalid connection");
        return;
    }
    if (!data) {
        ferret_net_result_ok_i32(out, 0);
        return;
    }
    size_t len = strlen(data);
    if (len == 0) {
        ferret_net_result_ok_i32(out, 0);
        return;
    }
    ferret_socket_t sock = (ferret_socket_t)(intptr_t)conn->handle;
    int32_t total = 0;
    const char* ptr = data;
    size_t remaining = len;
    while (remaining > 0) {
        int sent = (int)send(sock, ptr, (int)remaining, 0);
        if (sent <= 0) {
            ferret_net_result_err(out, "send failed");
            return;
        }
        total += sent;
        remaining -= (size_t)sent;
        ptr += sent;
    }
    ferret_net_result_ok_i32(out, total);
}

// Read timeout
void ferret_std_net_SetReadTimeoutMs(void* out, const ferret_std_net_TcpConn* conn, int32_t ms) {
    if (!out) {
        return;
    }
    if (!conn || conn->handle == 0) {
        ferret_net_result_err(out, "invalid connection");
        return;
    }
    ferret_socket_t sock = (ferret_socket_t)(intptr_t)conn->handle;
#ifdef _WIN32
    DWORD timeout = ms < 0 ? 0 : (DWORD)ms;
    if (setsockopt(sock, SOL_SOCKET, SO_RCVTIMEO, (const char*)&timeout, sizeof(timeout)) != 0) {
        ferret_net_result_err(out, "setsockopt failed");
        return;
    }
#else
    struct timeval tv;
    tv.tv_sec = ms / 1000;
    tv.tv_usec = (ms % 1000) * 1000;
    if (setsockopt(sock, SOL_SOCKET, SO_RCVTIMEO, &tv, sizeof(tv)) != 0) {
        ferret_net_result_err(out, strerror(errno));
        return;
    }
#endif
    ferret_net_result_ok_bool(out, true);
}

// Write timeout
void ferret_std_net_SetWriteTimeoutMs(void* out, const ferret_std_net_TcpConn* conn, int32_t ms) {
    if (!out) {
        return;
    }
    if (!conn || conn->handle == 0) {
        ferret_net_result_err(out, "invalid connection");
        return;
    }
    ferret_socket_t sock = (ferret_socket_t)(intptr_t)conn->handle;
#ifdef _WIN32
    DWORD timeout = ms < 0 ? 0 : (DWORD)ms;
    if (setsockopt(sock, SOL_SOCKET, SO_SNDTIMEO, (const char*)&timeout, sizeof(timeout)) != 0) {
        ferret_net_result_err(out, "setsockopt failed");
        return;
    }
#else
    struct timeval tv;
    tv.tv_sec = ms / 1000;
    tv.tv_usec = (ms % 1000) * 1000;
    if (setsockopt(sock, SOL_SOCKET, SO_SNDTIMEO, &tv, sizeof(tv)) != 0) {
        ferret_net_result_err(out, strerror(errno));
        return;
    }
#endif
    ferret_net_result_ok_bool(out, true);
}

// Keepalive
void ferret_std_net_SetKeepAlive(void* out, const ferret_std_net_TcpConn* conn, bool enabled) {
    if (!out) {
        return;
    }
    if (!conn || conn->handle == 0) {
        ferret_net_result_err(out, "invalid connection");
        return;
    }
    ferret_socket_t sock = (ferret_socket_t)(intptr_t)conn->handle;
    int opt = enabled ? 1 : 0;
    if (setsockopt(sock, SOL_SOCKET, SO_KEEPALIVE, (const char*)&opt, sizeof(opt)) != 0) {
        ferret_net_result_err(out, "setsockopt failed");
        return;
    }
    ferret_net_result_ok_bool(out, true);
}
