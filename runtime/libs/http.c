// Ferret runtime: HTTP server implementation for std/http
// Minimal Express-like server with routing and middleware.

#include <stdio.h>
#include <stdlib.h>
#include <stdint.h>
#include <stdbool.h>
#include <string.h>
#include "../core/alloc.h"
#include <errno.h>
#ifndef _WIN32
#include <strings.h>
#endif
#include <ctype.h>

#ifdef _WIN32
#include <winsock2.h>
#include <ws2tcpip.h>
typedef SOCKET ferret_socket_t;
#define FERRET_INVALID_SOCKET INVALID_SOCKET
#define ferret_close_socket closesocket
#define strcasecmp _stricmp
#else
#include <sys/socket.h>
#include <netinet/in.h>
#include <arpa/inet.h>
#include <unistd.h>
typedef int ferret_socket_t;
#define FERRET_INVALID_SOCKET (-1)
#define ferret_close_socket close
#endif

#include "../core/alloc.h"
#include "../core/array.h"
#include "../core/map.h"
#include "../core/optional.h"
#include "../core/type_system.h"
#include "../core/runtime_naming.h"
#include "../core/result.h"

// Define the module prefix for this file (implements ferret_libs/net/http.fer)
#define MODULE_PREFIX ferret_net_http

#define REQUEST FERRET_TYPE(Request)
#define RESPONSE FERRET_TYPE(Response)
#define RESPONSE_T FERRET_TYPE(response_t)
#define APP FERRET_TYPE(App)

#define FERRET_HTTP_MAX_BODY (10 * 1024 * 1024)
#define FERRET_HTTP_MAX_STATIC_FILE (10 * 1024 * 1024)

// Type definitions using naming macros (match ferret_libs/std/http.fer types)
typedef struct {
    int64_t handle;
} FERRET_TYPE(App);

typedef struct {
    char* Method;
    char* Path;
    char* Url;
    ferret_map_t* Query;
    ferret_map_t* Params;
    ferret_map_t* Headers;
    char* Body;
    ferret_array_t* BodyBytes;
    char* IP;
} FERRET_TYPE(Request);

typedef struct {
    int64_t handle;
} FERRET_TYPE(Response);

typedef struct {
    char* name;
    char* value;
} FERRET_TYPE(header_t);

typedef struct {
    ferret_socket_t client_fd;
    int status;
    FERRET_TYPE(header_t)* headers;
    size_t header_count;
    size_t header_cap;
    bool sent;
} RESPONSE_T;

typedef struct {
    char* method;
    char* pattern;
    void* handler; // Ferret closure pointer
} FERRET_TYPE(route_t);

typedef struct {
    void* handler; // Ferret closure pointer
} FERRET_TYPE(middleware_t);

typedef struct {
    char* prefix;
    char* dir;
} FERRET_TYPE(static_t);

typedef struct {
    ferret_socket_t server_fd;
    bool running;
    FERRET_TYPE(route_t)* routes;
    size_t route_count;
    size_t route_cap;
    FERRET_TYPE(middleware_t)* middleware;
    size_t middleware_count;
    size_t middleware_cap;
    FERRET_TYPE(static_t)* statics;
    size_t static_count;
    size_t static_cap;
} FERRET_TYPE(app_t);

typedef void (*FERRET_TYPE(handler_fn))(void* env, REQUEST* req, uint64_t req_heap, RESPONSE* res, uint64_t res_heap);
typedef void (*FERRET_TYPE(middleware_fn))(void* env, REQUEST* req, uint64_t req_heap, RESPONSE* res, uint64_t res_heap, void* next);
typedef void (*FERRET_TYPE(next_fn))(void* env);

typedef struct {
    void* __fn;
    void* ctx;
} FERRET_TYPE(next_closure_t);

typedef struct {
    FERRET_TYPE(app_t)* app;
    REQUEST* req;
    RESPONSE* res;
    size_t index;
    FERRET_TYPE(next_closure_t)* next_closure;
} FERRET_TYPE(next_ctx_t);

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

static void FERRET_FUNC(lowercase)(char* s) {
    if (!s) {
        return;
    }
    for (; *s; s++) {
        *s = (char)tolower((unsigned char)*s);
    }
}

static char* FERRET_FUNC(strndup)(const char* s, size_t n) {
    char* out = (char*)ferret_alloc(n + 1);
    if (!out) {
        return NULL;
    }
    memcpy(out, s, n);
    out[n] = '\0';
    return out;
}

static char* FERRET_FUNC(url_decode)(const char* src) {
    if (!src) {
        return NULL;
    }
    size_t len = strlen(src);
    char* out = (char*)ferret_alloc(len + 1);
    if (!out) {
        return NULL;
    }
    char* dst = out;
    for (size_t i = 0; i < len; i++) {
        char c = src[i];
        if (c == '%' && i + 2 < len) {
            char h1 = src[i + 1];
            char h2 = src[i + 2];
            if (isxdigit((unsigned char)h1) && isxdigit((unsigned char)h2)) {
                int v1 = isdigit((unsigned char)h1) ? h1 - '0' : (tolower((unsigned char)h1) - 'a' + 10);
                int v2 = isdigit((unsigned char)h2) ? h2 - '0' : (tolower((unsigned char)h2) - 'a' + 10);
                *dst++ = (char)((v1 << 4) | v2);
                i += 2;
                continue;
            }
        }
        if (c == '+') {
            *dst++ = ' ';
        } else {
            *dst++ = c;
        }
    }
    *dst = '\0';
    return out;
}

static ferret_map_t* FERRET_FUNC(new_str_map)(void) {
    return ferret_map_new_str(sizeof(char*), sizeof(char*), (ferret_type_info_t*)&ferret_type_str, (ferret_type_info_t*)&ferret_type_str);
}

