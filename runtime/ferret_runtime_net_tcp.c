#ifndef _WIN32
#define _POSIX_C_SOURCE 200112L
#endif

#include "ferret_runtime_internal.h"

#include <errno.h>
#include <stdio.h>

#ifdef _WIN32
#include <winsock2.h>
#include <ws2tcpip.h>
typedef SOCKET FerretSocket;
#define FERRET_INVALID_SOCKET INVALID_SOCKET
#define FERRET_SHUT_RD SD_RECEIVE
#define FERRET_SHUT_WR SD_SEND
#else
#include <netdb.h>
#include <netinet/tcp.h>
#include <sys/socket.h>
#include <sys/time.h>
#include <sys/types.h>
#include <unistd.h>
typedef int FerretSocket;
#define FERRET_INVALID_SOCKET (-1)
#define FERRET_SHUT_RD SHUT_RD
#define FERRET_SHUT_WR SHUT_WR
#endif

typedef struct {
    FerretSocket socket_fd;
    ferret_u8   *read_buf;
    ferret_usize read_cap;
} FerretStdTcpConn;

typedef struct {
    FerretSocket socket_fd;
    ferret_i32   accept_timeout_ms;
} FerretStdTcpListener;

static ferret_i32 ferret__net_map_error_code(int code, ferret_bool addrinfo_code);

#ifdef _WIN32
static ferret_bool ferret__net_init(void) {
    static int started = 0;
    static int ready = 0;
    WSADATA data;

    if (started) {
        return ready ? 1 : 0;
    }
    started = 1;
    if (WSAStartup(MAKEWORD(2, 2), &data) != 0) {
        ready = 0;
        return 0;
    }
    ready = 1;
    return 1;
}

static void ferret__net_close_socket(FerretSocket socket_fd) {
    if (socket_fd != FERRET_INVALID_SOCKET) {
        closesocket(socket_fd);
    }
}
#else
static ferret_bool ferret__net_init(void) {
    return 1;
}

static void ferret__net_close_socket(FerretSocket socket_fd) {
    if (socket_fd != FERRET_INVALID_SOCKET) {
        close(socket_fd);
    }
}
#endif

static ferret_bool ferret__net_buffer_reserve(FerretStdTcpConn *conn, ferret_usize size) {
    void *next_buf;

    if (conn == NULL) {
        return 0;
    }
    if (size <= conn->read_cap) {
        return 1;
    }
    next_buf = realloc(conn->read_buf, (size_t)size);
    if (next_buf == NULL) {
        return 0;
    }
    conn->read_buf = (ferret_u8 *)next_buf;
    conn->read_cap = size;
    return 1;
}

static FerretStdTcpConn *ferret__net_new_conn(FerretSocket socket_fd) {
    FerretStdTcpConn *conn;

    if (socket_fd == FERRET_INVALID_SOCKET) {
        return NULL;
    }
    conn = (FerretStdTcpConn *)calloc(1, sizeof(FerretStdTcpConn));
    if (conn == NULL) {
        ferret__net_close_socket(socket_fd);
        ferret__io_error_set(FERRET_IO_ERR_UNKNOWN);
        return NULL;
    }
    conn->socket_fd = socket_fd;
    return conn;
}

static ferret_bool ferret__net_apply_timeout(FerretSocket socket_fd, int optname, ferret_i32 ms) {
    if (socket_fd == FERRET_INVALID_SOCKET || ms < 0) {
        return 0;
    }
#ifdef _WIN32
    {
        DWORD value = (DWORD)ms;
        return setsockopt(socket_fd, SOL_SOCKET, optname, (const char *)&value, (int)sizeof(value)) == 0;
    }
#else
    {
        struct timeval value;
        value.tv_sec = (time_t)(ms / 1000);
        value.tv_usec = (suseconds_t)((ms % 1000) * 1000);
        return setsockopt(socket_fd, SOL_SOCKET, optname, &value, (socklen_t)sizeof(value)) == 0;
    }
#endif
}

