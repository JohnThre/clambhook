// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

#include <curl/curl.h>
#include <errno.h>
#include <signal.h>
#include <stdbool.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/ioctl.h>
#include <sys/select.h>
#include <termios.h>
#include <time.h>
#include <unistd.h>

#include "clambhook/error.h"
#include "clambhook/json.h"
#include "internal.h"

#ifndef CLAMBHOOK_VERSION
#define CLAMBHOOK_VERSION "dev"
#endif

#define TUI_BODY_LIMIT (4U * 1024U * 1024U)
#define TUI_REFRESH_SECONDS 2
#define TUI_RECENT_CONNECTIONS 12U

enum tui_key {
    TUI_KEY_NONE = 0,
    TUI_KEY_UP = 256,
    TUI_KEY_DOWN
};

typedef struct tui_buffer {
    char *data;
    size_t length;
    size_t capacity;
    bool failed;
} tui_buffer;

typedef struct tui_snapshot {
    ch_json_value *status;
    ch_json_value *profiles;
    ch_json_value *traffic;
    ch_json_value *prompts;
    char error[256];
    char notice[256];
    bool online;
    size_t profile_cursor;
    size_t prompt_cursor;
} tui_snapshot;

static volatile sig_atomic_t tui_stopping;
static struct termios tui_saved_terminal;
static bool tui_terminal_saved;

static void tui_stop(int signal_number) {
    (void)signal_number;
    tui_stopping = 1;
}

static void tui_restore_terminal(void) {
    if (tui_terminal_saved) {
        (void)tcsetattr(STDIN_FILENO, TCSAFLUSH, &tui_saved_terminal);
        tui_terminal_saved = false;
    }
    if (isatty(STDOUT_FILENO)) {
        (void)fputs("\033[?25h\033[?1049l", stdout);
        (void)fflush(stdout);
    }
}

static bool tui_enter_terminal(void) {
    if (!isatty(STDIN_FILENO) || !isatty(STDOUT_FILENO)) return false;
    if (tcgetattr(STDIN_FILENO, &tui_saved_terminal) != 0) return false;
    struct termios raw = tui_saved_terminal;
    raw.c_lflag &= (tcflag_t)~(ICANON | ECHO);
    raw.c_iflag &= (tcflag_t)~(IXON | ICRNL);
    raw.c_cc[VMIN] = 0;
    raw.c_cc[VTIME] = 0;
    if (tcsetattr(STDIN_FILENO, TCSAFLUSH, &raw) != 0) return false;
    tui_terminal_saved = true;
    (void)fputs("\033[?1049h\033[?25l", stdout);
    (void)fflush(stdout);
    return true;
}

static bool tui_buffer_reserve(tui_buffer *buffer, size_t extra) {
    if (buffer->failed || extra > TUI_BODY_LIMIT - buffer->length) {
        buffer->failed = true;
        return false;
    }
    size_t needed = buffer->length + extra + 1U;
    if (needed <= buffer->capacity) return true;
    size_t next = buffer->capacity == 0U ? 4096U : buffer->capacity;
    while (next < needed) {
        if (next > TUI_BODY_LIMIT / 2U) {
            next = TUI_BODY_LIMIT + 1U;
            break;
        }
        next *= 2U;
    }
    char *grown = realloc(buffer->data, next);
    if (grown == NULL) {
        buffer->failed = true;
        return false;
    }
    buffer->data = grown;
    buffer->capacity = next;
    return true;
}

static size_t tui_write_response(char *data, size_t size, size_t count,
                                 void *opaque) {
    tui_buffer *buffer = opaque;
    if (size != 0U && count > SIZE_MAX / size) {
        buffer->failed = true;
        return 0U;
    }
    size_t length = size * count;
    if (!tui_buffer_reserve(buffer, length)) return 0U;
    memcpy(buffer->data + buffer->length, data, length);
    buffer->length += length;
    buffer->data[buffer->length] = '\0';
    return length;
}

static void tui_set_error(char *destination, size_t capacity,
                          const char *message) {
    if (capacity == 0U) return;
    (void)snprintf(destination, capacity, "%s",
                   message == NULL ? "unknown error" : message);
}

static char *tui_url(const char *base, const char *path) {
    size_t base_length = strlen(base);
    size_t path_length = strlen(path);
    if (base_length > SIZE_MAX - path_length - 1U) return NULL;
    char *url = malloc(base_length + path_length + 1U);
    if (url == NULL) return NULL;
    memcpy(url, base, base_length);
    memcpy(url + base_length, path, path_length + 1U);
    return url;
}