static void FERRET_FUNC(map_set)(ferret_map_t* map, const char* key, const char* value) {
    if (!map || !key) {
        return;
    }
    char* key_copy = FERRET_FUNC(strdup)(key);
    char* val_copy = value ? FERRET_FUNC(strdup)(value) : NULL;
    ferret_map_set(map, &key_copy, &val_copy);
}

static void FERRET_FUNC(headers_add)(RESPONSE_T* res, const char* name, const char* value) {
    if (!res || !name) {
        return;
    }
    // Replace existing header (case-insensitive)
    for (size_t i = 0; i < res->header_count; i++) {
        if (strcasecmp(res->headers[i].name, name) == 0) {
            res->headers[i].value = FERRET_FUNC(strdup)(value ? value : "");
            return;
        }
    }
    if (res->header_count == res->header_cap) {
        size_t next_cap = res->header_cap == 0 ? 8 : res->header_cap * 2;
        FERRET_TYPE(header_t)* next = (FERRET_TYPE(header_t)*)ferret_realloc(res->headers, next_cap * sizeof(FERRET_TYPE(header_t)));
        if (!next) {
            return;
        }
        res->headers = next;
        res->header_cap = next_cap;
    }
    res->headers[res->header_count].name = FERRET_FUNC(strdup)(name);
    res->headers[res->header_count].value = FERRET_FUNC(strdup)(value ? value : "");
    res->header_count++;
}

static bool FERRET_FUNC(headers_has)(RESPONSE_T* res, const char* name) {
    if (!res || !name) {
        return false;
    }
    for (size_t i = 0; i < res->header_count; i++) {
        if (strcasecmp(res->headers[i].name, name) == 0) {
            return true;
        }
    }
    return false;
}

static const char* FERRET_FUNC(status_text)(int status) {
    switch (status) {
        case 200: return "OK";
        case 201: return "Created";
        case 202: return "Accepted";
        case 204: return "No Content";
        case 301: return "Moved Permanently";
        case 302: return "Found";
        case 304: return "Not Modified";
        case 400: return "Bad Request";
        case 401: return "Unauthorized";
        case 403: return "Forbidden";
        case 404: return "Not Found";
        case 405: return "Method Not Allowed";
        case 413: return "Payload Too Large";
        case 500: return "Internal Server Error";
        default: return "OK";
    }
}

static bool FERRET_FUNC(send_all)(ferret_socket_t fd, const char* data, size_t len) {
    if (!data || len == 0) {
        return true;
    }
    size_t sent = 0;
    while (sent < len) {
        int chunk = send(fd, data + sent, (int)(len - sent), 0);
        if (chunk <= 0) {
            return false;
        }
        sent += (size_t)chunk;
    }
    return true;
}

static bool FERRET_FUNC(send_response)(RESPONSE_T* res, const char* body, size_t body_len) {
    if (!res || res->sent) {
        return false;
    }
    const char* status_text = FERRET_FUNC(status_text)(res->status);
    char header_buf[512];
    int header_len = snprintf(header_buf, sizeof(header_buf),
        "HTTP/1.1 %d %s\r\n", res->status, status_text);
    if (header_len <= 0) {
        return false;
    }

    if (!FERRET_FUNC(headers_has)(res, "Content-Length")) {
        char len_buf[64];
        snprintf(len_buf, sizeof(len_buf), "%zu", body_len);
        FERRET_FUNC(headers_add)(res, "Content-Length", len_buf);
    }
    if (!FERRET_FUNC(headers_has)(res, "Connection")) {
        FERRET_FUNC(headers_add)(res, "Connection", "close");
    }

    if (!FERRET_FUNC(send_all)(res->client_fd, header_buf, (size_t)header_len)) {
        return false;
    }
    for (size_t i = 0; i < res->header_count; i++) {
        const char* name = res->headers[i].name ? res->headers[i].name : "";
        const char* value = res->headers[i].value ? res->headers[i].value : "";
        char line_buf[1024];
        int line_len = snprintf(line_buf, sizeof(line_buf), "%s: %s\r\n", name, value);
        if (line_len <= 0) {
            continue;
        }
        if (!FERRET_FUNC(send_all)(res->client_fd, line_buf, (size_t)line_len)) {
            return false;
        }
    }
    if (!FERRET_FUNC(send_all)(res->client_fd, "\r\n", 2)) {
        return false;
    }
    if (body_len > 0 && body != NULL) {
        if (!FERRET_FUNC(send_all)(res->client_fd, body, body_len)) {
            return false;
        }
    }
    res->sent = true;
    return true;
}

static bool FERRET_FUNC(parse_content_length)(const char* value, size_t* out_len) {
    if (!value || !out_len) {
        return false;
    }
    errno = 0;
    char* end = NULL;
    long long parsed = strtoll(value, &end, 10);
    if (errno != 0 || end == value) {
        return false;
    }
    while (*end == ' ' || *end == '\t') {
        end++;
    }
    if (*end != '\0') {
        return false;
    }
    if (parsed < 0 || (unsigned long long)parsed > FERRET_HTTP_MAX_BODY) {
        return false;
    }
    *out_len = (size_t)parsed;
    return true;
}

static void FERRET_FUNC(write_optional_str_ptr)(void* out, const char* value) {
    if (!out) {
        return;
    }
    void* opt = ferret_optional_alloc_none(sizeof(char*), sizeof(void*));
    if (!opt) {
        *(void**)out = NULL;
        return;
    }
    if (value) {
        memcpy(opt, &value, sizeof(char*));
        uint8_t* flag = (uint8_t*)opt + sizeof(char*);
        *flag = 1;
    }
    *(void**)out = opt;
}