static ferret_bool ferret__net_apply_flag(FerretSocket socket_fd, int level, int optname, ferret_bool enabled) {
    int value = enabled ? 1 : 0;

    if (socket_fd == FERRET_INVALID_SOCKET) {
        return 0;
    }
#ifdef _WIN32
    return setsockopt(socket_fd, level, optname, (const char *)&value, (int)sizeof(value)) == 0;
#else
    return setsockopt(socket_fd, level, optname, &value, (socklen_t)sizeof(value)) == 0;
#endif
}

static FerretStr ferret__net_socket_addr(FerretSocket socket_fd, ferret_bool peer) {
    FerretStr out = {0};
    struct sockaddr_storage addr;
    socklen_t addr_len = (socklen_t)sizeof(addr);
    char host[256];
    char service[32];
    int status;
    char *text;
    size_t host_len;
    size_t service_len;

    if (socket_fd == FERRET_INVALID_SOCKET) {
        ferret__io_error_set(FERRET_IO_ERR_CLOSED);
        return out;
    }
    memset(&addr, 0, sizeof(addr));
    if (peer) {
        status = getpeername(socket_fd, (struct sockaddr *)&addr, &addr_len);
    } else {
        status = getsockname(socket_fd, (struct sockaddr *)&addr, &addr_len);
    }
    if (status != 0) {
        ferret__io_error_set(ferret__net_map_error_code(
#ifdef _WIN32
            WSAGetLastError(),
#else
            errno,
#endif
            0));
        return out;
    }
    status = getnameinfo((struct sockaddr *)&addr, addr_len, host, (socklen_t)sizeof(host), service, (socklen_t)sizeof(service), NI_NUMERICHOST | NI_NUMERICSERV);
    if (status != 0) {
        ferret__io_error_set(ferret__net_map_error_code(status, 1));
        return out;
    }
    host_len = strlen(host);
    service_len = strlen(service);
    text = (char *)malloc(host_len + service_len + 2);
    if (text == NULL) {
        ferret__io_error_set(FERRET_IO_ERR_UNKNOWN);
        return out;
    }
    memcpy(text, host, host_len);
    text[host_len] = ':';
    memcpy(text + host_len + 1, service, service_len);
    text[host_len + service_len + 1] = '\0';
    out.ptr = (ferret_u8 *)text;
    out.len = (ferret_usize)(host_len + service_len + 1);
    ferret__io_error_clear();
    return out;
}

static ferret_i32 ferret__net_map_error_code(int code, ferret_bool addrinfo_code) {
#ifdef _WIN32
    if (addrinfo_code) {
        switch (code) {
        case WSAHOST_NOT_FOUND:
        case WSANO_DATA:
            return FERRET_IO_ERR_NOT_FOUND;
        case WSAEAI_AGAIN:
            return FERRET_IO_ERR_TIMED_OUT;
        default:
            return FERRET_IO_ERR_UNKNOWN;
        }
    }
    switch (code) {
    case WSAEACCES:
        return FERRET_IO_ERR_PERMISSION_DENIED;
    case WSAECONNREFUSED:
        return FERRET_IO_ERR_CONNECTION_REFUSED;
    case WSAETIMEDOUT:
    case WSAEWOULDBLOCK:
        return FERRET_IO_ERR_TIMED_OUT;
    case WSAEADDRINUSE:
        return FERRET_IO_ERR_ADDRESS_IN_USE;
    case WSAECONNRESET:
    case WSAENOTCONN:
    case WSAESHUTDOWN:
        return FERRET_IO_ERR_CLOSED;
    case WSAEHOSTUNREACH:
    case WSAENETUNREACH:
        return FERRET_IO_ERR_NOT_FOUND;
    default:
        return FERRET_IO_ERR_UNKNOWN;
    }
#else
    if (addrinfo_code) {
        switch (code) {
        case EAI_NONAME:
            return FERRET_IO_ERR_NOT_FOUND;
#ifdef EAI_AGAIN
        case EAI_AGAIN:
            return FERRET_IO_ERR_TIMED_OUT;
#endif
        default:
            return FERRET_IO_ERR_UNKNOWN;
        }
    }
    switch (code) {
    case EACCES:
    case EPERM:
        return FERRET_IO_ERR_PERMISSION_DENIED;
    case ECONNREFUSED:
        return FERRET_IO_ERR_CONNECTION_REFUSED;
    case ETIMEDOUT:
#ifdef EAGAIN
    case EAGAIN:
#endif
#if defined(EWOULDBLOCK) && (!defined(EAGAIN) || EWOULDBLOCK != EAGAIN)
    case EWOULDBLOCK:
#endif
        return FERRET_IO_ERR_TIMED_OUT;
    case EADDRINUSE:
        return FERRET_IO_ERR_ADDRESS_IN_USE;
    case ECONNRESET:
    case ENOTCONN:
    case EPIPE:
        return FERRET_IO_ERR_CLOSED;
    case ENOENT:
    case EHOSTUNREACH:
    case ENETUNREACH:
        return FERRET_IO_ERR_NOT_FOUND;
    default:
        return FERRET_IO_ERR_UNKNOWN;
    }
#endif
}