static char *tui_request(const char *base, const char *token,
                         const char *method, const char *path,
                         const char *body, char *error, size_t error_capacity) {
    char *url = tui_url(base, path);
    if (url == NULL) {
        tui_set_error(error, error_capacity, "allocate request URL");
        return NULL;
    }
    CURL *curl = curl_easy_init();
    if (curl == NULL) {
        free(url);
        tui_set_error(error, error_capacity, "initialize HTTP client");
        return NULL;
    }
    tui_buffer response = {0};
    char curl_error[CURL_ERROR_SIZE] = {0};
    struct curl_slist *headers = NULL;
    if (token != NULL && token[0] != '\0') {
        size_t length = strlen(token);
        char *authorization = malloc(length + 23U);
        if (authorization == NULL) {
            curl_easy_cleanup(curl);
            free(url);
            tui_set_error(error, error_capacity, "allocate authorization header");
            return NULL;
        }
        (void)snprintf(authorization, length + 23U, "Authorization: Bearer %s", token);
        struct curl_slist *grown = curl_slist_append(headers, authorization);
        free(authorization);
        if (grown == NULL) {
            curl_easy_cleanup(curl);
            free(url);
            tui_set_error(error, error_capacity, "allocate authorization header");
            return NULL;
        }
        headers = grown;
    }
    if (body != NULL) {
        struct curl_slist *grown = curl_slist_append(
            headers, "Content-Type: application/json");
        if (grown == NULL) {
            curl_slist_free_all(headers);
            curl_easy_cleanup(curl);
            free(url);
            tui_set_error(error, error_capacity, "allocate content-type header");
            return NULL;
        }
        headers = grown;
    }
    (void)curl_easy_setopt(curl, CURLOPT_URL, url);
    (void)curl_easy_setopt(curl, CURLOPT_ERRORBUFFER, curl_error);
    (void)curl_easy_setopt(curl, CURLOPT_CUSTOMREQUEST, method);
    (void)curl_easy_setopt(curl, CURLOPT_HTTPHEADER, headers);
    (void)curl_easy_setopt(curl, CURLOPT_WRITEFUNCTION, tui_write_response);
    (void)curl_easy_setopt(curl, CURLOPT_WRITEDATA, &response);
    (void)curl_easy_setopt(curl, CURLOPT_CONNECTTIMEOUT_MS, 1000L);
    (void)curl_easy_setopt(curl, CURLOPT_TIMEOUT_MS,
                           strstr(path, "/outline/") == NULL ? 2000L : 17000L);
    (void)curl_easy_setopt(curl, CURLOPT_NOSIGNAL, 1L);
    /* The control plane is loopback-only; never inherit an ambient proxy. */
    (void)curl_easy_setopt(curl, CURLOPT_NOPROXY, "*");
    if (body != NULL) {
        (void)curl_easy_setopt(curl, CURLOPT_POSTFIELDS, body);
        (void)curl_easy_setopt(curl, CURLOPT_POSTFIELDSIZE_LARGE,
                               (curl_off_t)strlen(body));
    }
    CURLcode result = curl_easy_perform(curl);
    long response_code = 0L;
    (void)curl_easy_getinfo(curl, CURLINFO_RESPONSE_CODE, &response_code);
    if (result != CURLE_OK) {
        tui_set_error(error, error_capacity, response.failed ?
            "API response exceeded 4 MiB" :
            (curl_error[0] == '\0' ? curl_easy_strerror(result) : curl_error));
        free(response.data);
        response.data = NULL;
    } else if (response_code < 200L || response_code > 299L) {
        char message[256];
        (void)snprintf(message, sizeof(message), "HTTP %ld: %.200s",
                       response_code, response.data == NULL ? "" : response.data);
        tui_set_error(error, error_capacity, message);
        free(response.data);
        response.data = NULL;
    } else if (response.data == NULL) {
        response.data = strdup("");
        if (response.data == NULL) tui_set_error(error, error_capacity, "allocate response");
    }
    curl_slist_free_all(headers);
    curl_easy_cleanup(curl);
    free(url);
    return response.data;
}

static ch_json_value *tui_get_json(const char *base, const char *token,
                                   const char *path, char *error,
                                   size_t error_capacity) {
    char *body = tui_request(base, token, "GET", path, NULL, error,
                             error_capacity);
    if (body == NULL) return NULL;
    ch_error parse_error;
    ch_json_value *value = ch_json_parse(body, strlen(body), &parse_error);
    free(body);
    if (value == NULL) tui_set_error(error, error_capacity, parse_error.message);
    return value;
}

static const ch_json_value *tui_member(const ch_json_value *object,
                                       const char *name) {
    return ch_json_object_get(object, name);
}

static const char *tui_string(const ch_json_value *object, const char *name) {
    const char *value = ch_json_string_value(tui_member(object, name));
    return value == NULL ? "" : value;
}

static int64_t tui_integer(const ch_json_value *object, const char *name) {
    int64_t value = 0;
    (void)ch_json_int64_value(tui_member(object, name), &value);
    return value;
}

static void tui_replace_json(ch_json_value **destination,
                             ch_json_value *replacement) {
    ch_json_value_destroy(*destination);
    *destination = replacement;
}

static void tui_align_cursors(tui_snapshot *snapshot) {
    const ch_json_value *profiles = tui_member(snapshot->profiles, "profiles");
    size_t count = ch_json_array_size(profiles);
    if (count == 0U) snapshot->profile_cursor = 0U;
    else if (snapshot->profile_cursor >= count) snapshot->profile_cursor = count - 1U;
    const ch_json_value *prompts = tui_member(snapshot->prompts, "prompts");
    count = ch_json_array_size(prompts);
    if (count == 0U) snapshot->prompt_cursor = 0U;
    else if (snapshot->prompt_cursor >= count) snapshot->prompt_cursor = count - 1U;
}