static void FERRET_FUNC(call_handler)(void* handler, REQUEST* req, RESPONSE* res) {
    if (!handler) {
        return;
    }
    void* fn_ptr = *(void**)handler;
    if (!fn_ptr) {
        return;
    }
    FERRET_TYPE(handler_fn) fn = (FERRET_TYPE(handler_fn))fn_ptr;
    fn(handler, req, 0, res, 0);
}

static void FERRET_FUNC(call_middleware)(void* handler, REQUEST* req, RESPONSE* res, void* next) {
    if (!handler) {
        return;
    }
    void* fn_ptr = *(void**)handler;
    if (!fn_ptr) {
        return;
    }
    FERRET_TYPE(middleware_fn) fn = (FERRET_TYPE(middleware_fn))fn_ptr;
    fn(handler, req, 0, res, 0, next);
}

static void FERRET_FUNC(run_route)(FERRET_TYPE(app_t)* app, REQUEST* req, RESPONSE* res);

static void FERRET_FUNC(next_thunk)(void* env) {
    FERRET_TYPE(next_closure_t)* clo = (FERRET_TYPE(next_closure_t)*)env;
    if (!clo || !clo->ctx) {
        return;
    }
    FERRET_TYPE(next_ctx_t)* ctx = (FERRET_TYPE(next_ctx_t)*)clo->ctx;
    if (ctx->index < ctx->app->middleware_count) {
        void* handler = ctx->app->middleware[ctx->index].handler;
        ctx->index++;
        FERRET_FUNC(call_middleware)(handler, ctx->req, ctx->res, ctx->next_closure);
        return;
    }
    FERRET_FUNC(run_route)(ctx->app, ctx->req, ctx->res);
}

static bool FERRET_FUNC(match_route)(const char* pattern, const char* path, ferret_map_t* params) {
    if (!pattern || !path) {
        return false;
    }
    const char* p = pattern;
    const char* s = path;
    while (*p == '/') p++;
    while (*s == '/') s++;
    while (*p || *s) {
        const char* p_next = strchr(p, '/');
        const char* s_next = strchr(s, '/');
        size_t p_len = p_next ? (size_t)(p_next - p) : strlen(p);
        size_t s_len = s_next ? (size_t)(s_next - s) : strlen(s);
        if (p_len == 0 && s_len == 0) {
            break;
        }
        if (p_len == 0 || s_len == 0) {
            return false;
        }
        if (p[0] == ':') {
            if (params) {
                char* key = FERRET_FUNC(strndup)(p + 1, p_len - 1);
                char* val_raw = FERRET_FUNC(strndup)(s, s_len);
                char* val = FERRET_FUNC(url_decode)(val_raw ? val_raw : "");
                FERRET_FUNC(map_set)(params, key ? key : "", val ? val : "");
            }
        } else {
            if (p_len != s_len || strncmp(p, s, p_len) != 0) {
                return false;
            }
        }
        if (!p_next && !s_next) {
            return true;
        }
        if ((p_next && !s_next) || (!p_next && s_next)) {
            return false;
        }
        p = p_next + 1;
        s = s_next + 1;
    }
    return true;
}

static void FERRET_FUNC(run_route)(FERRET_TYPE(app_t)* app, REQUEST* req, RESPONSE* res) {
    if (!app || !req || !res) {
        return;
    }

    // Static files first
    for (size_t i = 0; i < app->static_count; i++) {
        FERRET_TYPE(static_t)* st = &app->statics[i];
        if (!st->prefix || !st->dir) {
            continue;
        }
        size_t prefix_len = strlen(st->prefix);
        if (strncmp(req->Path, st->prefix, prefix_len) != 0) {
            continue;
        }
        const char* rel = req->Path + prefix_len;
        if (*rel == '/') rel++;
        if (*rel == '\0') {
            continue;
        }
        if (strstr(rel, "..")) {
            RESPONSE_T* resp = (RESPONSE_T*)(intptr_t)res->handle;
            if (resp) {
                resp->status = 403;
                FERRET_FUNC(send_response)(resp, "Forbidden", 9);
            }
            return;
        }
        size_t path_len = strlen(st->dir) + 1 + strlen(rel) + 1;
        char* full = (char*)ferret_alloc(path_len);
        if (!full) {
            return;
        }
        snprintf(full, path_len, "%s/%s", st->dir, rel);
        FILE* f = fopen(full, "rb");
        ferret_free(full);
        if (!f) {
            continue;
        }
        fseek(f, 0, SEEK_END);
        long fsize = ftell(f);
        fseek(f, 0, SEEK_SET);
        if (fsize < 0) {
            fclose(f);
            continue;
        }
        if ((size_t)fsize > FERRET_HTTP_MAX_STATIC_FILE) {
            fclose(f);
            RESPONSE_T* resp = (RESPONSE_T*)(intptr_t)res->handle;
            if (resp) {
                resp->status = 413;
                FERRET_FUNC(send_response)(resp, "Payload Too Large", 17);
            }
            return;
        }
        char* buf = (char*)ferret_alloc((size_t)fsize);
        if (!buf) {
            fclose(f);
            continue;
        }
        size_t total_read = fread(buf, 1, (size_t)fsize, f);
        int read_error = ferror(f);
        fclose(f);
        if (read_error) {
            ferret_free(buf);
            continue;
        }
        if (total_read == 0) {
            ferret_free(buf);
            continue;
        }
        RESPONSE_T* resp = (RESPONSE_T*)(intptr_t)res->handle;
        if (resp) {
            resp->status = 200;
            FERRET_FUNC(send_response)(resp, buf, total_read);
        }
        ferret_free(buf);
        return;
    }

    // Route match
    for (size_t i = 0; i < app->route_count; i++) {
        FERRET_TYPE(route_t)* route = &app->routes[i];
        if (!route->handler || !route->pattern || !route->method) {
            continue;
        }
        if (strcmp(route->method, "*") != 0 && strcasecmp(route->method, req->Method) != 0) {
            continue;
        }
        if (!FERRET_FUNC(match_route)(route->pattern, req->Path, NULL)) {
            continue;
        }
        ferret_map_t* params = FERRET_FUNC(new_str_map)();
        FERRET_FUNC(match_route)(route->pattern, req->Path, params);
        req->Params = params;
        FERRET_FUNC(call_handler)(route->handler, req, res);
        return;
    }

    RESPONSE_T* resp = (RESPONSE_T*)(intptr_t)res->handle;
    if (resp) {
        resp->status = 404;
        FERRET_FUNC(send_response)(resp, "Not Found", 9);
    }
}