static FerretSocket ferret__net_resolve_and_open(const char *host, const char *port_text, int passive, int do_listen) {
    struct addrinfo hints;
    struct addrinfo *results;
    struct addrinfo *current;
    FerretSocket socket_fd;
    int gai_status;

    memset(&hints, 0, sizeof(hints));
    hints.ai_family = AF_UNSPEC;
    hints.ai_socktype = SOCK_STREAM;
    hints.ai_protocol = IPPROTO_TCP;
    if (passive) {
        hints.ai_flags = AI_PASSIVE;
    }

    results = NULL;
    gai_status = getaddrinfo(host, port_text, &hints, &results);
    if (gai_status != 0) {
        ferret__io_error_set(ferret__net_map_error_code(gai_status, 1));
        return FERRET_INVALID_SOCKET;
    }

    socket_fd = FERRET_INVALID_SOCKET;
    for (current = results; current != NULL; current = current->ai_next) {
        socket_fd = socket(current->ai_family, current->ai_socktype, current->ai_protocol);
        if (socket_fd == FERRET_INVALID_SOCKET) {
            continue;
        }
        if (do_listen) {
            int enabled = 1;
#ifdef _WIN32
            setsockopt(socket_fd, SOL_SOCKET, SO_REUSEADDR, (const char *)&enabled, (int)sizeof(enabled));
#else
            setsockopt(socket_fd, SOL_SOCKET, SO_REUSEADDR, &enabled, (socklen_t)sizeof(enabled));
#endif
            if (bind(socket_fd, current->ai_addr, (socklen_t)current->ai_addrlen) == 0 && listen(socket_fd, 16) == 0) {
                break;
            }
        } else if (connect(socket_fd, current->ai_addr, (socklen_t)current->ai_addrlen) == 0) {
            break;
        }
        ferret__net_close_socket(socket_fd);
        socket_fd = FERRET_INVALID_SOCKET;
    }

    freeaddrinfo(results);
    if (socket_fd == FERRET_INVALID_SOCKET) {
        ferret__io_error_set(ferret__net_map_error_code(
#ifdef _WIN32
            WSAGetLastError(),
#else
            errno,
#endif
            0));
    } else {
        ferret__io_error_clear();
    }
    return socket_fd;
}

ferret_raw ferret_std_net_tcp_dial(const FerretStr *host, ferret_u16 port) {
    char *c_host;
    char port_text[6];
    FerretSocket socket_fd;

    if (!ferret__net_init() || host == NULL) {
        ferret__io_error_set(FERRET_IO_ERR_UNKNOWN);
        return NULL;
    }

    c_host = (char *)ferret_global_str_cstr(host);
    if (c_host == NULL) {
        ferret__io_error_set(FERRET_IO_ERR_UNKNOWN);
        return NULL;
    }

    snprintf(port_text, sizeof(port_text), "%u", (unsigned int)port);
    socket_fd = ferret__net_resolve_and_open(c_host, port_text, 0, 0);
    free(c_host);

    if (socket_fd == FERRET_INVALID_SOCKET) {
        return NULL;
    }
    return (ferret_raw)ferret__net_new_conn(socket_fd);
}