static void tui_refresh(tui_snapshot *snapshot, const char *base,
                        const char *token) {
    char error[256] = {0};
    ch_json_value *status = tui_get_json(base, token, "/api/v1/status", error,
                                         sizeof(error));
    if (status == NULL) {
        snapshot->online = false;
        tui_set_error(snapshot->error, sizeof(snapshot->error), error);
        return;
    }
    ch_json_value *profiles = tui_get_json(base, token, "/api/v1/profiles",
                                           error, sizeof(error));
    ch_json_value *traffic = profiles == NULL ? NULL : tui_get_json(
        base, token, "/api/v1/traffic", error, sizeof(error));
    ch_json_value *prompts = traffic == NULL ? NULL : tui_get_json(
        base, token, "/api/v1/prompts/pending", error, sizeof(error));
    if (profiles == NULL || traffic == NULL || prompts == NULL) {
        ch_json_value_destroy(status);
        ch_json_value_destroy(profiles);
        ch_json_value_destroy(traffic);
        ch_json_value_destroy(prompts);
        snapshot->online = false;
        tui_set_error(snapshot->error, sizeof(snapshot->error), error);
        return;
    }
    tui_replace_json(&snapshot->status, status);
    tui_replace_json(&snapshot->profiles, profiles);
    tui_replace_json(&snapshot->traffic, traffic);
    tui_replace_json(&snapshot->prompts, prompts);
    snapshot->online = true;
    snapshot->error[0] = '\0';
    tui_align_cursors(snapshot);
}

static size_t tui_terminal_columns(void) {
    const char *configured = getenv("COLUMNS");
    if (configured != NULL && configured[0] != '\0') {
        char *end = NULL;
        unsigned long value = strtoul(configured, &end, 10);
        if (end != configured && *end == '\0' && value >= 40UL && value <= 1000UL) {
            return (size_t)value;
        }
    }
    struct winsize size = {0};
    if (ioctl(STDOUT_FILENO, TIOCGWINSZ, &size) == 0 && size.ws_col >= 40U) {
        return (size_t)size.ws_col;
    }
    return 120U;
}

static void tui_render_profiles(const tui_snapshot *snapshot, bool color,
                                bool compact) {
    const ch_json_value *profiles = tui_member(snapshot->profiles, "profiles");
    const char *active = tui_string(snapshot->profiles, "active");
    (void)printf("%sProfiles%s  ", color ? "\033[1;36m" : "",
                 color ? "\033[0m" : "");
    for (size_t index = 0U; index < ch_json_array_size(profiles); ++index) {
        const char *name = ch_json_string_value(ch_json_array_get(profiles, index));
        if (name == NULL) continue;
        const char *cursor = index == snapshot->profile_cursor ? ">" : " ";
        const char *selected = strcmp(name, active) == 0 ? "*" : "";
        (void)printf(compact ? "\n %s%s%s%s%s" : "%s%s%s%s%s ", cursor,
                     color && strcmp(name, active) == 0 ? "\033[1;32m" : "",
                     name, selected, color ? "\033[0m" : "");
    }
    (void)putchar('\n');
    if (ch_json_array_size(profiles) == 0U) {
        (void)puts("  No profiles configured. Press i to import Outline or v to convert a profile.");
    }
}

static void tui_format_bytes(int64_t bytes, char *destination, size_t capacity) {
    static const char *units[] = {"B", "KiB", "MiB", "GiB", "TiB"};
    double value = bytes < 0 ? 0.0 : (double)bytes;
    size_t unit = 0U;
    while (value >= 1024.0 && unit + 1U < sizeof(units) / sizeof(units[0])) {
        value /= 1024.0;
        unit++;
    }
    if (unit == 0U) {
        (void)snprintf(destination, capacity, "%.0f %s", value, units[unit]);
    } else {
        (void)snprintf(destination, capacity, "%.1f %s", value, units[unit]);
    }
}

static void tui_render_prompts(const tui_snapshot *snapshot, bool color) {
    const ch_json_value *prompts = tui_member(snapshot->prompts, "prompts");
    size_t count = ch_json_array_size(prompts);
    if (count == 0U) return;
    (void)printf("\n%sInteractive prompts (%zu)%s\n",
                 color ? "\033[1;33m" : "", count,
                 color ? "\033[0m" : "");
    for (size_t index = 0U; index < count; ++index) {
        const ch_json_value *prompt = ch_json_array_get(prompts, index);
        (void)printf(" %c %-18.18s %-7.7s %-36.36s %s\n",
                     index == snapshot->prompt_cursor ? '>' : ' ',
                     tui_string(prompt, "process_name"),
                     tui_string(prompt, "network"),
                     tui_string(prompt, "target"),
                     tui_string(prompt, "would_use_chain"));
    }
    (void)puts(" a/A allow once/forever · u allow until quit · b/B block once/forever · U block until quit");
}