static bool FERRET_FUNC(parse_request)(ferret_socket_t fd, REQUEST* out_req) {
    if (!out_req) {
        return false;
    }

    const size_t max_header = 1024 * 64;
    size_t cap = 4096;
    size_t len = 0;
    char* buf = (char*)ferret_alloc(cap);
    if (!buf) {
        return false;
    }

    size_t header_end = 0;
    while (1) {
        if (len + 1024 > cap) {
            size_t next_cap = cap * 2;
            if (next_cap > max_header) {
                ferret_free(buf);
                return false;
            }
            char* next = (char*)ferret_realloc(buf, next_cap);
            if (!next) {
                ferret_free(buf);
                return false;
            }
            buf = next;
            cap = next_cap;
        }
        int r = recv(fd, buf + len, (int)(cap - len), 0);
        if (r <= 0) {
            ferret_free(buf);
            return false;
        }
        len += (size_t)r;
        char* found = NULL;
        if (len >= 4) {
            found = strstr(buf, "\r\n\r\n");
        }
        if (found) {
            header_end = (size_t)(found - buf) + 4;
            break;
        }
        if (len > max_header) {
            ferret_free(buf);
            return false;
        }
    }

    buf[header_end - 2] = '\0';
    char* line = buf;
    char* next_line = strstr(line, "\r\n");
    if (!next_line) {
        ferret_free(buf);
        return false;
    }
    *next_line = '\0';
    char* method = strtok(line, " ");
    char* url = strtok(NULL, " ");
    if (!method || !url) {
        ferret_free(buf);
        return false;
    }
    out_req->Method = FERRET_FUNC(strdup)(method);
    out_req->Url = FERRET_FUNC(strdup)(url);

    // Path and query
    char* qmark = strchr(url, '?');
    if (qmark) {
        out_req->Path = FERRET_FUNC(strndup)(url, (size_t)(qmark - url));
    } else {
        out_req->Path = FERRET_FUNC(strdup)(url);
    }

    out_req->Headers = FERRET_FUNC(new_str_map)();
    out_req->Query = FERRET_FUNC(new_str_map)();
    out_req->Params = FERRET_FUNC(new_str_map)();

    // Headers
    line = next_line + 2;
    size_t content_length = 0;
    while (line && *line) {
        next_line = strstr(line, "\r\n");
        if (!next_line) {
            break;
        }
        *next_line = '\0';
        char* colon = strchr(line, ':');
        if (colon) {
            *colon = '\0';
            char* key = line;
            char* value = colon + 1;
            while (*value == ' ' || *value == '\t') value++;
            char* key_copy = FERRET_FUNC(strdup)(key);
            FERRET_FUNC(lowercase)(key_copy);
            if (strcasecmp(key_copy, "content-length") == 0) {
                if (!FERRET_FUNC(parse_content_length)(value, &content_length)) {
                    ferret_free(buf);
                    return false;
                }
            }
            FERRET_FUNC(map_set)(out_req->Headers, key_copy, value);
        }
        line = next_line + 2;
    }

    // Query params
    if (qmark) {
        char* query = qmark + 1;
        char* qcopy = FERRET_FUNC(strdup)(query);
        if (qcopy) {
            char* tok = strtok(qcopy, "&");
            while (tok) {
                char* eq = strchr(tok, '=');
                if (eq) {
                    *eq = '\0';
                    char* k = FERRET_FUNC(url_decode)(tok);
                    char* v = FERRET_FUNC(url_decode)(eq + 1);
                    FERRET_FUNC(map_set)(out_req->Query, k ? k : "", v ? v : "");
                    if (k) ferret_free(k);
                    if (v) ferret_free(v);
                } else {
                    char* k = FERRET_FUNC(url_decode)(tok);
                    FERRET_FUNC(map_set)(out_req->Query, k ? k : "", "");
                    if (k) ferret_free(k);
                }
                tok = strtok(NULL, "&");
            }
            ferret_free(qcopy);
        }
    }

    // Body
    size_t body_len = 0;
    char* body = NULL;
    if (content_length > 0) {
        body = (char*)ferret_alloc(content_length + 1);
        if (!body) {
            ferret_free(buf);
            return false;
        }
        size_t already = len - header_end;
        size_t to_copy = already < content_length ? already : content_length;
        memcpy(body, buf + header_end, to_copy);
        size_t offset = to_copy;
        while (offset < content_length) {
            int r = recv(fd, body + offset, (int)(content_length - offset), 0);
            if (r <= 0) {
                break;
            }
            offset += (size_t)r;
        }
        body_len = offset;
        body[body_len] = '\0';
    } else {
        body = FERRET_FUNC(strdup)("");
        body_len = 0;
    }

    out_req->Body = body;
    if (body_len > 0) {
        uint8_t* bytes = (uint8_t*)ferret_alloc(body_len);
        if (bytes) {
            memcpy(bytes, body, body_len);
            out_req->BodyBytes = ferret_array_from_data(bytes, (int32_t)body_len, (int32_t)body_len, sizeof(uint8_t), (ferret_type_info_t*)&ferret_type_byte);
        }
    } else {
        out_req->BodyBytes = ferret_array_new(sizeof(uint8_t), 0, (ferret_type_info_t*)&ferret_type_byte);
    }

    ferret_free(buf);
    return true;
}

