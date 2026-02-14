// Ferret runtime: net (TCP transport)

#define _POSIX_C_SOURCE 200809L

#include <stdlib.h>
#include <stdint.h>
#include <stdbool.h>
#include "../core/alloc.h"
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
#include "../core/runtime_naming.h"
#include "../core/result.h"

// Define the module prefix for this file (implements ferret_libs/net/tcp.fer)
#define MODULE_PREFIX ferret_net_tcp
#define CONN FERRET_TYPE(CONN)
#define LISTENER FERRET_TYPE(LISTENER)

typedef struct {
    int64_t handle;
    const char* addr;
} LISTENER;

typedef struct {
    int64_t handle;
    const char* local;
    const char* remote;
} CONN;

static char* FERRET_FUNC(strdup)(const char* s) {
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

#ifdef _WIN32
static void FERRET_FUNC(init_winsock)(void) {
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

static bool FERRET_FUNC(split_addr)(const char* addr, char** host_out, char** port_out) {
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
    char* host = (char*)ferret_alloc(host_len + 1);
    if (!host) {
        return false;
    }
    if (host_len > 0) {
        memcpy(host, host_start, host_len);
    }
    host[host_len] = '\0';

    char* port = (char*)ferret_alloc(strlen(port_start) + 1);
    if (!port) {
        ferret_free(host);
        return false;
    }
    memcpy(port, port_start, strlen(port_start) + 1);

    *host_out = host;
    *port_out = port;
    return true;
}

static char* FERRET_FUNC(join_host_port)(const char* host, const char* port) {
    if (!host || !port) {
        return FERRET_FUNC(strdup)("");
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

static void FERRET_FUNC(socket_addrs)(ferret_socket_t sock, char** local_out, char** remote_out) {
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
            *local_out = FERRET_FUNC(join_host_port)(host, serv);
        }
    }
    if (local_out && *local_out == NULL) {
        *local_out = FERRET_FUNC(strdup)("");
    }

    len = sizeof(addr);
    if (remote_out && getpeername(sock, (struct sockaddr*)&addr, &len) == 0) {
        char host[256] = {0};
        char serv[64] = {0};
        if (getnameinfo((struct sockaddr*)&addr, len, host, sizeof(host), serv, sizeof(serv), NI_NUMERICHOST | NI_NUMERICSERV) == 0) {
            *remote_out = FERRET_FUNC(join_host_port)(host, serv);
        }
    }
    if (remote_out && *remote_out == NULL) {
        *remote_out = FERRET_FUNC(strdup)("");
    }
}

// Listen on TCP address (str!TcpListener)
void FERRET_FUNC(ListenTcp)(void* out, const char* addr) {
    if (!out) {
        return;
    }
    if (!addr) {
        FERRET_RESULT_ERR(out, 16, "addr is null");
        return;
    }

#ifdef _WIN32
    FERRET_FUNC(init_winsock)();
#endif

    char* host = NULL;
    char* port = NULL;
    if (!FERRET_FUNC(split_addr)(addr, &host, &port)) {
        FERRET_RESULT_ERR(out, 16, "invalid addr");
        return;
    }

    struct addrinfo hints;
    memset(&hints, 0, sizeof(hints));
    hints.ai_socktype = SOCK_STREAM;
    hints.ai_family = AF_UNSPEC;
    hints.ai_flags = AI_PASSIVE;

    struct addrinfo* res = NULL;
    int rc = getaddrinfo((host && host[0] != '\0') ? host : NULL, port, &hints, &res);
    ferret_free(host);
    ferret_free(port);
    if (rc != 0 || !res) {
        FERRET_RESULT_ERR(out, 16, "getaddrinfo failed");
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
        FERRET_RESULT_ERR(out, 16, "listen failed");
        return;
    }

    int64_t* handle_ptr = (int64_t*)out;
    char** addr_ptr = (char**)((char*)out + 8);
    uint8_t* tag_ptr = (uint8_t*)((char*)out + 16);
    *handle_ptr = (int64_t)(intptr_t)server_fd;
    *addr_ptr = FERRET_FUNC(strdup)(addr);
    *tag_ptr = 1;
}

// Dial TCP address (str!TcpConn)
void FERRET_FUNC(DialTcp)(void* out, const char* addr) {
    if (!out) {
        return;
    }
    if (!addr) {
        FERRET_RESULT_ERR(out, 24, "addr is null");
        return;
    }

#ifdef _WIN32
    FERRET_FUNC(init_winsock)();
#endif

    char* host = NULL;
    char* port = NULL;
    if (!FERRET_FUNC(split_addr)(addr, &host, &port)) {
        FERRET_RESULT_ERR(out, 24, "invalid addr");
        return;
    }

    struct addrinfo hints;
    memset(&hints, 0, sizeof(hints));
    hints.ai_socktype = SOCK_STREAM;
    hints.ai_family = AF_UNSPEC;

    struct addrinfo* res = NULL;
    int rc = getaddrinfo(host, port, &hints, &res);
    ferret_free(host);
    ferret_free(port);
    if (rc != 0 || !res) {
        FERRET_RESULT_ERR(out, 24, "getaddrinfo failed");
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
        FERRET_RESULT_ERR(out, 24, "connect failed");
        return;
    }

    int64_t* handle_ptr = (int64_t*)out;
    char** local_ptr = (char**)((char*)out + 8);
    char** remote_ptr = (char**)((char*)out + 16);
    uint8_t* tag_ptr = (uint8_t*)((char*)out + 24);
    *handle_ptr = (int64_t)(intptr_t)sock;
    FERRET_FUNC(socket_addrs)(sock, local_ptr, remote_ptr);
    *tag_ptr = 1;
}

// Accept TCP connection (str!TcpConn)
void FERRET_FUNC(TcpListener_Accept)(void* out, const LISTENER* listener, uint64_t listener_heap) {
    (void)listener_heap;
    if (!out) {
        return;
    }
    if (!listener || listener->handle == 0) {
        FERRET_RESULT_ERR(out, 24, "invalid listener");
        return;
    }
    ferret_socket_t server_fd = (ferret_socket_t)(intptr_t)listener->handle;
    ferret_socket_t client = accept(server_fd, NULL, NULL);
    if (client == FERRET_INVALID_SOCKET) {
        FERRET_RESULT_ERR(out, 24, "accept failed");
        return;
    }

    int64_t* handle_ptr = (int64_t*)out;
    char** local_ptr = (char**)((char*)out + 8);
    char** remote_ptr = (char**)((char*)out + 16);
    uint8_t* tag_ptr = (uint8_t*)((char*)out + 24);
    *handle_ptr = (int64_t)(intptr_t)client;
    FERRET_FUNC(socket_addrs)(client, local_ptr, remote_ptr);
    *tag_ptr = 1;
}

// Close listener
void FERRET_FUNC(TcpListener_Close)(LISTENER* listener) {
    if (!listener || listener->handle == 0) {
        return;
    }
    ferret_close_socket((ferret_socket_t)(intptr_t)listener->handle);
    listener->handle = 0;
}

// Close connection
void FERRET_FUNC(TcpConn_Close)(CONN* conn) {
    if (!conn || conn->handle == 0) {
        return;
    }
    ferret_close_socket((ferret_socket_t)(intptr_t)conn->handle);
    conn->handle = 0;
}

// Read bytes
void FERRET_FUNC(TcpConn_Read)(void* out, const CONN* conn, uint64_t conn_heap, int32_t maxBytes) {
    (void)conn_heap;
    if (!out) {
        return;
    }
    if (!conn || conn->handle == 0) {
        FERRET_RESULT_ERR(out, sizeof(void*), "invalid connection");
        return;
    }
    if (maxBytes <= 0) {
        FERRET_RESULT_ERR(out, sizeof(void*), "maxBytes must be > 0");
        return;
    }
    ferret_socket_t sock = (ferret_socket_t)(intptr_t)conn->handle;
    uint8_t* buf = (uint8_t*)ferret_alloc((size_t)maxBytes);
    if (!buf) {
        FERRET_RESULT_ERR(out, sizeof(void*), "out of memory");
        return;
    }
    int r = (int)recv(sock, (char*)buf, maxBytes, 0);
    if (r < 0) {
        FERRET_RESULT_ERR(out, sizeof(void*), "recv failed");
        return;
    }
    if (r == 0) {
        ferret_array_t* empty = ferret_array_new(sizeof(uint8_t), 0, (ferret_type_info_t*)&ferret_type_byte);
        FERRET_RESULT_OK(out, sizeof(void*), ferret_array_t*, empty);
        return;
    }
    ferret_array_t* arr = ferret_array_from_data(buf, r, r, sizeof(uint8_t), (ferret_type_info_t*)&ferret_type_byte);
    FERRET_RESULT_OK(out, sizeof(void*), ferret_array_t*, arr);
}

// Read bytes as string
void FERRET_FUNC(TcpConn_ReadStr)(void* out, const CONN* conn, uint64_t conn_heap, int32_t maxBytes) {
    (void)conn_heap;
    if (!out) {
        return;
    }
    if (!conn || conn->handle == 0) {
        FERRET_RESULT_ERR(out, 8, "invalid connection");
        return;
    }
    if (maxBytes <= 0) {
        FERRET_RESULT_ERR(out, 8, "maxBytes must be > 0");
        return;
    }
    ferret_socket_t sock = (ferret_socket_t)(intptr_t)conn->handle;
    uint8_t* buf = (uint8_t*)ferret_alloc((size_t)maxBytes);
    if (!buf) {
        FERRET_RESULT_ERR(out, 8, "out of memory");
        return;
    }
    int r = (int)recv(sock, (char*)buf, maxBytes, 0);
    if (r < 0) {
        FERRET_RESULT_ERR(out, 8, "recv failed");
        return;
    }
    if (r == 0) {
        FERRET_RESULT_OK(out, 8, char*, "");
        return;
    }
    char* str = (char*)ferret_alloc((size_t)r + 1);
    if (!str) {
        FERRET_RESULT_ERR(out, 8, "out of memory");
        return;
    }
    memcpy(str, buf, (size_t)r);
    str[r] = '\0';
    FERRET_RESULT_OK(out, 8, char*, str);
}

// Write bytes
void FERRET_FUNC(TcpConn_Write)(void* out, const CONN* conn, uint64_t conn_heap, ferret_array_t* data) {
    (void)conn_heap;
    if (!out) {
        return;
    }
    if (!conn || conn->handle == 0) {
        FERRET_RESULT_ERR(out, 8, "invalid connection");
        return;
    }
    if (!data || data->length == 0) {
        FERRET_RESULT_OK(out, 8, int32_t, 0);
        return;
    }
    ferret_socket_t sock = (ferret_socket_t)(intptr_t)conn->handle;
    int32_t total = 0;
    uint8_t* ptr = (uint8_t*)data->data;
    int32_t remaining = data->length;
    while (remaining > 0) {
        int sent = (int)send(sock, (const char*)ptr, remaining, 0);
        if (sent <= 0) {
            FERRET_RESULT_ERR(out, 8, "send failed");
            return;
        }
        total += sent;
        remaining -= sent;
        ptr += sent;
    }
    FERRET_RESULT_OK(out, 8, int32_t, total);
}

// Write string
void FERRET_FUNC(TcpConn_WriteStr)(void* out, const CONN* conn, uint64_t conn_heap, const char* data) {
    (void)conn_heap;
    if (!out) {
        return;
    }
    if (!conn || conn->handle == 0) {
        FERRET_RESULT_ERR(out, 8, "invalid connection");
        return;
    }
    if (!data) {
        FERRET_RESULT_OK(out, 8, int32_t, 0);
        return;
    }
    size_t len = strlen(data);
    if (len == 0) {
        FERRET_RESULT_OK(out, 8, int32_t, 0);
        return;
    }
    ferret_socket_t sock = (ferret_socket_t)(intptr_t)conn->handle;
    int32_t total = 0;
    const char* ptr = data;
    size_t remaining = len;
    while (remaining > 0) {
        int sent = (int)send(sock, ptr, (int)remaining, 0);
        if (sent <= 0) {
            FERRET_RESULT_ERR(out, 8, "send failed");
            return;
        }
        total += sent;
        remaining -= (size_t)sent;
        ptr += sent;
    }
    FERRET_RESULT_OK(out, 8, int32_t, total);
}

// Read timeout
void FERRET_FUNC(TcpConn_SetReadTimeoutMs)(void* out, const CONN* conn, uint64_t conn_heap, int32_t ms) {
    (void)conn_heap;
    if (!out) {
        return;
    }
    if (!conn || conn->handle == 0) {
        FERRET_RESULT_ERR(out, 8, "invalid connection");
        return;
    }
    ferret_socket_t sock = (ferret_socket_t)(intptr_t)conn->handle;
#ifdef _WIN32
    DWORD timeout = ms < 0 ? 0 : (DWORD)ms;
    if (setsockopt(sock, SOL_SOCKET, SO_RCVTIMEO, (const char*)&timeout, sizeof(timeout)) != 0) {
        FERRET_RESULT_ERR(out, 8, "setsockopt failed");
        return;
    }
#else
    struct timeval tv;
    tv.tv_sec = ms / 1000;
    tv.tv_usec = (ms % 1000) * 1000;
    if (setsockopt(sock, SOL_SOCKET, SO_RCVTIMEO, &tv, sizeof(tv)) != 0) {
        FERRET_RESULT_ERR(out, 8, strerror(errno));
        return;
    }
#endif
    FERRET_RESULT_OK(out, 8, bool, true);
}

// Write timeout
void FERRET_FUNC(TcpConn_SetWriteTimeoutMs)(void* out, const CONN* conn, uint64_t conn_heap, int32_t ms) {
    (void)conn_heap;
    if (!out) {
        return;
    }
    if (!conn || conn->handle == 0) {
        FERRET_RESULT_ERR(out, 8, "invalid connection");
        return;
    }
    ferret_socket_t sock = (ferret_socket_t)(intptr_t)conn->handle;
#ifdef _WIN32
    DWORD timeout = ms < 0 ? 0 : (DWORD)ms;
    if (setsockopt(sock, SOL_SOCKET, SO_SNDTIMEO, (const char*)&timeout, sizeof(timeout)) != 0) {
        FERRET_RESULT_ERR(out, 8, "setsockopt failed");
        return;
    }
#else
    struct timeval tv;
    tv.tv_sec = ms / 1000;
    tv.tv_usec = (ms % 1000) * 1000;
    if (setsockopt(sock, SOL_SOCKET, SO_SNDTIMEO, &tv, sizeof(tv)) != 0) {
        FERRET_RESULT_ERR(out, 8, strerror(errno));
        return;
    }
#endif
    FERRET_RESULT_OK(out, 8, bool, true);
}

// Keepalive
void FERRET_FUNC(TcpConn_SetKeepAlive)(void* out, const CONN* conn, uint64_t conn_heap, bool enabled) {
    (void)conn_heap;
    if (!out) {
        return;
    }
    if (!conn || conn->handle == 0) {
        FERRET_RESULT_ERR(out, 8, "invalid connection");
        return;
    }
    ferret_socket_t sock = (ferret_socket_t)(intptr_t)conn->handle;
    int opt = enabled ? 1 : 0;
    if (setsockopt(sock, SOL_SOCKET, SO_KEEPALIVE, (const char*)&opt, sizeof(opt)) != 0) {
        FERRET_RESULT_ERR(out, 8, "setsockopt failed");
        return;
    }
    FERRET_RESULT_OK(out, 8, bool, true);
}