static void tui_render(const tui_snapshot *snapshot, const char *base,
                       bool interactive) {
    bool compact = tui_terminal_columns() < 100U;
    if (interactive) (void)fputs("\033[H\033[2J", stdout);
    (void)printf("%s ClambHook C TUI %s  %s%s%s  %s\n",
                 interactive ? "\033[1;44;37m" : "",
                 interactive ? "\033[0m" : "",
                 snapshot->online && interactive ? "\033[1;32m" :
                    (!snapshot->online && interactive ? "\033[1;31m" : ""),
                 snapshot->online ? "ONLINE" : "OFFLINE",
                 interactive ? "\033[0m" : "",
                 base);
    if (!snapshot->online) {
        (void)printf("\n%s%s%s\n", interactive ? "\033[31m" : "",
                     snapshot->error, interactive ? "\033[0m" : "");
        if (interactive) (void)puts("\nq quit · r retry");
        (void)fflush(stdout);
        return;
    }
    if (snapshot->notice[0] != '\0') {
        (void)printf("%s%s%s\n", interactive ? "\033[33m" : "",
                     snapshot->notice, interactive ? "\033[0m" : "");
    }
    bool running = ch_json_bool_value(tui_member(snapshot->status, "running"), false);
    (void)printf("Status: %s%s%s  Profile: %s%s%s  Mode: %s\n",
                 interactive ? (running ? "\033[32m" : "\033[33m") : "",
                 running ? "RUNNING" : "STOPPED",
                 interactive ? "\033[0m" : "",
                 interactive ? "\033[1m" : "",
                 tui_string(snapshot->status, "profile"),
                 interactive ? "\033[0m" : "",
                 tui_string(snapshot->status, "tunnel_mode"));
    const ch_json_value *outline = tui_member(snapshot->status,
                                               "outline_refresh");
    if (ch_json_bool_value(tui_member(outline, "dynamic"), false)) {
        (void)printf("Outline: dynamic · last refresh %lld%s%s\n",
            (long long)tui_integer(outline, "last_success_ts_ns"),
            ch_json_bool_value(tui_member(outline, "stale"), false) ?
                " · STALE: " : "",
            tui_string(outline, "warning"));
    }
    const ch_json_value *network = tui_member(snapshot->status, "network_info");
    if (ch_json_value_type(network) == CH_JSON_OBJECT) {
        (void)printf("Network: %s%s%s\n", tui_string(network, "interface_name"),
                     tui_string(network, "ssid")[0] == '\0' ? "" : " · ",
                     tui_string(network, "ssid"));
    }
    tui_render_profiles(snapshot, interactive, compact);
    const ch_json_value *listeners = tui_member(snapshot->status, "listeners");
    (void)printf("\n%sListeners%s\n", interactive ? "\033[1;36m" : "",
                 interactive ? "\033[0m" : "");
    for (size_t index = 0U; index < ch_json_array_size(listeners); ++index) {
        const ch_json_value *listener = ch_json_array_get(listeners, index);
        (void)printf(" %-8.8s %-25.25s %lld active\n",
                     tui_string(listener, "protocol"), tui_string(listener, "addr"),
                     (long long)tui_integer(listener, "active_conns"));
    }
    if (ch_json_array_size(listeners) == 0U) (void)puts("  No active listeners");
    const ch_json_value *summary = tui_member(snapshot->traffic, "summary");
    char received[32];
    char sent[32];
    tui_format_bytes(tui_integer(summary, "rx_total"), received, sizeof(received));
    tui_format_bytes(tui_integer(summary, "tx_total"), sent, sizeof(sent));
    (void)printf("\n%sTraffic%s  %lld active · %s received · %s sent\n",
                 interactive ? "\033[1;36m" : "",
                 interactive ? "\033[0m" : "",
                 (long long)tui_integer(summary, "active_connections"),
                 received, sent);
    const ch_json_value *connections = tui_member(snapshot->traffic, "connections");
    size_t count = ch_json_array_size(connections);
    size_t shown = count < TUI_RECENT_CONNECTIONS ? count : TUI_RECENT_CONNECTIONS;
    for (size_t index = 0U; index < shown; ++index) {
        const ch_json_value *connection = ch_json_array_get(connections, index);
        if (compact) {
            (void)printf(" %-8.8s %-7.7s %-42.42s\n",
                         tui_string(connection, "state"),
                         tui_string(connection, "network"),
                         tui_string(connection, "target"));
        } else {
            (void)printf(" %-8.8s %-7.7s %-34.34s %-18.18s %s\n",
                         tui_string(connection, "state"), tui_string(connection, "network"),
                         tui_string(connection, "target"), tui_string(connection, "rule_name"),
                         tui_string(connection, "chain_name"));
        }
    }
    if (count == 0U) (void)puts("  No recent connections");
    tui_render_prompts(snapshot, interactive);
    if (interactive) {
        (void)puts("\nq quit · r refresh · ↑/↓ or j/k select · enter switch profile");
        (void)puts("c connect · d disconnect · actions pause while prompts need a decision");
        (void)puts("i import Outline key · v convert Mihomo/Surge file · R refresh dynamic profile");
    }
    (void)fflush(stdout);
}

