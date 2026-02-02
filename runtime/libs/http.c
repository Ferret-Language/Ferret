// Ferret runtime: HTTP server implementation for std/http
// Minimal Express-like server with routing and middleware.

#include <stdio.h>
#include <stdlib.h>
#include <stdint.h>
#include <stdbool.h>
#include <string.h>
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

typedef struct {
    int64_t handle;
} ferret_std_http_App;

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
} ferret_std_http_Request;

typedef struct {
    int64_t handle;
} ferret_std_http_Response;

typedef struct {
    char* name;
    char* value;
} ferret_http_header_t;

typedef struct {
    ferret_socket_t client_fd;
    int status;
    ferret_http_header_t* headers;
    size_t header_count;
    size_t header_cap;
    bool sent;
} ferret_http_response_t;

typedef struct {
    char* method;
    char* pattern;
    void* handler; // Ferret closure pointer
} ferret_http_route_t;

typedef struct {
    void* handler; // Ferret closure pointer
} ferret_http_middleware_t;

typedef struct {
    char* prefix;
    char* dir;
} ferret_http_static_t;

typedef struct {
    ferret_socket_t server_fd;
    bool running;
    ferret_http_route_t* routes;
    size_t route_count;
    size_t route_cap;
    ferret_http_middleware_t* middleware;
    size_t middleware_count;
    size_t middleware_cap;
    ferret_http_static_t* statics;
    size_t static_count;
    size_t static_cap;
} ferret_http_app_t;

typedef void (*ferret_http_handler_fn)(void* env, ferret_std_http_Request* req, uint64_t req_heap, ferret_std_http_Response* res, uint64_t res_heap);
typedef void (*ferret_http_middleware_fn)(void* env, ferret_std_http_Request* req, uint64_t req_heap, ferret_std_http_Response* res, uint64_t res_heap, void* next);
typedef void (*ferret_http_next_fn)(void* env);

typedef struct {
    void* __fn;
    void* ctx;
} ferret_http_next_closure_t;

typedef struct {
    ferret_http_app_t* app;
    ferret_std_http_Request* req;
    ferret_std_http_Response* res;
    size_t index;
    ferret_http_next_closure_t* next_closure;
} ferret_http_next_ctx_t;