static void FERRET_FUNC(set_ip)(ferret_socket_t fd, REQUEST* req) {
    if (!req) {
        return;
    }
    char ipbuf[128] = {0};
    struct sockaddr_storage addr;
    socklen_t addrlen = sizeof(addr);
    if (getpeername(fd, (struct sockaddr*)&addr, &addrlen) == 0) {
        void* src = NULL;
        if (addr.ss_family == AF_INET) {
            struct sockaddr_in* s = (struct sockaddr_in*)&addr;
            src = &s->sin_addr;
        } else if (addr.ss_family == AF_INET6) {
            struct sockaddr_in6* s = (struct sockaddr_in6*)&addr;
            src = &s->sin6_addr;
        }
        if (src) {
            inet_ntop(addr.ss_family, src, ipbuf, sizeof(ipbuf));
        }
    }
    req->IP = FERRET_FUNC(strdup)(ipbuf[0] ? ipbuf : "0.0.0.0");
}

static void FERRET_FUNC(handle_connection)(FERRET_TYPE(app_t)* app, ferret_socket_t client_fd) {
    REQUEST req = {0};
    if (!FERRET_FUNC(parse_request)(client_fd, &req)) {
        ferret_close_socket(client_fd);
        return;
    }
    FERRET_FUNC(set_ip)(client_fd, &req);

    RESPONSE_T resp_state = {0};
    resp_state.client_fd = client_fd;
    resp_state.status = 200;
    resp_state.sent = false;

    RESPONSE res = {0};
    res.handle = (int64_t)(intptr_t)&resp_state;

    if (app->middleware_count > 0) {
        FERRET_TYPE(next_ctx_t)* ctx = (FERRET_TYPE(next_ctx_t)*)ferret_alloc(sizeof(FERRET_TYPE(next_ctx_t)));
        FERRET_TYPE(next_closure_t)* next = (FERRET_TYPE(next_closure_t)*)ferret_alloc(sizeof(FERRET_TYPE(next_closure_t)));
        if (ctx && next) {
            ctx->app = app;
            ctx->req = &req;
            ctx->res = &res;
            ctx->index = 0;
            ctx->next_closure = next;
            next->__fn = (void*)FERRET_FUNC(next_thunk);
            next->ctx = ctx;
            FERRET_FUNC(next_thunk)(next);
        } else {
            FERRET_FUNC(run_route)(app, &req, &res);
        }
    } else {
        FERRET_FUNC(run_route)(app, &req, &res);
    }

    if (!resp_state.sent) {
        FERRET_FUNC(send_response)(&resp_state, "", 0);
    }

    ferret_close_socket(client_fd);
}

static void FERRET_FUNC(route_add)(FERRET_TYPE(app_t)* app, const char* method, const char* pattern, void* handler) {
    if (!app || !method || !pattern || !handler) {
        return;
    }
    if (app->route_count == app->route_cap) {
        size_t next_cap = app->route_cap == 0 ? 8 : app->route_cap * 2;
        FERRET_TYPE(route_t)* next = (FERRET_TYPE(route_t)*)ferret_realloc(app->routes, next_cap * sizeof(FERRET_TYPE(route_t)));
        if (!next) {
            return;
        }
        app->routes = next;
        app->route_cap = next_cap;
    }
    app->routes[app->route_count].method = FERRET_FUNC(strdup)(method);
    app->routes[app->route_count].pattern = FERRET_FUNC(strdup)(pattern);
    app->routes[app->route_count].handler = handler;
    app->route_count++;
}

static void FERRET_FUNC(middleware_add)(FERRET_TYPE(app_t)* app, void* handler) {
    if (!app || !handler) {
        return;
    }
    if (app->middleware_count == app->middleware_cap) {
        size_t next_cap = app->middleware_cap == 0 ? 4 : app->middleware_cap * 2;
        FERRET_TYPE(middleware_t)* next = (FERRET_TYPE(middleware_t)*)ferret_realloc(app->middleware, next_cap * sizeof(FERRET_TYPE(middleware_t)));
        if (!next) {
            return;
        }
        app->middleware = next;
        app->middleware_cap = next_cap;
    }
    app->middleware[app->middleware_count].handler = handler;
    app->middleware_count++;
}

static void FERRET_FUNC(static_add)(FERRET_TYPE(app_t)* app, const char* prefix, const char* dir) {
    if (!app || !prefix || !dir) {
        return;
    }
    if (app->static_count == app->static_cap) {
        size_t next_cap = app->static_cap == 0 ? 4 : app->static_cap * 2;
        FERRET_TYPE(static_t)* next = (FERRET_TYPE(static_t)*)ferret_realloc(app->statics, next_cap * sizeof(FERRET_TYPE(static_t)));
        if (!next) {
            return;
        }
        app->statics = next;
        app->static_cap = next_cap;
    }
    app->statics[app->static_count].prefix = FERRET_FUNC(strdup)(prefix);
    app->statics[app->static_count].dir = FERRET_FUNC(strdup)(dir);
    app->static_count++;
}

// ============================================
// Externs for std/http (using FERRET_FUNC macro for consistent naming)
// ============================================

void FERRET_FUNC(Server)(FERRET_TYPE(App)* out) {
    if (!out) {
        return;
    }
    FERRET_TYPE(app_t)* app = (FERRET_TYPE(app_t)*)ferret_alloc(sizeof(FERRET_TYPE(app_t)));
    if (!app) {
        out->handle = 0;
        return;
    }
    memset(app, 0, sizeof(*app));
    app->server_fd = FERRET_INVALID_SOCKET;
    out->handle = (int64_t)(intptr_t)app;
}