static char *tui_json_string_body(const char *key, const char *value) {
    static const char hex[] = "0123456789abcdef";
    size_t length = strlen(value);
    if (length > (SIZE_MAX - strlen(key) - 16U) / 6U) return NULL;
    char *body = malloc(strlen(key) + length * 6U + 16U);
    if (body == NULL) return NULL;
    char *cursor = body;
    *cursor++ = '{'; *cursor++ = '"';
    size_t key_length = strlen(key);
    memcpy(cursor, key, key_length); cursor += key_length;
    *cursor++ = '"'; *cursor++ = ':'; *cursor++ = '"';
    for (const unsigned char *source = (const unsigned char *)value;
         *source != 0U; ++source) {
        if (*source == '"' || *source == '\\') {
            *cursor++ = '\\'; *cursor++ = (char)*source;
        } else if (*source == '\b' || *source == '\f' || *source == '\n' ||
                   *source == '\r' || *source == '\t') {
            *cursor++ = '\\';
            *cursor++ = *source == '\b' ? 'b' : (*source == '\f' ? 'f' :
                (*source == '\n' ? 'n' : (*source == '\r' ? 'r' : 't')));
        } else if (*source < 0x20U) {
            *cursor++ = '\\'; *cursor++ = 'u'; *cursor++ = '0'; *cursor++ = '0';
            *cursor++ = hex[*source >> 4U]; *cursor++ = hex[*source & 0x0fU];
        } else {
            *cursor++ = (char)*source;
        }
    }
    *cursor++ = '"'; *cursor++ = '}'; *cursor = '\0';
    return body;
}

static void tui_action(tui_snapshot *snapshot, const char *base,
                       const char *token, const char *method, const char *path,
                       const char *body) {
    char error[256] = {0};
    char *response = tui_request(base, token, method, path, body, error,
                                 sizeof(error));
    bool failed = response == NULL;
    free(response);
    tui_refresh(snapshot, base, token);
    tui_set_error(snapshot->notice, sizeof(snapshot->notice),
                  failed ? error : "Action completed");
}

static char *tui_prompt_line(const char *prompt, bool secret) {
    struct termios cooked = tui_saved_terminal;
    if (secret) cooked.c_lflag &= (tcflag_t)~ECHO;
    (void)tcsetattr(STDIN_FILENO, TCSAFLUSH, &cooked);
    (void)fputs("\033[?25h\n", stdout);
    (void)fputs(prompt, stdout);
    (void)fflush(stdout);
    char *line = NULL;
    size_t capacity = 0U;
    ssize_t length = getline(&line, &capacity, stdin);
    if (secret) (void)putchar('\n');
    struct termios raw = tui_saved_terminal;
    raw.c_lflag &= (tcflag_t)~(ICANON | ECHO);
    raw.c_iflag &= (tcflag_t)~(IXON | ICRNL);
    raw.c_cc[VMIN] = 0;
    raw.c_cc[VTIME] = 0;
    (void)tcsetattr(STDIN_FILENO, TCSAFLUSH, &raw);
    (void)fputs("\033[?25l", stdout);
    if (length <= 0) { free(line); return NULL; }
    while (length > 0 && (line[length - 1] == '\n' ||
           line[length - 1] == '\r')) line[--length] = '\0';
    return line;
}

static char *tui_outline_body(const char *access_key, const char *profile) {
    ch_json_buffer json;
    ch_json_init(&json);
    int okay = ch_json_append(&json, "{\"access_key\":") &&
        ch_json_append_string(&json, access_key) &&
        (profile == NULL || (ch_json_append(&json, ",\"profile_name\":") &&
                             ch_json_append_string(&json, profile) &&
                             ch_json_append(&json, ",\"activate\":false"))) &&
        ch_json_append(&json, "}");
    char *result = okay ? ch_json_take(&json) : NULL;
    ch_json_dispose(&json);
    return result;
}

static char *tui_read_converter_file(const char *path, char *error,
                                     size_t error_capacity) {
    FILE *file = fopen(path, "rb");
    if (file == NULL) {
        tui_set_error(error, error_capacity, "open converter source file");
        return NULL;
    }
    tui_buffer buffer = {0};
    unsigned char chunk[8192];
    while (!feof(file)) {
        size_t count = fread(chunk, 1U, sizeof(chunk), file);
        if (count > 0U && (!tui_buffer_reserve(&buffer, count) ||
                          buffer.length + count > TUI_BODY_LIMIT)) {
            buffer.failed = true;
            break;
        }
        if (count > 0U) {
            memcpy(buffer.data + buffer.length, chunk, count);
            buffer.length += count;
            buffer.data[buffer.length] = '\0';
        }
        if (ferror(file)) { buffer.failed = true; break; }
    }
    (void)fclose(file);
    if (buffer.failed || buffer.length == 0U) {
        free(buffer.data);
        tui_set_error(error, error_capacity,
                      buffer.failed ? "converter source exceeds 4 MiB or cannot be read" :
                                      "converter source is empty");
        return NULL;
    }
    return buffer.data;
}

static char *tui_converter_body(const char *source, const char *format,
                                const char *profile, const char *sha,
                                bool activate) {
    ch_json_buffer json;
    ch_json_init(&json);
    int okay = ch_json_append(&json, "{\"source\":") &&
        ch_json_append_string(&json, source) &&
        ch_json_append(&json, ",\"format\":") &&
        ch_json_append_string(&json, format) &&
        ch_json_append(&json, ",\"profile_name\":") &&
        ch_json_append_string(&json, profile);
    if (okay && sha != NULL) {
        okay = ch_json_append(&json, ",\"expected_sha256\":") &&
            ch_json_append_string(&json, sha) &&
            ch_json_append(&json, activate ? ",\"activate\":true" :
                                             ",\"activate\":false");
    }
    okay = okay && ch_json_append(&json, "}");
    char *result = okay ? ch_json_take(&json) : NULL;
    ch_json_dispose(&json);
    return result;
}