static char* ferret_http_strdup(const char* s) {
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

static void ferret_http_lowercase(char* s) {
    if (!s) {
        return;
    }
    for (; *s; s++) {
        *s = (char)tolower((unsigned char)*s);
    }
}

static char* ferret_http_strndup(const char* s, size_t n) {
    char* out = (char*)ferret_alloc(n + 1);
    if (!out) {
        return NULL;
    }
    memcpy(out, s, n);
    out[n] = '\0';
    return out;
}

static char* ferret_http_url_decode(const char* src) {
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

static ferret_map_t* ferret_http_new_str_map(void) {
    return ferret_map_new_str(sizeof(char*), sizeof(char*), (ferret_type_info_t*)&ferret_type_str, (ferret_type_info_t*)&ferret_type_str);
}

static void ferret_http_map_set(ferret_map_t* map, const char* key, const char* value) {
    if (!map || !key) {
        return;
    }
    char* key_copy = ferret_http_strdup(key);
    char* val_copy = value ? ferret_http_strdup(value) : NULL;
    ferret_map_set(map, &key_copy, &val_copy);
}

static void ferret_http_headers_add(ferret_http_response_t* res, const char* name, const char* value) {
    if (!res || !name) {
        return;
    }
    // Replace existing header (case-insensitive)
    for (size_t i = 0; i < res->header_count; i++) {
        if (strcasecmp(res->headers[i].name, name) == 0) {
            res->headers[i].value = ferret_http_strdup(value ? value : "");
            return;
        }
    }
    if (res->header_count == res->header_cap) {
        size_t next_cap = res->header_cap == 0 ? 8 : res->header_cap * 2;
        ferret_http_header_t* next = (ferret_http_header_t*)realloc(res->headers, next_cap * sizeof(ferret_http_header_t));
        if (!next) {
            return;
        }
        res->headers = next;
        res->header_cap = next_cap;
    }
    res->headers[res->header_count].name = ferret_http_strdup(name);
    res->headers[res->header_count].value = ferret_http_strdup(value ? value : "");
    res->header_count++;
}

static bool ferret_http_headers_has(ferret_http_response_t* res, const char* name) {
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

static const char* ferret_http_status_text(int status) {
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
        case 500: return "Internal Server Error";
        default: return "OK";
    }
}

static bool ferret_http_send_all(ferret_socket_t fd, const char* data, size_t len) {
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

static bool ferret_http_send_response(ferret_http_response_t* res, const char* body, size_t body_len) {
    if (!res || res->sent) {
        return false;
    }
    const char* status_text = ferret_http_status_text(res->status);
    char header_buf[512];
    int header_len = snprintf(header_buf, sizeof(header_buf),
        "HTTP/1.1 %d %s\r\n", res->status, status_text);
    if (header_len <= 0) {
        return false;
    }

    if (!ferret_http_headers_has(res, "Content-Length")) {
        char len_buf[64];
        snprintf(len_buf, sizeof(len_buf), "%zu", body_len);
        ferret_http_headers_add(res, "Content-Length", len_buf);
    }
    if (!ferret_http_headers_has(res, "Connection")) {
        ferret_http_headers_add(res, "Connection", "close");
    }

    if (!ferret_http_send_all(res->client_fd, header_buf, (size_t)header_len)) {
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
        if (!ferret_http_send_all(res->client_fd, line_buf, (size_t)line_len)) {
            return false;
        }
    }
    if (!ferret_http_send_all(res->client_fd, "\r\n", 2)) {
        return false;
    }
    if (body_len > 0 && body != NULL) {
        if (!ferret_http_send_all(res->client_fd, body, body_len)) {
            return false;
        }
    }
    res->sent = true;
    return true;
}

static void ferret_http_write_optional_str_ptr(void* out, const char* value) {
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

static void ferret_http_result_err_str(void* out, const char* err) {
    if (!out) {
        return;
    }
    size_t offset = sizeof(char*);
    char** err_ptr = (char**)out;
    *err_ptr = (char*)err;
    int8_t* tag = (int8_t*)((char*)out + offset);
    *tag = 0;
}

static void ferret_http_result_ok_bool(void* out, bool ok) {
    if (!out) {
        return;
    }
    size_t offset = sizeof(char*);
    bool* val_ptr = (bool*)out;
    *val_ptr = ok;
    int8_t* tag = (int8_t*)((char*)out + offset);
    *tag = 1;
}

static void ferret_http_call_handler(void* handler, ferret_std_http_Request* req, ferret_std_http_Response* res) {
    if (!handler) {
        return;
    }
    void* fn_ptr = *(void**)handler;
    if (!fn_ptr) {
        return;
    }
    ferret_http_handler_fn fn = (ferret_http_handler_fn)fn_ptr;
    fn(handler, req, 0, res, 0);
}

static void ferret_http_call_middleware(void* handler, ferret_std_http_Request* req, ferret_std_http_Response* res, void* next) {
    if (!handler) {
        return;
    }
    void* fn_ptr = *(void**)handler;
    if (!fn_ptr) {
        return;
    }
    ferret_http_middleware_fn fn = (ferret_http_middleware_fn)fn_ptr;
    fn(handler, req, 0, res, 0, next);
}

static void ferret_http_run_route(ferret_http_app_t* app, ferret_std_http_Request* req, ferret_std_http_Response* res);

static void ferret_http_next_thunk(void* env) {
    ferret_http_next_closure_t* clo = (ferret_http_next_closure_t*)env;
    if (!clo || !clo->ctx) {
        return;
    }
    ferret_http_next_ctx_t* ctx = (ferret_http_next_ctx_t*)clo->ctx;
    if (ctx->index < ctx->app->middleware_count) {
        void* handler = ctx->app->middleware[ctx->index].handler;
        ctx->index++;
        ferret_http_call_middleware(handler, ctx->req, ctx->res, ctx->next_closure);
        return;
    }
    ferret_http_run_route(ctx->app, ctx->req, ctx->res);
}

static bool ferret_http_match_route(const char* pattern, const char* path, ferret_map_t* params) {
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
                char* key = ferret_http_strndup(p + 1, p_len - 1);
                char* val_raw = ferret_http_strndup(s, s_len);
                char* val = ferret_http_url_decode(val_raw ? val_raw : "");
                ferret_http_map_set(params, key ? key : "", val ? val : "");
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

static void ferret_http_run_route(ferret_http_app_t* app, ferret_std_http_Request* req, ferret_std_http_Response* res) {
    if (!app || !req || !res) {
        return;
    }

    // Static files first
    for (size_t i = 0; i < app->static_count; i++) {
        ferret_http_static_t* st = &app->statics[i];
        if (!st->prefix || !st->dir) {
            continue;
        }
        size_t prefix_len = strlen(st->prefix);
        if (strncmp(req->Path, st->prefix, prefix_len) != 0) {
            continue;
        }
        const char* rel = req->Path + prefix_len;
        if (*rel == '/') rel++;
        if (strstr(rel, "..")) {
            ferret_http_response_t* resp = (ferret_http_response_t*)(intptr_t)res->handle;
            if (resp) {
                resp->status = 403;
                ferret_http_send_response(resp, "Forbidden", 9);
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
        char* buf = (char*)ferret_alloc((size_t)fsize);
        if (!buf) {
            fclose(f);
            continue;
        }
        fread(buf, 1, (size_t)fsize, f);
        fclose(f);
        ferret_http_response_t* resp = (ferret_http_response_t*)(intptr_t)res->handle;
        if (resp) {
            resp->status = 200;
            ferret_http_send_response(resp, buf, (size_t)fsize);
        }
        return;
    }

    // Route match
    for (size_t i = 0; i < app->route_count; i++) {
        ferret_http_route_t* route = &app->routes[i];
        if (!route->handler || !route->pattern || !route->method) {
            continue;
        }
        if (strcmp(route->method, "*") != 0 && strcasecmp(route->method, req->Method) != 0) {
            continue;
        }
        if (!ferret_http_match_route(route->pattern, req->Path, NULL)) {
            continue;
        }
        ferret_map_t* params = ferret_http_new_str_map();
        ferret_http_match_route(route->pattern, req->Path, params);
        req->Params = params;
        ferret_http_call_handler(route->handler, req, res);
        return;
    }

    ferret_http_response_t* resp = (ferret_http_response_t*)(intptr_t)res->handle;
    if (resp) {
        resp->status = 404;
        ferret_http_send_response(resp, "Not Found", 9);
    }
}

static bool ferret_http_parse_request(ferret_socket_t fd, ferret_std_http_Request* out_req) {
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
                return false;
            }
            char* next = (char*)realloc(buf, next_cap);
            if (!next) {
                return false;
            }
            buf = next;
            cap = next_cap;
        }
        int r = recv(fd, buf + len, (int)(cap - len), 0);
        if (r <= 0) {
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
            return false;
        }
    }

    buf[header_end - 2] = '\0';
    char* line = buf;
    char* next_line = strstr(line, "\r\n");
    if (!next_line) {
        return false;
    }
    *next_line = '\0';
    char* method = strtok(line, " ");
    char* url = strtok(NULL, " ");
    if (!method || !url) {
        return false;
    }
    out_req->Method = ferret_http_strdup(method);
    out_req->Url = ferret_http_strdup(url);

    // Path and query
    char* qmark = strchr(url, '?');
    if (qmark) {
        out_req->Path = ferret_http_strndup(url, (size_t)(qmark - url));
    } else {
        out_req->Path = ferret_http_strdup(url);
    }

    out_req->Headers = ferret_http_new_str_map();
    out_req->Query = ferret_http_new_str_map();
    out_req->Params = ferret_http_new_str_map();

    // Headers
    line = next_line + 2;
    int content_length = 0;
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
            char* key_copy = ferret_http_strdup(key);
            ferret_http_lowercase(key_copy);
            if (strcasecmp(key_copy, "content-length") == 0) {
                content_length = atoi(value);
            }
            ferret_http_map_set(out_req->Headers, key_copy, value);
        }
        line = next_line + 2;
    }

    // Query params
    if (qmark) {
        char* query = qmark + 1;
        char* qcopy = ferret_http_strdup(query);
        char* tok = strtok(qcopy, "&");
        while (tok) {
            char* eq = strchr(tok, '=');
            if (eq) {
                *eq = '\0';
                char* k = ferret_http_url_decode(tok);
                char* v = ferret_http_url_decode(eq + 1);
                ferret_http_map_set(out_req->Query, k ? k : "", v ? v : "");
            } else {
                char* k = ferret_http_url_decode(tok);
                ferret_http_map_set(out_req->Query, k ? k : "", "");
            }
            tok = strtok(NULL, "&");
        }
    }

    // Body
    size_t body_len = 0;
    char* body = NULL;
    if (content_length > 0) {
        body = (char*)ferret_alloc((size_t)content_length + 1);
        if (!body) {
            return false;
        }
        size_t already = len - header_end;
        size_t to_copy = already < (size_t)content_length ? already : (size_t)content_length;
        memcpy(body, buf + header_end, to_copy);
        size_t offset = to_copy;
        while (offset < (size_t)content_length) {
            int r = recv(fd, body + offset, (int)((size_t)content_length - offset), 0);
            if (r <= 0) {
                break;
            }
            offset += (size_t)r;
        }
        body_len = offset;
        body[body_len] = '\0';
    } else {
        body = ferret_http_strdup("");
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

    return true;
}

static void ferret_http_set_ip(ferret_socket_t fd, ferret_std_http_Request* req) {
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
    req->IP = ferret_http_strdup(ipbuf[0] ? ipbuf : "0.0.0.0");
}

static void ferret_http_handle_connection(ferret_http_app_t* app, ferret_socket_t client_fd) {
    ferret_std_http_Request req = {0};
    if (!ferret_http_parse_request(client_fd, &req)) {
        ferret_close_socket(client_fd);
        return;
    }
    ferret_http_set_ip(client_fd, &req);

    ferret_http_response_t resp_state = {0};
    resp_state.client_fd = client_fd;
    resp_state.status = 200;
    resp_state.sent = false;

    ferret_std_http_Response res = {0};
    res.handle = (int64_t)(intptr_t)&resp_state;

    if (app->middleware_count > 0) {
        ferret_http_next_ctx_t* ctx = (ferret_http_next_ctx_t*)ferret_alloc(sizeof(ferret_http_next_ctx_t));
        ferret_http_next_closure_t* next = (ferret_http_next_closure_t*)ferret_alloc(sizeof(ferret_http_next_closure_t));
        if (ctx && next) {
            ctx->app = app;
            ctx->req = &req;
            ctx->res = &res;
            ctx->index = 0;
            ctx->next_closure = next;
            next->__fn = (void*)ferret_http_next_thunk;
            next->ctx = ctx;
            ferret_http_next_thunk(next);
        } else {
            ferret_http_run_route(app, &req, &res);
        }
    } else {
        ferret_http_run_route(app, &req, &res);
    }

    if (!resp_state.sent) {
        ferret_http_send_response(&resp_state, "", 0);
    }

    ferret_close_socket(client_fd);
}

static void ferret_http_route_add(ferret_http_app_t* app, const char* method, const char* pattern, void* handler) {
    if (!app || !method || !pattern || !handler) {
        return;
    }
    if (app->route_count == app->route_cap) {
        size_t next_cap = app->route_cap == 0 ? 8 : app->route_cap * 2;
        ferret_http_route_t* next = (ferret_http_route_t*)realloc(app->routes, next_cap * sizeof(ferret_http_route_t));
        if (!next) {
            return;
        }
        app->routes = next;
        app->route_cap = next_cap;
    }
    app->routes[app->route_count].method = ferret_http_strdup(method);
    app->routes[app->route_count].pattern = ferret_http_strdup(pattern);
    app->routes[app->route_count].handler = handler;
    app->route_count++;
}

static void ferret_http_middleware_add(ferret_http_app_t* app, void* handler) {
    if (!app || !handler) {
        return;
    }
    if (app->middleware_count == app->middleware_cap) {
        size_t next_cap = app->middleware_cap == 0 ? 4 : app->middleware_cap * 2;
        ferret_http_middleware_t* next = (ferret_http_middleware_t*)realloc(app->middleware, next_cap * sizeof(ferret_http_middleware_t));
        if (!next) {
            return;
        }
        app->middleware = next;
        app->middleware_cap = next_cap;
    }
    app->middleware[app->middleware_count].handler = handler;
    app->middleware_count++;
}

static void ferret_http_static_add(ferret_http_app_t* app, const char* prefix, const char* dir) {
    if (!app || !prefix || !dir) {
        return;
    }
    if (app->static_count == app->static_cap) {
        size_t next_cap = app->static_cap == 0 ? 4 : app->static_cap * 2;
        ferret_http_static_t* next = (ferret_http_static_t*)realloc(app->statics, next_cap * sizeof(ferret_http_static_t));
        if (!next) {
            return;
        }
        app->statics = next;
        app->static_cap = next_cap;
    }
    app->statics[app->static_count].prefix = ferret_http_strdup(prefix);
    app->statics[app->static_count].dir = ferret_http_strdup(dir);
    app->static_count++;
}

// ============================================
// Externs for std/http
// ============================================

void ferret_std_http_Server(ferret_std_http_App* out) {
    if (!out) {
        return;
    }
    ferret_http_app_t* app = (ferret_http_app_t*)ferret_alloc(sizeof(ferret_http_app_t));
    if (!app) {
        out->handle = 0;
        return;
    }
    memset(app, 0, sizeof(*app));
    app->server_fd = FERRET_INVALID_SOCKET;
    out->handle = (int64_t)(intptr_t)app;
}

// App methods (note: method names are Type_Method without ferret_ prefix)
void std_http_App_Get(ferret_std_http_App* app, uint64_t app_heap, const char* path, void* handler) {
    (void)app_heap;
    if (!app) return;
    ferret_http_app_t* a = (ferret_http_app_t*)(intptr_t)app->handle;
    ferret_http_route_add(a, "GET", path, handler);
}

void std_http_App_Post(ferret_std_http_App* app, uint64_t app_heap, const char* path, void* handler) {
    (void)app_heap;
    if (!app) return;
    ferret_http_app_t* a = (ferret_http_app_t*)(intptr_t)app->handle;
    ferret_http_route_add(a, "POST", path, handler);
}

void std_http_App_Put(ferret_std_http_App* app, uint64_t app_heap, const char* path, void* handler) {
    (void)app_heap;
    if (!app) return;
    ferret_http_app_t* a = (ferret_http_app_t*)(intptr_t)app->handle;
    ferret_http_route_add(a, "PUT", path, handler);
}

void std_http_App_Patch(ferret_std_http_App* app, uint64_t app_heap, const char* path, void* handler) {
    (void)app_heap;
    if (!app) return;
    ferret_http_app_t* a = (ferret_http_app_t*)(intptr_t)app->handle;
    ferret_http_route_add(a, "PATCH", path, handler);
}

void std_http_App_Delete(ferret_std_http_App* app, uint64_t app_heap, const char* path, void* handler) {
    (void)app_heap;
    if (!app) return;
    ferret_http_app_t* a = (ferret_http_app_t*)(intptr_t)app->handle;
    ferret_http_route_add(a, "DELETE", path, handler);
}

void std_http_App_Options(ferret_std_http_App* app, uint64_t app_heap, const char* path, void* handler) {
    (void)app_heap;
    if (!app) return;
    ferret_http_app_t* a = (ferret_http_app_t*)(intptr_t)app->handle;
    ferret_http_route_add(a, "OPTIONS", path, handler);
}

void std_http_App_Head(ferret_std_http_App* app, uint64_t app_heap, const char* path, void* handler) {
    (void)app_heap;
    if (!app) return;
    ferret_http_app_t* a = (ferret_http_app_t*)(intptr_t)app->handle;
    ferret_http_route_add(a, "HEAD", path, handler);
}

void std_http_App_Any(ferret_std_http_App* app, uint64_t app_heap, const char* path, void* handler) {
    (void)app_heap;
    if (!app) return;
    ferret_http_app_t* a = (ferret_http_app_t*)(intptr_t)app->handle;
    ferret_http_route_add(a, "*", path, handler);
}

void std_http_App_Use(ferret_std_http_App* app, uint64_t app_heap, void* handler) {
    (void)app_heap;
    if (!app) return;
    ferret_http_app_t* a = (ferret_http_app_t*)(intptr_t)app->handle;
    ferret_http_middleware_add(a, handler);
}

void std_http_App_Static(ferret_std_http_App* app, uint64_t app_heap, const char* prefix, const char* dir) {
    (void)app_heap;
    if (!app) return;
    ferret_http_app_t* a = (ferret_http_app_t*)(intptr_t)app->handle;
    ferret_http_static_add(a, prefix, dir);
}

void std_http_App_Close(ferret_std_http_App* app, uint64_t app_heap) {
    (void)app_heap;
    if (!app) return;
    ferret_http_app_t* a = (ferret_http_app_t*)(intptr_t)app->handle;
    if (!a) return;
    if (a->server_fd != FERRET_INVALID_SOCKET) {
        ferret_close_socket(a->server_fd);
        a->server_fd = FERRET_INVALID_SOCKET;
    }
    a->running = false;
}

void std_http_App_Listen(void* out, ferret_std_http_App* app, uint64_t app_heap, int32_t port) {
    (void)app_heap;
    if (!app) {
        ferret_http_result_err_str(out, "invalid app");
        return;
    }
    ferret_http_app_t* a = (ferret_http_app_t*)(intptr_t)app->handle;
    if (!a) {
        ferret_http_result_err_str(out, "invalid app handle");
        return;
    }

#ifdef _WIN32
    static bool wsa_initialized = false;
    if (!wsa_initialized) {
        WSADATA wsa;
        if (WSAStartup(MAKEWORD(2, 2), &wsa) != 0) {
            ferret_http_result_err_str(out, "WSAStartup failed");
            return;
        }
        wsa_initialized = true;
    }
#endif

    ferret_socket_t server_fd = socket(AF_INET, SOCK_STREAM, 0);
    if (server_fd == FERRET_INVALID_SOCKET) {
        ferret_http_result_err_str(out, "socket failed");
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
        ferret_http_result_err_str(out, "bind failed");
        return;
    }
    if (listen(server_fd, 16) != 0) {
        ferret_close_socket(server_fd);
        ferret_http_result_err_str(out, "listen failed");
        return;
    }

    a->server_fd = server_fd;
    a->running = true;

    ferret_http_result_ok_bool(out, true);

    while (a->running) {
        ferret_socket_t client = accept(server_fd, NULL, NULL);
        if (client == FERRET_INVALID_SOCKET) {
            break;
        }
        ferret_http_handle_connection(a, client);
    }
}

void std_http_App_ListenAddr(void* out, ferret_std_http_App* app, uint64_t app_heap, const char* addr) {
    (void)app_heap;
    if (!addr) {
        ferret_http_result_err_str(out, "addr is null");
        return;
    }
    const char* colon = strrchr(addr, ':');
    if (!colon) {
        ferret_http_result_err_str(out, "invalid addr");
        return;
    }
    char* host = ferret_http_strndup(addr, (size_t)(colon - addr));
    int port = atoi(colon + 1);
    if (port <= 0) {
        ferret_http_result_err_str(out, "invalid port");
        return;
    }

#ifdef _WIN32
    static bool wsa_initialized = false;
    if (!wsa_initialized) {
        WSADATA wsa;
        if (WSAStartup(MAKEWORD(2, 2), &wsa) != 0) {
            ferret_http_result_err_str(out, "WSAStartup failed");
            return;
        }
        wsa_initialized = true;
    }
#endif

    ferret_socket_t server_fd = socket(AF_INET, SOCK_STREAM, 0);
    if (server_fd == FERRET_INVALID_SOCKET) {
        ferret_http_result_err_str(out, "socket failed");
        return;
    }
    int opt = 1;
    setsockopt(server_fd, SOL_SOCKET, SO_REUSEADDR, (const char*)&opt, sizeof(opt));

    struct sockaddr_in addr_in;
    memset(&addr_in, 0, sizeof(addr_in));
    addr_in.sin_family = AF_INET;
    if (host && strlen(host) > 0) {
        if (inet_pton(AF_INET, host, &addr_in.sin_addr) != 1) {
            ferret_close_socket(server_fd);
            ferret_http_result_err_str(out, "invalid host");
            return;
        }
    } else {
        addr_in.sin_addr.s_addr = htonl(INADDR_ANY);
    }
    addr_in.sin_port = htons((uint16_t)port);
    if (bind(server_fd, (struct sockaddr*)&addr_in, sizeof(addr_in)) != 0) {
        ferret_close_socket(server_fd);
        ferret_http_result_err_str(out, "bind failed");
        return;
    }
    if (listen(server_fd, 16) != 0) {
        ferret_close_socket(server_fd);
        ferret_http_result_err_str(out, "listen failed");
        return;
    }

    ferret_http_app_t* a = (ferret_http_app_t*)(intptr_t)app->handle;
    if (!a) {
        ferret_close_socket(server_fd);
        ferret_http_result_err_str(out, "invalid app handle");
        return;
    }
    a->server_fd = server_fd;
    a->running = true;

    ferret_http_result_ok_bool(out, true);

    while (a->running) {
        ferret_socket_t client = accept(server_fd, NULL, NULL);
        if (client == FERRET_INVALID_SOCKET) {
            break;
        }
        ferret_http_handle_connection(a, client);
    }
}

// Request helpers (optional return)
void std_http_Request_Header(void* out, ferret_std_http_Request* req, uint64_t req_heap, const char* name) {
    (void)req_heap;
    if (!req || !name) {
        ferret_http_write_optional_str_ptr(out, NULL);
        return;
    }
    void* opt = ferret_optional_alloc_none(sizeof(char*), sizeof(void*));
    if (!opt) {
        *(void**)out = NULL;
        return;
    }
    char* key = ferret_http_strdup(name);
    ferret_http_lowercase(key);
    char* key_ptr = key;
    ferret_map_get_optional_out(req->Headers, &key_ptr, opt);
    *(void**)out = opt;
}

void std_http_Request_Query(void* out, ferret_std_http_Request* req, uint64_t req_heap, const char* name) {
    (void)req_heap;
    if (!req || !name) {
        ferret_http_write_optional_str_ptr(out, NULL);
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

void std_http_Request_Param(void* out, ferret_std_http_Request* req, uint64_t req_heap, const char* name) {
    (void)req_heap;
    if (!req || !name) {
        ferret_http_write_optional_str_ptr(out, NULL);
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
ferret_std_http_Response* std_http_Response_Status(ferret_std_http_Response** out, uint64_t* out_heap, ferret_std_http_Response* res, uint64_t res_heap, int32_t code) {
    if (out) {
        *out = res;
    }
    if (out_heap) {
        *out_heap = res_heap;
    }
    if (!res) {
        return res;
    }
    ferret_http_response_t* r = (ferret_http_response_t*)(intptr_t)res->handle;
    if (r) {
        r->status = code;
    }
    return res;
}

ferret_std_http_Response* std_http_Response_Header(ferret_std_http_Response** out, uint64_t* out_heap, ferret_std_http_Response* res, uint64_t res_heap, const char* name, const char* value) {
    if (out) {
        *out = res;
    }
    if (out_heap) {
        *out_heap = res_heap;
    }
    if (!res || !name) {
        return res;
    }
    ferret_http_response_t* r = (ferret_http_response_t*)(intptr_t)res->handle;
    if (r) {
        ferret_http_headers_add(r, name, value ? value : "");
    }
    return res;
}

void std_http_Response_Send(ferret_std_http_Response* res, uint64_t res_heap, const char* body) {
    (void)res_heap;
    if (!res) {
        return;
    }
    ferret_http_response_t* r = (ferret_http_response_t*)(intptr_t)res->handle;
    if (!r) {
        return;
    }
    size_t len = body ? strlen(body) : 0;
    ferret_http_send_response(r, body ? body : "", len);
}

void std_http_Response_Json(ferret_std_http_Response* res, uint64_t res_heap, const char* body) {
    (void)res_heap;
    if (!res) {
        return;
    }
    ferret_http_response_t* r = (ferret_http_response_t*)(intptr_t)res->handle;
    if (!r) {
        return;
    }
    ferret_http_headers_add(r, "Content-Type", "application/json");
    size_t len = body ? strlen(body) : 0;
    ferret_http_send_response(r, body ? body : "", len);
}

void std_http_Response_Redirect(ferret_std_http_Response* res, uint64_t res_heap, const char* url) {
    (void)res_heap;
    if (!res) {
        return;
    }
    ferret_http_response_t* r = (ferret_http_response_t*)(intptr_t)res->handle;
    if (!r) {
        return;
    }
    r->status = 302;
    ferret_http_headers_add(r, "Location", url ? url : "");
    ferret_http_send_response(r, "", 0);
}

void std_http_Response_End(ferret_std_http_Response* res, uint64_t res_heap) {
    (void)res_heap;
    if (!res) {
        return;
    }
    ferret_http_response_t* r = (ferret_http_response_t*)(intptr_t)res->handle;
    if (!r) {
        return;
    }
    ferret_http_send_response(r, "", 0);
}

void std_http_Response_SendFile(void* out, ferret_std_http_Response* res, uint64_t res_heap, const char* path) {
    (void)res_heap;
    if (!out) {
        return;
    }
    if (!res || !path) {
        ferret_http_result_err_str(out, "invalid arguments");
        return;
    }
    FILE* f = fopen(path, "rb");
    if (!f) {
        ferret_http_result_err_str(out, "failed to open file");
        return;
    }
    fseek(f, 0, SEEK_END);
    long size = ftell(f);
    fseek(f, 0, SEEK_SET);
    if (size < 0) {
        fclose(f);
        ferret_http_result_err_str(out, "invalid file size");
        return;
    }
    char* buf = (char*)ferret_alloc((size_t)size);
    if (!buf) {
        fclose(f);
        ferret_http_result_err_str(out, "alloc failed");
        return;
    }
    fread(buf, 1, (size_t)size, f);
    fclose(f);

    ferret_http_response_t* r = (ferret_http_response_t*)(intptr_t)res->handle;
    if (!r) {
        ferret_http_result_err_str(out, "invalid response");
        return;
    }
    ferret_http_headers_add(r, "Content-Type", "application/octet-stream");
    ferret_http_send_response(r, buf, (size_t)size);
    ferret_http_result_ok_bool(out, true);
}