// App methods (note: method names are Type_Method without ferret_ prefix)
void FERRET_FUNC(App_Get)(APP* app, uint64_t app_heap, const char* path, void* handler) {
    (void)app_heap;
    if (!app) return;
    FERRET_TYPE(app_t)* a = (FERRET_TYPE(app_t)*)(intptr_t)app->handle;
    FERRET_FUNC(route_add)(a, "GET", path, handler);
}

void FERRET_FUNC(App_Post)(APP* app, uint64_t app_heap, const char* path, void* handler) {
    (void)app_heap;
    if (!app) return;
    FERRET_TYPE(app_t)* a = (FERRET_TYPE(app_t)*)(intptr_t)app->handle;
    FERRET_FUNC(route_add)(a, "POST", path, handler);
}

void FERRET_FUNC(App_Put)(APP* app, uint64_t app_heap, const char* path, void* handler) {
    (void)app_heap;
    if (!app) return;
    FERRET_TYPE(app_t)* a = (FERRET_TYPE(app_t)*)(intptr_t)app->handle;
    FERRET_FUNC(route_add)(a, "PUT", path, handler);
}

void FERRET_FUNC(App_Patch)(APP* app, uint64_t app_heap, const char* path, void* handler) {
    (void)app_heap;
    if (!app) return;
    FERRET_TYPE(app_t)* a = (FERRET_TYPE(app_t)*)(intptr_t)app->handle;
    FERRET_FUNC(route_add)(a, "PATCH", path, handler);
}

void FERRET_FUNC(App_Delete)(APP* app, uint64_t app_heap, const char* path, void* handler) {
    (void)app_heap;
    if (!app) return;
    FERRET_TYPE(app_t)* a = (FERRET_TYPE(app_t)*)(intptr_t)app->handle;
    FERRET_FUNC(route_add)(a, "DELETE", path, handler);
}

void FERRET_FUNC(App_Options)(APP* app, uint64_t app_heap, const char* path, void* handler) {
    (void)app_heap;
    if (!app) return;
    FERRET_TYPE(app_t)* a = (FERRET_TYPE(app_t)*)(intptr_t)app->handle;
    FERRET_FUNC(route_add)(a, "OPTIONS", path, handler);
}

void FERRET_FUNC(App_Head)(APP* app, uint64_t app_heap, const char* path, void* handler) {
    (void)app_heap;
    if (!app) return;
    FERRET_TYPE(app_t)* a = (FERRET_TYPE(app_t)*)(intptr_t)app->handle;
    FERRET_FUNC(route_add)(a, "HEAD", path, handler);
}

void FERRET_FUNC(App_Any)(APP* app, uint64_t app_heap, const char* path, void* handler) {
    (void)app_heap;
    if (!app) return;
    FERRET_TYPE(app_t)* a = (FERRET_TYPE(app_t)*)(intptr_t)app->handle;
    FERRET_FUNC(route_add)(a, "*", path, handler);
}

void FERRET_FUNC(App_Use)(APP* app, uint64_t app_heap, void* handler) {
    (void)app_heap;
    if (!app) return;
    FERRET_TYPE(app_t)* a = (FERRET_TYPE(app_t)*)(intptr_t)app->handle;
    FERRET_FUNC(middleware_add)(a, handler);
}

void FERRET_FUNC(App_Static)(APP* app, uint64_t app_heap, const char* prefix, const char* dir) {
    (void)app_heap;
    if (!app) return;
    FERRET_TYPE(app_t)* a = (FERRET_TYPE(app_t)*)(intptr_t)app->handle;
    FERRET_FUNC(static_add)(a, prefix, dir);
}

void FERRET_FUNC(App_Close)(APP* app, uint64_t app_heap) {
    (void)app_heap;
    if (!app) return;
    FERRET_TYPE(app_t)* a = (FERRET_TYPE(app_t)*)(intptr_t)app->handle;
    if (!a) return;
    if (a->server_fd != FERRET_INVALID_SOCKET) {
        ferret_close_socket(a->server_fd);
        a->server_fd = FERRET_INVALID_SOCKET;
    }
    a->running = false;
}

void FERRET_FUNC(App_Listen)(void* out, APP* app, uint64_t app_heap, int32_t port) {
    (void)app_heap;
    if (!app) {
        FERRET_RESULT_ERR(out, 8, "invalid app");
        return;
    }
    FERRET_TYPE(app_t)* a = (FERRET_TYPE(app_t)*)(intptr_t)app->handle;
    if (!a) {
        FERRET_RESULT_ERR(out, 8, "invalid app handle");
        return;
    }
    if (port <= 0 || port > 65535) {
        FERRET_RESULT_ERR(out, 8, "invalid port");
        return;
    }

#ifdef _WIN32
    static bool wsa_initialized = false;
    if (!wsa_initialized) {
        WSADATA wsa;
        if (WSAStartup(MAKEWORD(2, 2), &wsa) != 0) {
            FERRET_RESULT_ERR(out, 8, "WSAStartup failed");
            return;
        }
        wsa_initialized = true;
    }
#endif

    ferret_socket_t server_fd = socket(AF_INET, SOCK_STREAM, 0);
    if (server_fd == FERRET_INVALID_SOCKET) {
        FERRET_RESULT_ERR(out, 8, "socket failed");
        return;
    }
    int opt = 1;
    setsockopt(server_fd, SOL_SOCKET, SO_REUSEADDR, (const char*)&opt, sizeof(opt));

    struct sockaddr_in addr;
    memset(&addr, 0, sizeof(addr));
    addr.sin_family = AF_INET;
    addr.sin_addr.s_addr = htonl(INADDR_ANY);
    addr.sin_port = htons((uint16_t)port);
    if (bind(server_fd, (struct sockaddr*)&addr, sizeof(addr)) != 0) {
        ferret_close_socket(server_fd);
        FERRET_RESULT_ERR(out, 8, "bind failed");
        return;
    }
    if (listen(server_fd, 16) != 0) {
        ferret_close_socket(server_fd);
        FERRET_RESULT_ERR(out, 8, "listen failed");
        return;
    }

    a->server_fd = server_fd;
    a->running = true;

    FERRET_RESULT_OK(out, 8, bool, true);

    while (a->running) {
        ferret_socket_t client = accept(server_fd, NULL, NULL);
        if (client == FERRET_INVALID_SOCKET) {
            break;
        }
        FERRET_FUNC(handle_connection)(a, client);
    }
}