static void tui_converter_review_print(const ch_json_value *review) {
    const ch_json_value *profiles = tui_member(review, "profiles");
    const ch_json_value *profile = ch_json_array_get(profiles, 0U);
    (void)printf("\nConverter review (credentials hidden)\n"
                 "Format: %s · profile: %s · %lld chains · %lld groups · %lld rules\n",
                 tui_string(review, "format"), tui_string(profile, "suggested_name"),
                 (long long)tui_integer(profile, "chain_count"),
                 (long long)tui_integer(profile, "group_count"),
                 (long long)tui_integer(profile, "rule_count"));
    const ch_json_value *warnings = tui_member(review, "warnings");
    for (size_t index = 0U; index < ch_json_array_size(warnings); ++index) {
        const ch_json_value *warning = ch_json_array_get(warnings, index);
        (void)printf("Warning [%s] %s: %s\n", tui_string(warning, "code"),
                     tui_string(warning, "path"), tui_string(warning, "message"));
    }
}

static void tui_convert_profile(tui_snapshot *snapshot, const char *base,
                                const char *token) {
    char *path = tui_prompt_line("Mihomo YAML or Surge profile path: ", false);
    if (path == NULL || path[0] == '\0') { free(path); return; }
    char error[256] = {0};
    char *source = tui_read_converter_file(path, error, sizeof(error));
    free(path);
    if (source == NULL) {
        tui_set_error(snapshot->notice, sizeof(snapshot->notice), error);
        return;
    }
    char *format = tui_prompt_line("Format [auto/mihomo/surge, default auto]: ", false);
    char *profile = tui_prompt_line("Profile name [Converted Profile]: ", false);
    if (format == NULL || profile == NULL) { free(source); free(format); free(profile); return; }
    if (format[0] == '\0') { free(format); format = strdup("auto"); }
    if (profile[0] == '\0') { free(profile); profile = strdup("Converted Profile"); }
    char *body = format == NULL || profile == NULL ? NULL :
        tui_converter_body(source, format, profile, NULL, false);
    char *response = body == NULL ? NULL : tui_request(
        base, token, "POST", "/api/v1/config/converter/review", body,
        error, sizeof(error));
    free(body);
    ch_error parse_error;
    ch_json_value *review = response == NULL ? NULL :
        ch_json_parse(response, strlen(response), &parse_error);
    if (review == NULL) {
        tui_set_error(snapshot->notice, sizeof(snapshot->notice),
                      response == NULL ? error : "parse converter review");
        free(response); free(source); free(format); free(profile);
        return;
    }
    tui_converter_review_print(review);
    const char *sha_value = tui_string(review, "sha256");
    const char *toml = tui_string(review, "toml");
    char *sha = strdup(sha_value);
    char *export_toml = strdup(toml);
    ch_json_value_destroy(review); free(response);
    char *choice = tui_prompt_line("Merge, export sensitive TOML, or cancel? [m/e/N] ", false);
    if (choice != NULL && (choice[0] == 'm' || choice[0] == 'M')) {
        char *activation = tui_prompt_line("Activate imported profile? [y/N] ", false);
        bool activate = activation != NULL &&
            (activation[0] == 'y' || activation[0] == 'Y');
        free(activation);
        body = sha == NULL ? NULL : tui_converter_body(
            source, format, profile, sha, activate);
        if (body != NULL) tui_action(snapshot, base, token, "POST",
                                     "/api/v1/config/converter/import", body);
        free(body);
    } else if (choice != NULL && (choice[0] == 'e' || choice[0] == 'E')) {
        char *output = tui_prompt_line("Sensitive TOML output path: ", false);
        FILE *file = output == NULL || output[0] == '\0' ? NULL : fopen(output, "wb");
        if (file == NULL || export_toml == NULL ||
            fwrite(export_toml, 1U, strlen(export_toml), file) != strlen(export_toml)) {
            tui_set_error(snapshot->notice, sizeof(snapshot->notice),
                          "write sensitive converter export");
        } else {
            tui_set_error(snapshot->notice, sizeof(snapshot->notice),
                          "Sensitive TOML export written");
        }
        if (file != NULL) (void)fclose(file);
        free(output);
    }
    free(choice); free(sha); free(export_toml); free(source); free(format); free(profile);
}