ferret_raw ferret_std_net_tcp_listen(const FerretStr *host, ferret_u16 port) {
    char *c_host;
    char port_text[6];
    FerretSocket socket_fd;
    FerretStdTcpListener *listener;

    if (!ferret__net_init() || host == NULL) {
        ferret__io_error_set(FERRET_IO_ERR_UNKNOWN);
        return NULL;
    }
    c_host = (char *)ferret_global_str_cstr(host);
    if (c_host == NULL) {
        ferret__io_error_set(FERRET_IO_ERR_UNKNOWN);
        return NULL;
    }

    snprintf(port_text, sizeof(port_text), "%u", (unsigned int)port);
    socket_fd = ferret__net_resolve_and_open(c_host, port_text, 1, 1);
    free(c_host);

    if (socket_fd == FERRET_INVALID_SOCKET) {
        return NULL;
    }
    listener = (FerretStdTcpListener *)calloc(1, sizeof(FerretStdTcpListener));
    if (listener == NULL) {
        ferret__net_close_socket(socket_fd);
        ferret__io_error_set(FERRET_IO_ERR_UNKNOWN);
        return NULL;
    }
    listener->socket_fd = socket_fd;
    listener->accept_timeout_ms = -1;
    ferret__io_error_clear();
    return (ferret_raw)listener;
}

ferret_raw ferret_std_net_tcp_accept(ferret_raw handle) {
    FerretStdTcpListener *listener = (FerretStdTcpListener *)handle;
    FerretSocket accepted;

    if (listener == NULL || listener->socket_fd == FERRET_INVALID_SOCKET) {
        ferret__io_error_set(FERRET_IO_ERR_CLOSED);
        return NULL;
    }
    if (listener->accept_timeout_ms >= 0) {
        fd_set readfds;
        int ready;
        struct timeval timeout;

        FD_ZERO(&readfds);
        FD_SET(listener->socket_fd, &readfds);
        timeout.tv_sec = (time_t)(listener->accept_timeout_ms / 1000);
        timeout.tv_usec = (suseconds_t)((listener->accept_timeout_ms % 1000) * 1000);
        ready = select(
#ifdef _WIN32
            0,
#else
            listener->socket_fd + 1,
#endif
            &readfds, NULL, NULL, &timeout);
        if (ready == 0) {
            ferret__io_error_set(FERRET_IO_ERR_TIMED_OUT);
            return NULL;
        }
        if (ready < 0) {
            ferret__io_error_set(ferret__net_map_error_code(
#ifdef _WIN32
                WSAGetLastError(),
#else
                errno,
#endif
                0));
            return NULL;
        }
    }
    accepted = accept(listener->socket_fd, NULL, NULL);
    if (accepted == FERRET_INVALID_SOCKET) {
        ferret__io_error_set(ferret__net_map_error_code(
#ifdef _WIN32
            WSAGetLastError(),
#else
            errno,
#endif
            0));
        return NULL;
    }
    ferret__io_error_clear();
    return (ferret_raw)ferret__net_new_conn(accepted);
}

ferret_usize ferret_std_net_tcp_write(ferret_raw handle, const FerretStr *text) {
    FerretStdTcpConn *conn = (FerretStdTcpConn *)handle;
    ferret_usize total;

    if (conn == NULL || conn->socket_fd == FERRET_INVALID_SOCKET) {
        ferret__io_error_set(FERRET_IO_ERR_CLOSED);
        return 0;
    }
    if (text == NULL || text->ptr == NULL || text->len == 0) {
        ferret__io_error_clear();
        return 0;
    }

    total = 0;
    while (total < text->len) {
        int sent = send(conn->socket_fd, (const char *)(text->ptr + total), (int)(text->len - total), 0);
        if (sent <= 0) {
            if (total == 0) {
                ferret__io_error_set(ferret__net_map_error_code(
#ifdef _WIN32
                    WSAGetLastError(),
#else
                    errno,
#endif
                    0));
            } else {
                ferret__io_error_clear();
            }
            break;
        }
        total += (ferret_usize)sent;
    }
    if (total == text->len) {
        ferret__io_error_clear();
    }
    return total;
}