void FERRET_FUNC(App_Serve)(void* out, APP* app, uint64_t app_heap, int64_t listener_handle, const char* listener_addr) {
    (void)app_heap;
    (void)listener_addr;
    if (!app) {
        FERRET_RESULT_ERR(out, 8, "invalid app");
        return;
    }
    if (listener_handle == 0) {
        FERRET_RESULT_ERR(out, 8, "invalid listener");
        return;
    }

#ifdef _WIN32
    static bool wsa_initialized = false;
    if (!wsa_initialized) {
        WSADATA wsa;
        if (WSAStartup(MAKEWORD(2, 2), &wsa) != 0) {
            FERRET_RESULT_ERR(out, 8, "WSAStartup failed");
            return;
        }
        wsa_initialized = true;
    }
#endif

    FERRET_TYPE(app_t)* a = (FERRET_TYPE(app_t)*)(intptr_t)app->handle;
    if (!a) {
        FERRET_RESULT_ERR(out, 8, "invalid app handle");
        return;
    }

    ferret_socket_t server_fd = (ferret_socket_t)(intptr_t)listener_handle;
    a->server_fd = server_fd;
    a->running = true;

    FERRET_RESULT_OK(out, 8, bool, true);

    while (a->running) {
        ferret_socket_t client = accept(server_fd, NULL, NULL);
        if (client == FERRET_INVALID_SOCKET) {
            break;
        }
        FERRET_FUNC(handle_connection)(a, client);
    }
}

void FERRET_FUNC(App_ListenAddrNative)(void* out, APP* app, uint64_t app_heap, const char* addr) {
    (void)app_heap;
    if (!addr) {
        FERRET_RESULT_ERR(out, 8, "addr is null");
        return;
    }
    const char* colon = strrchr(addr, ':');
    if (!colon) {
        FERRET_RESULT_ERR(out, 8, "invalid addr");
        return;
    }
    char* host = FERRET_FUNC(strndup)(addr, (size_t)(colon - addr));
    errno = 0;
    char* end = NULL;
    long port = strtol(colon + 1, &end, 10);
    if (errno != 0 || end == (colon + 1) || port <= 0 || port > 65535) {
        if (host) ferret_free(host);
        FERRET_RESULT_ERR(out, 8, "invalid port");
        return;
    }

#ifdef _WIN32
    static bool wsa_initialized = false;
    if (!wsa_initialized) {
        WSADATA wsa;
        if (WSAStartup(MAKEWORD(2, 2), &wsa) != 0) {
            FERRET_RESULT_ERR(out, 8, "WSAStartup failed");
            return;
        }
        wsa_initialized = true;
    }
#endif

    ferret_socket_t server_fd = socket(AF_INET, SOCK_STREAM, 0);
    if (server_fd == FERRET_INVALID_SOCKET) {
        FERRET_RESULT_ERR(out, 8, "socket failed");
        return;
    }
    int opt = 1;
    setsockopt(server_fd, SOL_SOCKET, SO_REUSEADDR, (const char*)&opt, sizeof(opt));

    struct sockaddr_in addr_in;
    memset(&addr_in, 0, sizeof(addr_in));
    addr_in.sin_family = AF_INET;
    if (host && strlen(host) > 0) {
        if (inet_pton(AF_INET, host, &addr_in.sin_addr) != 1) {
            if (host) ferret_free(host);
            ferret_close_socket(server_fd);
            FERRET_RESULT_ERR(out, 8, "invalid host");
            return;
        }
    } else {
        addr_in.sin_addr.s_addr = htonl(INADDR_ANY);
    }
    if (host) ferret_free(host);
    addr_in.sin_port = htons((uint16_t)port);
    if (bind(server_fd, (struct sockaddr*)&addr_in, sizeof(addr_in)) != 0) {
        ferret_close_socket(server_fd);
        FERRET_RESULT_ERR(out, 8, "bind failed");
        return;
    }
    if (listen(server_fd, 16) != 0) {
        ferret_close_socket(server_fd);
        FERRET_RESULT_ERR(out, 8, "listen failed");
        return;
    }

    FERRET_TYPE(app_t)* a = (FERRET_TYPE(app_t)*)(intptr_t)app->handle;
    if (!a) {
        ferret_close_socket(server_fd);
        FERRET_RESULT_ERR(out, 8, "invalid app handle");
        return;
    }
    a->server_fd = server_fd;
    a->running = true;

    FERRET_RESULT_OK(out, 8, bool, true);

    while (a->running) {
        ferret_socket_t client = accept(server_fd, NULL, NULL);
        if (client == FERRET_INVALID_SOCKET) {
            break;
        }
        FERRET_FUNC(handle_connection)(a, client);
    }
}

// Request helpers (optional return)
void FERRET_FUNC(Request_Header)(void* out, REQUEST* req, uint64_t req_heap, const char* name) {
    (void)req_heap;
    if (!req || !name) {
        FERRET_FUNC(write_optional_str_ptr)(out, NULL);
        return;
    }
    void* opt = ferret_optional_alloc_none(sizeof(char*), sizeof(void*));
    if (!opt) {
        *(void**)out = NULL;
        return;
    }
    char* key = FERRET_FUNC(strdup)(name);
    FERRET_FUNC(lowercase)(key);
    char* key_ptr = key;
    ferret_map_get_optional_out(req->Headers, &key_ptr, opt);
    *(void**)out = opt;
}