static void tui_import_outline(tui_snapshot *snapshot, const char *base,
                               const char *token) {
    char *access_key = tui_prompt_line("Outline access key (hidden): ", true);
    if (access_key == NULL || access_key[0] == '\0') { free(access_key); return; }
    char *review_body = tui_outline_body(access_key, NULL);
    char error[256] = {0};
    char *review = review_body == NULL ? NULL : tui_request(
        base, token, "POST", "/api/v1/outline/review", review_body, error,
        sizeof(error));
    free(review_body);
    if (review == NULL) {
        tui_set_error(snapshot->notice, sizeof(snapshot->notice), error);
        free(access_key);
        return;
    }
    (void)printf("\nCompatibility preview (credentials hidden):\n%s\n", review);
    free(review);
    char *profile = tui_prompt_line("Profile name [Outline]: ", false);
    if (profile == NULL) { free(access_key); return; }
    if (profile[0] == '\0') {
        free(profile);
        profile = strdup("Outline");
    }
    char confirmation_prompt[256];
    (void)snprintf(confirmation_prompt, sizeof(confirmation_prompt),
                   "Import as profile %.160s without connecting? [y/N] ",
                   profile == NULL ? "Outline" : profile);
    char *confirmation = tui_prompt_line(confirmation_prompt, false);
    if (profile != NULL && confirmation != NULL &&
        (confirmation[0] == 'y' || confirmation[0] == 'Y')) {
        char *body = tui_outline_body(access_key, profile);
        if (body != NULL) {
            tui_action(snapshot, base, token, "POST",
                       "/api/v1/outline/import", body);
        }
        free(body);
    }
    free(confirmation);
    free(profile);
    free(access_key);
}

static void tui_refresh_outline(tui_snapshot *snapshot, const char *base,
                                const char *token) {
    if (ch_json_bool_value(tui_member(snapshot->status, "running"), false)) {
        tui_set_error(snapshot->notice, sizeof(snapshot->notice),
                      "Disconnect before refreshing an Outline profile");
        return;
    }
    const ch_json_value *profiles = tui_member(snapshot->profiles, "profiles");
    const char *name = ch_json_string_value(ch_json_array_get(
        profiles, snapshot->profile_cursor));
    if (name == NULL) return;
    char *body = tui_json_string_body("profile", name);
    if (body != NULL) {
        tui_action(snapshot, base, token, "POST",
                   "/api/v1/outline/refresh", body);
    }
    free(body);
}

static void tui_switch_profile(tui_snapshot *snapshot, const char *base,
                               const char *token) {
    const ch_json_value *profiles = tui_member(snapshot->profiles, "profiles");
    const char *name = ch_json_string_value(ch_json_array_get(
        profiles, snapshot->profile_cursor));
    if (name == NULL) return;
    char *body = tui_json_string_body("name", name);
    if (body == NULL) return;
    tui_action(snapshot, base, token, "PUT", "/api/v1/profiles/active", body);
    free(body);
}

static void tui_resolve_prompt(tui_snapshot *snapshot, const char *base,
                               const char *token, const char *action,
                               const char *scope) {
    const ch_json_value *prompts = tui_member(snapshot->prompts, "prompts");
    const ch_json_value *prompt = ch_json_array_get(prompts, snapshot->prompt_cursor);
    const char *identifier = tui_string(prompt, "id");
    if (identifier[0] == '\0') return;
    CURL *curl = curl_easy_init();
    if (curl == NULL) return;
    char *escaped = curl_easy_escape(curl, identifier, 0);
    if (escaped == NULL) {
        curl_easy_cleanup(curl);
        return;
    }
    size_t path_length = strlen(escaped) + 27U;
    char *path = malloc(path_length);
    if (path != NULL) {
        (void)snprintf(path, path_length, "/api/v1/prompts/%s/resolve", escaped);
        char body[192];
        (void)snprintf(body, sizeof(body),
            "{\"action\":\"%s\",\"scope\":\"%s\",\"match_host\":false,"
            "\"match_port\":false,\"match_protocol\":false,\"ttl_seconds\":0}",
            action, scope);
        tui_action(snapshot, base, token, "POST", path, body);
    }
    free(path);
    curl_free(escaped);
    curl_easy_cleanup(curl);
}

static int tui_read_key(void) {
    fd_set readable;
    FD_ZERO(&readable);
    FD_SET(STDIN_FILENO, &readable);
    struct timeval timeout = {.tv_sec = 0, .tv_usec = 250000};
    int ready;
    do {
        ready = select(STDIN_FILENO + 1, &readable, NULL, NULL, &timeout);
    } while (ready < 0 && errno == EINTR && !tui_stopping);
    if (ready <= 0) return TUI_KEY_NONE;
    unsigned char key = 0U;
    if (read(STDIN_FILENO, &key, 1U) != 1) return TUI_KEY_NONE;
    if (key != 27U) return (int)key;
    unsigned char sequence[2] = {0U, 0U};
    if (read(STDIN_FILENO, &sequence[0], 1U) != 1 || sequence[0] != '[' ||
        read(STDIN_FILENO, &sequence[1], 1U) != 1) return 27;
    if (sequence[1] == 'A') return TUI_KEY_UP;
    if (sequence[1] == 'B') return TUI_KEY_DOWN;
    return TUI_KEY_NONE;
}

static void tui_destroy(tui_snapshot *snapshot) {
    ch_json_value_destroy(snapshot->status);
    ch_json_value_destroy(snapshot->profiles);
    ch_json_value_destroy(snapshot->traffic);
    ch_json_value_destroy(snapshot->prompts);
}

static void tui_usage(FILE *stream) {
    (void)fprintf(stream,
        "usage: clambhook-tui [--help] [--version] [-api-token token] [host:port]\n"
        "\nInteractive keys:\n"
        "  Up/Down or j/k  Select a prompt or profile\n"
        "  Enter           Activate the selected profile\n"
        "  c/d             Connect or disconnect\n"
        "  r               Refresh now\n"
        "  q               Quit\n"
        "\nThe layout adapts automatically to narrow terminals.\n");
}