FerretSliceU8 ferret_std_net_tcp_read(ferret_raw handle, ferret_usize size) {
    FerretStdTcpConn *conn = (FerretStdTcpConn *)handle;
    FerretSliceU8 out = {0};
    int received;

    if (conn == NULL || conn->socket_fd == FERRET_INVALID_SOCKET) {
        ferret__io_error_set(FERRET_IO_ERR_CLOSED);
        return out;
    }
    if (size == 0) {
        ferret__io_error_clear();
        return out;
    }
    if (!ferret__net_buffer_reserve(conn, size)) {
        ferret__io_error_set(FERRET_IO_ERR_UNKNOWN);
        return out;
    }

    received = recv(conn->socket_fd, (char *)conn->read_buf, (int)size, 0);
    if (received == 0) {
        ferret__io_error_clear();
        return out;
    }
    if (received < 0) {
        ferret__io_error_set(ferret__net_map_error_code(
#ifdef _WIN32
            WSAGetLastError(),
#else
            errno,
#endif
            0));
        return out;
    }

    out.ptr = conn->read_buf;
    out.len = (ferret_usize)received;
    ferret__io_error_clear();
    return out;
}

ferret_usize ferret_std_net_tcp_set_accept_timeout(ferret_raw handle, ferret_i32 ms) {
    FerretStdTcpListener *listener = (FerretStdTcpListener *)handle;

    if (listener == NULL || listener->socket_fd == FERRET_INVALID_SOCKET) {
        ferret__io_error_set(FERRET_IO_ERR_CLOSED);
        return 0;
    }
    if (ms < -1) {
        ferret__io_error_set(FERRET_IO_ERR_UNKNOWN);
        return 0;
    }
    listener->accept_timeout_ms = ms;
    ferret__io_error_clear();
    return 0;
}

ferret_usize ferret_std_net_tcp_set_read_timeout(ferret_raw handle, ferret_i32 ms) {
    FerretStdTcpConn *conn = (FerretStdTcpConn *)handle;

    if (conn == NULL || conn->socket_fd == FERRET_INVALID_SOCKET) {
        ferret__io_error_set(FERRET_IO_ERR_CLOSED);
        return 0;
    }
    if (!ferret__net_apply_timeout(conn->socket_fd, SO_RCVTIMEO, ms)) {
        ferret__io_error_set(ferret__net_map_error_code(
#ifdef _WIN32
            WSAGetLastError(),
#else
            errno,
#endif
            0));
        return 0;
    }
    ferret__io_error_clear();
    return 0;
}

ferret_usize ferret_std_net_tcp_set_nodelay(ferret_raw handle, ferret_bool enabled) {
    FerretStdTcpConn *conn = (FerretStdTcpConn *)handle;

    if (conn == NULL || conn->socket_fd == FERRET_INVALID_SOCKET) {
        ferret__io_error_set(FERRET_IO_ERR_CLOSED);
        return 0;
    }
    if (!ferret__net_apply_flag(conn->socket_fd, IPPROTO_TCP, TCP_NODELAY, enabled)) {
        ferret__io_error_set(ferret__net_map_error_code(
#ifdef _WIN32
            WSAGetLastError(),
#else
            errno,
#endif
            0));
        return 0;
    }
    ferret__io_error_clear();
    return 0;
}

ferret_usize ferret_std_net_tcp_set_keepalive(ferret_raw handle, ferret_bool enabled) {
    FerretStdTcpConn *conn = (FerretStdTcpConn *)handle;

    if (conn == NULL || conn->socket_fd == FERRET_INVALID_SOCKET) {
        ferret__io_error_set(FERRET_IO_ERR_CLOSED);
        return 0;
    }
    if (!ferret__net_apply_flag(conn->socket_fd, SOL_SOCKET, SO_KEEPALIVE, enabled)) {
        ferret__io_error_set(ferret__net_map_error_code(
#ifdef _WIN32
            WSAGetLastError(),
#else
            errno,
#endif
            0));
        return 0;
    }
    ferret__io_error_clear();
    return 0;
}