void FERRET_FUNC(Request_Query)(void* out, REQUEST* req, uint64_t req_heap, const char* name) {
    (void)req_heap;
    if (!req || !name) {
        FERRET_FUNC(write_optional_str_ptr)(out, NULL);
        return;
    }
    void* opt = ferret_optional_alloc_none(sizeof(char*), sizeof(void*));
    if (!opt) {
        *(void**)out = NULL;
        return;
    }
    char* key = (char*)name;
    ferret_map_get_optional_out(req->Query, &key, opt);
    *(void**)out = opt;
}

void FERRET_FUNC(Request_Param)(void* out, REQUEST* req, uint64_t req_heap, const char* name) {
    (void)req_heap;
    if (!req || !name) {
        FERRET_FUNC(write_optional_str_ptr)(out, NULL);
        return;
    }
    void* opt = ferret_optional_alloc_none(sizeof(char*), sizeof(void*));
    if (!opt) {
        *(void**)out = NULL;
        return;
    }
    char* key = (char*)name;
    ferret_map_get_optional_out(req->Params, &key, opt);
    *(void**)out = opt;
}

// Response helpers
RESPONSE* FERRET_FUNC(Response_Status)(RESPONSE** out, uint64_t* out_heap, RESPONSE* res, uint64_t res_heap, int32_t code) {
    if (out) {
        *out = res;
    }
    if (out_heap) {
        *out_heap = res_heap;
    }
    if (!res) {
        return res;
    }
    RESPONSE_T* r = (RESPONSE_T*)(intptr_t)res->handle;
    if (r) {
        r->status = code;
    }
    return res;
}

int32_t FERRET_FUNC(Response_StatusCode)(RESPONSE* res, uint64_t res_heap) {
    (void)res_heap;
    if (!res) {
        return 0;
    }
    RESPONSE_T* r = (RESPONSE_T*)(intptr_t)res->handle;
    if (!r) {
        return 0;
    }
    return (int32_t)r->status;
}

RESPONSE* FERRET_FUNC(Response_Header)(RESPONSE** out, uint64_t* out_heap, RESPONSE* res, uint64_t res_heap, const char* name, const char* value) {
    if (out) {
        *out = res;
    }
    if (out_heap) {
        *out_heap = res_heap;
    }
    if (!res || !name) {
        return res;
    }
    RESPONSE_T* r = (RESPONSE_T*)(intptr_t)res->handle;
    if (r) {
        FERRET_FUNC(headers_add)(r, name, value ? value : "");
    }
    return res;
}

void FERRET_FUNC(Response_Send)(RESPONSE* res, uint64_t res_heap, const char* body) {
    (void)res_heap;
    if (!res) {
        return;
    }
    RESPONSE_T* r = (RESPONSE_T*)(intptr_t)res->handle;
    if (!r) {
        return;
    }
    size_t len = body ? strlen(body) : 0;
    FERRET_FUNC(send_response)(r, body ? body : "", len);
}

void FERRET_FUNC(Response_Json)(RESPONSE* res, uint64_t res_heap, const char* body) {
    (void)res_heap;
    if (!res) {
        return;
    }
    RESPONSE_T* r = (RESPONSE_T*)(intptr_t)res->handle;
    if (!r) {
        return;
    }
    FERRET_FUNC(headers_add)(r, "Content-Type", "application/json");
    size_t len = body ? strlen(body) : 0;
    FERRET_FUNC(send_response)(r, body ? body : "", len);
}

void FERRET_FUNC(Response_Redirect)(RESPONSE* res, uint64_t res_heap, const char* url) {
    (void)res_heap;
    if (!res) {
        return;
    }
    RESPONSE_T* r = (RESPONSE_T*)(intptr_t)res->handle;
    if (!r) {
        return;
    }
    r->status = 302;
    FERRET_FUNC(headers_add)(r, "Location", url ? url : "");
    FERRET_FUNC(send_response)(r, "", 0);
}

void FERRET_FUNC(Response_End)(RESPONSE* res, uint64_t res_heap) {
    (void)res_heap;
    if (!res) {
        return;
    }
    RESPONSE_T* r = (RESPONSE_T*)(intptr_t)res->handle;
    if (!r) {
        return;
    }
    FERRET_FUNC(send_response)(r, "", 0);
}

void FERRET_FUNC(Response_SendFile)(void* out, RESPONSE* res, uint64_t res_heap, const char* path) {
    (void)res_heap;
    if (!out) {
        return;
    }
    if (!res || !path) {
        FERRET_RESULT_ERR(out, 8, "invalid arguments");
        return;
    }
    FILE* f = fopen(path, "rb");
    if (!f) {
        FERRET_RESULT_ERR(out, 8, "failed to open file");
        return;
    }
    fseek(f, 0, SEEK_END);
    long size = ftell(f);
    fseek(f, 0, SEEK_SET);
    if (size < 0) {
        fclose(f);
        FERRET_RESULT_ERR(out, 8, "invalid file size");
        return;
    }
    char* buf = (char*)ferret_alloc((size_t)size);
    if (!buf) {
        fclose(f);
        FERRET_RESULT_ERR(out, 8, "alloc failed");
        return;
    }
    fread(buf, 1, (size_t)size, f);
    fclose(f);

    RESPONSE_T* r = (RESPONSE_T*)(intptr_t)res->handle;
    if (!r) {
        FERRET_RESULT_ERR(out, 8, "invalid response");
        return;
    }
    FERRET_FUNC(headers_add)(r, "Content-Type", "application/octet-stream");
    FERRET_FUNC(send_response)(r, buf, (size_t)size);
    FERRET_RESULT_OK(out, 8, bool, true);
}