int main(int argc, char **argv) {
    const char *address = "127.0.0.1:9090";
    const char *token = getenv("CLAMBHOOK_API_TOKEN");
    for (int index = 1; index < argc; ++index) {
        if (strcmp(argv[index], "-h") == 0 || strcmp(argv[index], "--help") == 0) {
            tui_usage(stdout);
            return 0;
        }
        if (strcmp(argv[index], "-version") == 0 ||
            strcmp(argv[index], "--version") == 0) {
            (void)printf("clambhook-tui %s\n", CLAMBHOOK_VERSION);
            return 0;
        }
        if (strcmp(argv[index], "-api-token") == 0) {
            if (++index >= argc) { tui_usage(stderr); return 2; }
            token = argv[index];
        } else if (argv[index][0] == '-') {
            tui_usage(stderr);
            return 2;
        } else {
            address = argv[index];
        }
    }
    if (strstr(address, "://") != NULL || strchr(address, '/') != NULL) {
        (void)fputs("API address must be host:port\n", stderr);
        return 2;
    }
    size_t address_length = strlen(address);
    char *base = malloc(address_length + 8U);
    if (base == NULL) return 1;
    (void)snprintf(base, address_length + 8U, "http://%s", address);
    if (curl_global_init(CURL_GLOBAL_DEFAULT) != CURLE_OK) {
        free(base);
        return 1;
    }
    (void)signal(SIGINT, tui_stop);
    (void)signal(SIGTERM, tui_stop);
    (void)atexit(tui_restore_terminal);
    bool interactive = tui_enter_terminal();
    tui_snapshot snapshot = {0};
    tui_refresh(&snapshot, base, token);
    tui_render(&snapshot, base, interactive);
    if (!interactive) {
        int result = snapshot.online ? 0 : 1;
        tui_destroy(&snapshot);
        curl_global_cleanup();
        free(base);
        return result;
    }
    time_t next_refresh = time(NULL) + TUI_REFRESH_SECONDS;
    while (!tui_stopping) {
        int key = tui_read_key();
        bool redraw = false;
        const ch_json_value *prompts = tui_member(snapshot.prompts, "prompts");
        size_t prompt_count = ch_json_array_size(prompts);
        if (key != TUI_KEY_NONE) {
            if (key == 'q' || key == 3U) break;
            if (key == 'r') {
                tui_refresh(&snapshot, base, token); redraw = true;
            } else if (key == 'k' || key == TUI_KEY_UP) {
                if (prompt_count > 0U && snapshot.prompt_cursor > 0U) snapshot.prompt_cursor--;
                else if (prompt_count == 0U && snapshot.profile_cursor > 0U) snapshot.profile_cursor--;
                redraw = true;
            } else if (key == 'j' || key == TUI_KEY_DOWN) {
                if (prompt_count > 0U && snapshot.prompt_cursor + 1U < prompt_count) snapshot.prompt_cursor++;
                else {
                    const ch_json_value *profiles = tui_member(snapshot.profiles, "profiles");
                    if (prompt_count == 0U && snapshot.profile_cursor + 1U < ch_json_array_size(profiles)) snapshot.profile_cursor++;
                }
                redraw = true;
            } else if ((key == '\r' || key == '\n') && prompt_count == 0U) {
                tui_switch_profile(&snapshot, base, token); redraw = true;
            } else if (key == 'c' && prompt_count == 0U) {
                tui_action(&snapshot, base, token, "POST", "/api/v1/connect", NULL); redraw = true;
            } else if (key == 'd' && prompt_count == 0U) {
                tui_action(&snapshot, base, token, "POST", "/api/v1/disconnect", NULL); redraw = true;
            } else if (key == 'i' && prompt_count == 0U) {
                tui_import_outline(&snapshot, base, token); redraw = true;
            } else if (key == 'v' && prompt_count == 0U) {
                tui_convert_profile(&snapshot, base, token); redraw = true;
            } else if (key == 'R' && prompt_count == 0U) {
                tui_refresh_outline(&snapshot, base, token); redraw = true;
            } else if (prompt_count > 0U) {
                if (key == 'a') tui_resolve_prompt(&snapshot, base, token, "allow", "once");
                else if (key == 'A') tui_resolve_prompt(&snapshot, base, token, "allow", "forever");
                else if (key == 'u') tui_resolve_prompt(&snapshot, base, token, "allow", "until_quit");
                else if (key == 'b') tui_resolve_prompt(&snapshot, base, token, "block", "once");
                else if (key == 'B') tui_resolve_prompt(&snapshot, base, token, "block", "forever");
                else if (key == 'U') tui_resolve_prompt(&snapshot, base, token, "block", "until_quit");
                else continue;
                redraw = true;
            }
        }
        time_t now = time(NULL);
        if (now >= next_refresh) {
            tui_refresh(&snapshot, base, token);
            next_refresh = now + TUI_REFRESH_SECONDS;
            redraw = true;
        }
        if (redraw) tui_render(&snapshot, base, true);
    }
    tui_destroy(&snapshot);
    curl_global_cleanup();
    free(base);
    return 0;
}
