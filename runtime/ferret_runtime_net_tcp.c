#ifndef _WIN32
#define _POSIX_C_SOURCE 200112L
#endif

#include "ferret_runtime_internal.h"

#include <stdio.h>

#ifdef _WIN32
#include <winsock2.h>
#include <ws2tcpip.h>
typedef SOCKET FerretSocket;
#define FERRET_INVALID_SOCKET INVALID_SOCKET
#else
#include <netdb.h>
#include <sys/socket.h>
#include <sys/types.h>
#include <unistd.h>
typedef int FerretSocket;
#define FERRET_INVALID_SOCKET (-1)
#endif

typedef struct {
    FerretSocket socket_fd;
    ferret_u8   *read_buf;
    ferret_usize read_cap;
} FerretStdTcpConn;

typedef struct {
    FerretSocket socket_fd;
} FerretStdTcpListener;

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
        return NULL;
    }
    conn->socket_fd = socket_fd;
    return conn;
}

static FerretSocket ferret__net_resolve_and_open(const char *host, const char *port_text, int passive, int do_listen) {
    struct addrinfo hints;
    struct addrinfo *results;
    struct addrinfo *current;
    FerretSocket socket_fd;

    memset(&hints, 0, sizeof(hints));
    hints.ai_family = AF_UNSPEC;
    hints.ai_socktype = SOCK_STREAM;
    hints.ai_protocol = IPPROTO_TCP;
    if (passive) {
        hints.ai_flags = AI_PASSIVE;
    }

    results = NULL;
    if (getaddrinfo(host, port_text, &hints, &results) != 0) {
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
    return socket_fd;
}

ferret_raw ferret_std_net_tcp_dial(const FerretStr *host, ferret_u16 port) {
    char *c_host;
    char port_text[6];
    FerretSocket socket_fd;

    if (!ferret__net_init() || host == NULL) {
        return NULL;
    }

    c_host = (char *)ferret_global_str_cstr(host);
    if (c_host == NULL) {
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
        return NULL;
    }
    c_host = (char *)ferret_global_str_cstr(host);
    if (c_host == NULL) {
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
        return NULL;
    }
    listener->socket_fd = socket_fd;
    return (ferret_raw)listener;
}

ferret_raw ferret_std_net_tcp_accept(ferret_raw handle) {
    FerretStdTcpListener *listener = (FerretStdTcpListener *)handle;
    FerretSocket accepted;

    if (listener == NULL || listener->socket_fd == FERRET_INVALID_SOCKET) {
        return NULL;
    }
    accepted = accept(listener->socket_fd, NULL, NULL);
    if (accepted == FERRET_INVALID_SOCKET) {
        return NULL;
    }
    return (ferret_raw)ferret__net_new_conn(accepted);
}

ferret_usize ferret_std_net_tcp_write(ferret_raw handle, const FerretStr *text) {
    FerretStdTcpConn *conn = (FerretStdTcpConn *)handle;
    ferret_usize total;

    if (conn == NULL || conn->socket_fd == FERRET_INVALID_SOCKET || text == NULL || text->ptr == NULL || text->len == 0) {
        return 0;
    }

    total = 0;
    while (total < text->len) {
        int sent = send(conn->socket_fd, (const char *)(text->ptr + total), (int)(text->len - total), 0);
        if (sent <= 0) {
            break;
        }
        total += (ferret_usize)sent;
    }
    return total;
}

FerretSliceU8 ferret_std_net_tcp_read(ferret_raw handle, ferret_usize size) {
    FerretStdTcpConn *conn = (FerretStdTcpConn *)handle;
    FerretSliceU8 out = {0};
    int received;

    if (conn == NULL || conn->socket_fd == FERRET_INVALID_SOCKET || size == 0) {
        return out;
    }
    if (!ferret__net_buffer_reserve(conn, size)) {
        return out;
    }

    received = recv(conn->socket_fd, (char *)conn->read_buf, (int)size, 0);
    if (received <= 0) {
        return out;
    }

    out.ptr = conn->read_buf;
    out.len = (ferret_usize)received;
    return out;
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