ferret_usize ferret_std_net_tcp_shutdown_read(ferret_raw handle) {
    FerretStdTcpConn *conn = (FerretStdTcpConn *)handle;

    if (conn == NULL || conn->socket_fd == FERRET_INVALID_SOCKET) {
        ferret__io_error_set(FERRET_IO_ERR_CLOSED);
        return 0;
    }
    if (shutdown(conn->socket_fd, FERRET_SHUT_RD) != 0) {
        ferret__io_error_set(ferret__net_map_error_code(
#ifdef _WIN32
            WSAGetLastError(),
#else
            errno,
#endif
            0));
        return 0;
    }
    ferret__io_error_clear();
    return 0;
}

ferret_usize ferret_std_net_tcp_shutdown_write(ferret_raw handle) {
    FerretStdTcpConn *conn = (FerretStdTcpConn *)handle;

    if (conn == NULL || conn->socket_fd == FERRET_INVALID_SOCKET) {
        ferret__io_error_set(FERRET_IO_ERR_CLOSED);
        return 0;
    }
    if (shutdown(conn->socket_fd, FERRET_SHUT_WR) != 0) {
        ferret__io_error_set(ferret__net_map_error_code(
#ifdef _WIN32
            WSAGetLastError(),
#else
            errno,
#endif
            0));
        return 0;
    }
    ferret__io_error_clear();
    return 0;
}

FerretStr ferret_std_net_tcp_local_addr(ferret_raw handle) {
    FerretStdTcpConn *conn = (FerretStdTcpConn *)handle;
    if (conn == NULL || conn->socket_fd == FERRET_INVALID_SOCKET) {
        ferret__io_error_set(FERRET_IO_ERR_CLOSED);
        return (FerretStr){0};
    }
    return ferret__net_socket_addr(conn->socket_fd, 0);
}

FerretStr ferret_std_net_tcp_listener_local_addr(ferret_raw handle) {
    FerretStdTcpListener *listener = (FerretStdTcpListener *)handle;
    if (listener == NULL || listener->socket_fd == FERRET_INVALID_SOCKET) {
        ferret__io_error_set(FERRET_IO_ERR_CLOSED);
        return (FerretStr){0};
    }
    return ferret__net_socket_addr(listener->socket_fd, 0);
}

FerretStr ferret_std_net_tcp_peer_addr(ferret_raw handle) {
    FerretStdTcpConn *conn = (FerretStdTcpConn *)handle;
    if (conn == NULL || conn->socket_fd == FERRET_INVALID_SOCKET) {
        ferret__io_error_set(FERRET_IO_ERR_CLOSED);
        return (FerretStr){0};
    }
    return ferret__net_socket_addr(conn->socket_fd, 1);
}

ferret_usize ferret_std_net_tcp_set_write_timeout(ferret_raw handle, ferret_i32 ms) {
    FerretStdTcpConn *conn = (FerretStdTcpConn *)handle;

    if (conn == NULL || conn->socket_fd == FERRET_INVALID_SOCKET) {
        ferret__io_error_set(FERRET_IO_ERR_CLOSED);
        return 0;
    }
    if (!ferret__net_apply_timeout(conn->socket_fd, SO_SNDTIMEO, ms)) {
        ferret__io_error_set(ferret__net_map_error_code(
#ifdef _WIN32
            WSAGetLastError(),
#else
            errno,
#endif
            0));
        return 0;
    }
    ferret__io_error_clear();
    return 0;
}

void ferret_std_net_tcp_close(ferret_raw handle) {
    FerretStdTcpConn *conn = (FerretStdTcpConn *)handle;
    FerretSocket socket_fd;

    if (conn == NULL) {
        return;
    }
    socket_fd = conn->socket_fd;
    conn->socket_fd = FERRET_INVALID_SOCKET;
    free(conn->read_buf);
    free(conn);
    ferret__net_close_socket(socket_fd);
}

void ferret_std_net_tcp_close_listener(ferret_raw handle) {
    FerretStdTcpListener *listener = (FerretStdTcpListener *)handle;
    FerretSocket socket_fd;

    if (listener == NULL) {
        return;
    }
    socket_fd = listener->socket_fd;
    listener->socket_fd = FERRET_INVALID_SOCKET;
    free(listener);
    ferret__net_close_socket(socket_fd);
}
