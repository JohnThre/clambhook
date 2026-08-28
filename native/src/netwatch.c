// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

#include "clambhook/netwatch.h"

#include <ctype.h>
#include <errno.h>
#include <limits.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <strings.h>

#include <uv.h>

#include "internal.h"

#ifdef __APPLE__
#include <fcntl.h>
#include <poll.h>
#include <signal.h>
#include <spawn.h>
#include <sys/wait.h>
#include <time.h>
#include <unistd.h>
extern char **environ;
#elif defined(__linux__)
#include <ifaddrs.h>
#include <net/if.h>
#endif

#define CH_NETWATCH_DEFAULT_POLL_MS 10000U
#define CH_NETWATCH_PROBE_TIMEOUT_MS 5000U
#define CH_NETWATCH_OUTPUT_CAPACITY 8192U

struct ch_netwatch {
    uv_async_t async;
    uv_thread_t worker;
    uv_mutex_t mutex;
    uv_cond_t condition;
    ch_netwatch_options options;
    ch_network_info last;
    ch_network_info pending_info;
    char pending_log[512];
    int pending_log_level;
    bool has_last;
    bool pending_observation;
    bool pending_log_message;
    bool ssid_warning_active;
    bool stopping;
    bool worker_started;
};

static void ch_trim_bounds(const char *text, const char **out_start,
                           const char **out_end) {
    const char *start = text == NULL ? "" : text;
    while (isspace((unsigned char)*start)) ++start;
    const char *end = start + strlen(start);
    while (end > start && isspace((unsigned char)end[-1])) --end;
    *out_start = start;
    *out_end = end;
}

static bool ch_copy_range(char *destination, size_t capacity,
                          const char *start, const char *end) {
    if (destination == NULL || capacity == 0U || start == NULL || end < start) {
        return false;
    }
    size_t length = (size_t)(end - start);
    if (length >= capacity) return false;
    if (length > 0U) memcpy(destination, start, length);
    destination[length] = '\0';
    return true;
}

static bool ch_copy_trimmed(char *destination, size_t capacity,
                            const char *text) {
    const char *start;
    const char *end;
    ch_trim_bounds(text, &start, &end);
    return ch_copy_range(destination, capacity, start, end);
}

bool ch_netwatch_valid_interface_name(const char *name) {
    if (name == NULL) return false;
    size_t length = strlen(name);
    if (length == 0U || length >= CH_NETWORK_INTERFACE_CAPACITY) return false;
    for (size_t index = 0U; index < length; ++index) {
        unsigned char character = (unsigned char)name[index];
        bool valid = isalnum(character) || character == '.' ||
            character == '_' || (index > 0U && character == '-');
        if (!valid) return false;
    }
    return true;
}

static bool ch_line_copy(const char *start, const char *end, char *line,
                         size_t capacity) {
    while (start < end && isspace((unsigned char)*start)) ++start;
    while (end > start && isspace((unsigned char)end[-1])) --end;
    return ch_copy_range(line, capacity, start, end);
}

bool ch_netwatch_parse_scutil_interface(const char *text, char *out_interface,
                                        size_t capacity) {
    if (text == NULL || out_interface == NULL || capacity == 0U) return false;
    out_interface[0] = '\0';
    const char *line_start = text;
    while (*line_start != '\0') {
        const char *line_end = strchr(line_start, '\n');
        if (line_end == NULL) line_end = line_start + strlen(line_start);
        char line[512];
        if (ch_line_copy(line_start, line_end, line, sizeof(line)) &&
            strncmp(line, "interface", 9U) == 0) {
            const char *separator = strchr(line, ':');
            char candidate[CH_NETWORK_INTERFACE_CAPACITY];
            if (separator != NULL &&
                ch_copy_trimmed(candidate, sizeof(candidate), separator + 1) &&
                ch_netwatch_valid_interface_name(candidate) &&
                strlen(candidate) < capacity) {
                (void)strcpy(out_interface, candidate);
                return true;
            }
        }
        if (*line_end == '\0') break;
        line_start = line_end + 1;
    }
    return false;
}

void ch_netwatch_parse_airport(const char *text, char *out_ssid,
                               size_t capacity, bool *out_is_wifi,
                               bool *out_associated) {
    static const char prefix[] = "Current Wi-Fi Network:";
    if (out_ssid != NULL && capacity > 0U) out_ssid[0] = '\0';
    if (out_is_wifi != NULL) *out_is_wifi = true;
    if (out_associated != NULL) *out_associated = false;
    const char *start;
    const char *end;
    ch_trim_bounds(text, &start, &end);
    if (strstr(start, "not a Wi-Fi interface") != NULL) {
        if (out_is_wifi != NULL) *out_is_wifi = false;
        return;
    }
    size_t prefix_length = sizeof(prefix) - 1U;
    if ((size_t)(end - start) >= prefix_length &&
        strncmp(start, prefix, prefix_length) == 0) {
        const char *ssid_start = start + prefix_length;
        while (ssid_start < end && isspace((unsigned char)*ssid_start)) {
            ++ssid_start;
        }
        if (out_ssid != NULL &&
            ch_copy_range(out_ssid, capacity, ssid_start, end) &&
            out_ssid[0] != '\0') {
            if (out_associated != NULL) *out_associated = true;
        }
    }
}

bool ch_netwatch_parse_ipconfig_ssid(const char *text, char *out_ssid,
                                     size_t capacity) {
    if (text == NULL || out_ssid == NULL || capacity == 0U) return false;
    out_ssid[0] = '\0';
    const char *line_start = text;
    while (*line_start != '\0') {
        const char *line_end = strchr(line_start, '\n');
        if (line_end == NULL) line_end = line_start + strlen(line_start);
        char line[512];
        if (ch_line_copy(line_start, line_end, line, sizeof(line)) &&
            strncmp(line, "SSID", 4U) == 0) {
            const char *separator = line + 4U;
            while (isspace((unsigned char)*separator)) ++separator;
            if (*separator == ':') {
                char candidate[CH_NETWORK_SSID_CAPACITY];
                if (ch_copy_trimmed(candidate, sizeof(candidate), separator + 1) &&
                    candidate[0] != '\0' && strcmp(candidate, "<redacted>") != 0 &&
                    strlen(candidate) < capacity) {
                    (void)strcpy(out_ssid, candidate);
                    return true;
                }
                return false;
            }
        }
        if (*line_end == '\0') break;
        line_start = line_end + 1;
    }
    return false;
}

bool ch_netwatch_parse_proc_wireless(const char *text, char *out_interface,
                                     size_t capacity) {
    if (text == NULL || out_interface == NULL || capacity == 0U) return false;
    out_interface[0] = '\0';
    const char *line_start = text;
    unsigned int line_number = 0U;
    while (*line_start != '\0') {
        const char *line_end = strchr(line_start, '\n');
        if (line_end == NULL) line_end = line_start + strlen(line_start);
        ++line_number;
        if (line_number > 2U) {
            const char *separator = memchr(line_start, ':',
                                           (size_t)(line_end - line_start));
            if (separator != NULL) {
                char candidate[CH_NETWORK_INTERFACE_CAPACITY];
                if (ch_line_copy(line_start, separator, candidate,
                                 sizeof(candidate)) &&
                    ch_netwatch_valid_interface_name(candidate) &&
                    strlen(candidate) < capacity) {
                    (void)strcpy(out_interface, candidate);
                    return true;
                }
            }
        }
        if (*line_end == '\0') break;
        line_start = line_end + 1;
    }
    return false;
}

static bool ch_trimmed_equal_fold(const char *observed, const char *trigger) {
    const char *left_start;
    const char *left_end;
    const char *right_start;
    const char *right_end;
    ch_trim_bounds(observed, &left_start, &left_end);
    ch_trim_bounds(trigger, &right_start, &right_end);
    size_t left_length = (size_t)(left_end - left_start);
    size_t right_length = (size_t)(right_end - right_start);
    return left_length == right_length &&
        strncasecmp(left_start, right_start, left_length) == 0;
}

bool ch_network_info_matches(const ch_network_info *info, const char *ssid,
                             const char *interface_name) {
    if (info == NULL) return false;
    const char *ssid_start;
    const char *ssid_end;
    const char *interface_start;
    const char *interface_end;
    ch_trim_bounds(ssid, &ssid_start, &ssid_end);
    ch_trim_bounds(interface_name, &interface_start, &interface_end);
    bool has_ssid = ssid_end > ssid_start;
    bool has_interface = interface_end > interface_start;
    if (!has_ssid && !has_interface) return false;
    if (has_ssid && !ch_trimmed_equal_fold(info->ssid, ssid)) return false;
    if (has_interface &&
        !ch_trimmed_equal_fold(info->interface_name, interface_name)) {
        return false;
    }
    return true;
}

static bool ch_network_info_equal(const ch_network_info *left,
                                  const ch_network_info *right) {
    return left->is_wifi == right->is_wifi &&
        strcmp(left->interface_name, right->interface_name) == 0 &&
        strcmp(left->ssid, right->ssid) == 0;
}

#ifdef __APPLE__
static uint64_t ch_netwatch_now_milliseconds(void) {
    struct timespec time;
    if (clock_gettime(CLOCK_MONOTONIC, &time) != 0) return 0U;
    return (uint64_t)time.tv_sec * UINT64_C(1000) +
        (uint64_t)time.tv_nsec / UINT64_C(1000000);
}

static int ch_netwatch_remaining_milliseconds(uint64_t deadline) {
    uint64_t now = ch_netwatch_now_milliseconds();
    if (now >= deadline) return 0;
    uint64_t remaining = deadline - now;
    return remaining > (uint64_t)INT_MAX ? INT_MAX : (int)remaining;
}

static bool ch_netwatch_run_command(const char *path, char *const arguments[],
                                    uint64_t deadline, char *output,
                                    size_t capacity, bool *out_success,
                                    ch_error *error) {
    int descriptors[2];
    posix_spawn_file_actions_t actions;
    posix_spawnattr_t attributes;
    pid_t process = 0;
    int status = 0;
    bool child_done = false;
    bool pipe_done = false;
    size_t used = 0U;
    *out_success = false;
    if (capacity > 0U) output[0] = '\0';
    if (pipe(descriptors) != 0) {
        ch_error_set(error, CH_ERROR_IO, "netwatch pipe: %s", strerror(errno));
        return false;
    }
    int flags = fcntl(descriptors[0], F_GETFL, 0);
    if (flags < 0 || fcntl(descriptors[0], F_SETFL, flags | O_NONBLOCK) != 0) {
        (void)close(descriptors[0]);
        (void)close(descriptors[1]);
        ch_error_set(error, CH_ERROR_IO, "prepare netwatch command");
        return false;
    }
    if (posix_spawn_file_actions_init(&actions) != 0) {
        (void)close(descriptors[0]);
        (void)close(descriptors[1]);
        ch_error_set(error, CH_ERROR_IO, "prepare netwatch command actions");
        return false;
    }
    if (posix_spawnattr_init(&attributes) != 0) {
        (void)posix_spawn_file_actions_destroy(&actions);
        (void)close(descriptors[0]);
        (void)close(descriptors[1]);
        ch_error_set(error, CH_ERROR_IO,
                     "prepare netwatch command attributes");
        return false;
    }
#ifdef POSIX_SPAWN_CLOEXEC_DEFAULT
    int attribute_status = posix_spawnattr_setflags(
        &attributes, (short)POSIX_SPAWN_CLOEXEC_DEFAULT);
#else
    int attribute_status = 0;
#endif
    int action_status = posix_spawn_file_actions_adddup2(
        &actions, descriptors[1], STDOUT_FILENO);
    if (action_status == 0) {
        action_status = posix_spawn_file_actions_adddup2(
            &actions, descriptors[1], STDERR_FILENO);
    }
    if (action_status == 0) {
        action_status = posix_spawn_file_actions_addclose(&actions,
                                                          descriptors[0]);
    }
    if (action_status == 0) {
        action_status = posix_spawn_file_actions_addclose(&actions,
                                                          descriptors[1]);
    }
    int spawn_status = action_status == 0 && attribute_status == 0 ?
        posix_spawn(&process, path, &actions, &attributes, arguments,
                    environ) :
        (action_status != 0 ? action_status : attribute_status);
    (void)posix_spawn_file_actions_destroy(&actions);
    (void)posix_spawnattr_destroy(&attributes);
    (void)close(descriptors[1]);
    if (spawn_status != 0) {
        (void)close(descriptors[0]);
        ch_error_set(error, CH_ERROR_IO, "start %s: %s", path,
                     strerror(spawn_status));
        return false;
    }
    while (!child_done || !pipe_done) {
        char bytes[1024];
        for (;;) {
            ssize_t count = read(descriptors[0], bytes, sizeof(bytes));
            if (count > 0) {
                size_t available = capacity > used + 1U ? capacity - used - 1U : 0U;
                size_t copy = (size_t)count < available ? (size_t)count : available;
                if (copy > 0U) {
                    memcpy(output + used, bytes, copy);
                    used += copy;
                    output[used] = '\0';
                }
                continue;
            }
            if (count == 0) pipe_done = true;
            if (count < 0 && errno == EINTR) continue;
            break;
        }
        if (!child_done) {
            pid_t waited = waitpid(process, &status, WNOHANG);
            if (waited == process) child_done = true;
            else if (waited < 0 && errno != EINTR) child_done = true;
        }
        if (child_done && pipe_done) break;
        int remaining = ch_netwatch_remaining_milliseconds(deadline);
        if (remaining <= 0) {
            (void)kill(process, SIGKILL);
            while (waitpid(process, &status, 0) < 0 && errno == EINTR) {}
            (void)close(descriptors[0]);
            ch_error_set(error, CH_ERROR_IO, "netwatch probe timed out");
            return false;
        }
        struct pollfd poll_descriptor = {
            .fd = descriptors[0],
            .events = POLLIN | POLLHUP
        };
        int poll_timeout = remaining > 50 ? 50 : remaining;
        int polled;
        do {
            polled = poll(&poll_descriptor, 1U, poll_timeout);
        } while (polled < 0 && errno == EINTR);
        if (polled < 0) {
            (void)kill(process, SIGKILL);
            while (waitpid(process, &status, 0) < 0 && errno == EINTR) {}
            (void)close(descriptors[0]);
            ch_error_set(error, CH_ERROR_IO, "poll %s: %s", path,
                         strerror(errno));
            return false;
        }
    }
    (void)close(descriptors[0]);
    *out_success = WIFEXITED(status) && WEXITSTATUS(status) == 0;
    return true;
}

static ch_status ch_netwatch_current_apple(ch_network_info *out_info,
                                           ch_error *error) {
    uint64_t deadline = ch_netwatch_now_milliseconds() +
        CH_NETWATCH_PROBE_TIMEOUT_MS;
    char output[CH_NETWATCH_OUTPUT_CAPACITY];
    bool success = false;
    char *scutil_arguments[] = {"scutil", "--nwi", NULL};
    if (!ch_netwatch_run_command("/usr/sbin/scutil", scutil_arguments,
                                 deadline, output, sizeof(output), &success,
                                 error)) {
        return CH_ERROR_IO;
    }
    if (!success || !ch_netwatch_parse_scutil_interface(
            output, out_info->interface_name,
            sizeof(out_info->interface_name))) {
        return CH_OK;
    }
    char *networksetup_arguments[] = {
        "networksetup", "-getairportnetwork", out_info->interface_name, NULL
    };
    if (!ch_netwatch_run_command("/usr/sbin/networksetup",
                                 networksetup_arguments, deadline, output,
                                 sizeof(output), &success, error)) {
        return CH_ERROR_IO;
    }
    bool associated = false;
    ch_netwatch_parse_airport(output, out_info->ssid, sizeof(out_info->ssid),
                              &out_info->is_wifi, &associated);
    if (!out_info->is_wifi || (associated && out_info->ssid[0] != '\0')) {
        return CH_OK;
    }
    char *ipconfig_arguments[] = {
        "ipconfig", "getsummary", out_info->interface_name, NULL
    };
    if (!ch_netwatch_run_command("/usr/sbin/ipconfig", ipconfig_arguments,
                                 deadline, output, sizeof(output), &success,
                                 error)) {
        return CH_ERROR_IO;
    }
    if (success) {
        (void)ch_netwatch_parse_ipconfig_ssid(
            output, out_info->ssid, sizeof(out_info->ssid));
    }
    return CH_OK;
}
#endif

#ifdef __linux__
static ch_status ch_netwatch_current_linux(ch_network_info *out_info,
                                           ch_error *error) {
    FILE *wireless = fopen("/proc/net/wireless", "r");
    if (wireless != NULL) {
        char text[CH_NETWATCH_OUTPUT_CAPACITY];
        size_t length = fread(text, 1U, sizeof(text) - 1U, wireless);
        text[length] = '\0';
        (void)fclose(wireless);
        if (ch_netwatch_parse_proc_wireless(
                text, out_info->interface_name,
                sizeof(out_info->interface_name))) {
            out_info->is_wifi = true;
            return CH_OK;
        }
    }
    struct ifaddrs *interfaces = NULL;
    if (getifaddrs(&interfaces) != 0) {
        ch_error_set(error, CH_ERROR_IO, "enumerate interfaces: %s",
                     strerror(errno));
        return CH_ERROR_IO;
    }
    for (const struct ifaddrs *item = interfaces; item != NULL;
         item = item->ifa_next) {
        if (item->ifa_name == NULL ||
            (item->ifa_flags & IFF_UP) == 0U ||
            (item->ifa_flags & IFF_LOOPBACK) != 0U ||
            !ch_netwatch_valid_interface_name(item->ifa_name)) {
            continue;
        }
        (void)snprintf(out_info->interface_name,
                       sizeof(out_info->interface_name), "%s",
                       item->ifa_name);
        break;
    }
    freeifaddrs(interfaces);
    return CH_OK;
}
#endif

ch_status ch_netwatch_current(ch_network_info *out_info, void *context,
                              ch_error *error) {
    (void)context;
    ch_error_clear(error);
    if (out_info == NULL) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "network info output is required");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    memset(out_info, 0, sizeof(*out_info));
#ifdef __APPLE__
    return ch_netwatch_current_apple(out_info, error);
#elif defined(__linux__)
    return ch_netwatch_current_linux(out_info, error);
#else
    return CH_OK;
#endif
}

static void ch_netwatch_queue_log(ch_netwatch *watcher, int level,
                                  const char *message) {
    uv_mutex_lock(&watcher->mutex);
    if (!watcher->stopping) {
        (void)snprintf(watcher->pending_log, sizeof(watcher->pending_log),
                       "%s", message == NULL ? "netwatch probe failed" :
                                               message);
        watcher->pending_log_level = level;
        watcher->pending_log_message = true;
    }
    uv_mutex_unlock(&watcher->mutex);
    (void)uv_async_send(&watcher->async);
}

static void ch_netwatch_queue_observation(ch_netwatch *watcher,
                                          const ch_network_info *info) {
    uv_mutex_lock(&watcher->mutex);
    if (!watcher->stopping) {
        watcher->pending_info = *info;
        watcher->pending_observation = true;
    }
    uv_mutex_unlock(&watcher->mutex);
    (void)uv_async_send(&watcher->async);
}

static void ch_netwatch_worker(void *context) {
    ch_netwatch *watcher = context;
    bool first = true;
    for (;;) {
        uv_mutex_lock(&watcher->mutex);
        if (!first && !watcher->stopping) {
            uint64_t wait_nanoseconds =
                (uint64_t)watcher->options.poll_milliseconds *
                UINT64_C(1000000);
            (void)uv_cond_timedwait(&watcher->condition, &watcher->mutex,
                                    wait_nanoseconds);
        }
        bool stopping = watcher->stopping;
        uv_mutex_unlock(&watcher->mutex);
        if (stopping) break;
        first = false;

        ch_network_info info;
        ch_error error;
        ch_status status = watcher->options.probe(
            &info, watcher->options.probe_context, &error);
        if (status != CH_OK) {
            ch_netwatch_queue_log(watcher, 2, error.message);
            continue;
        }
#ifdef __APPLE__
        bool ssid_unavailable = info.is_wifi && info.ssid[0] == '\0' &&
            info.interface_name[0] != '\0';
        if (ssid_unavailable && !watcher->ssid_warning_active) {
            char warning[512];
            (void)snprintf(
                warning, sizeof(warning),
                "netwatch: SSID unavailable for Wi-Fi interface \"%s\"; "
                "macOS 14+ requires Location Services authorization for SSID "
                "triggers. Interface-only triggers still apply.",
                info.interface_name);
            ch_netwatch_queue_log(watcher, 2, warning);
        }
        watcher->ssid_warning_active = ssid_unavailable;
#endif
        if (!watcher->has_last || !ch_network_info_equal(&watcher->last,
                                                          &info)) {
            watcher->last = info;
            watcher->has_last = true;
            ch_netwatch_queue_observation(watcher, &info);
        }
    }
}

static void ch_netwatch_async(uv_async_t *async) {
    ch_netwatch *watcher = async->data;
    ch_network_info info;
    char log_message[512];
    bool has_observation;
    bool has_log;
    int log_level;
    ch_network_observation_callback observation;
    ch_netwatch_log_callback log;
    void *observation_context;
    void *log_context;
    uv_mutex_lock(&watcher->mutex);
    has_observation = watcher->pending_observation;
    has_log = watcher->pending_log_message;
    info = watcher->pending_info;
    log_level = watcher->pending_log_level;
    (void)snprintf(log_message, sizeof(log_message), "%s",
                   watcher->pending_log);
    watcher->pending_observation = false;
    watcher->pending_log_message = false;
    observation = watcher->options.observation;
    observation_context = watcher->options.observation_context;
    log = watcher->options.log;
    log_context = watcher->options.log_context;
    uv_mutex_unlock(&watcher->mutex);
    if (has_log && log != NULL) log(log_level, log_message, log_context);
    if (has_observation && observation != NULL) {
        observation(&info, observation_context);
    }
}

static void ch_netwatch_closed(uv_handle_t *handle) {
    ch_netwatch *watcher = handle->data;
    uv_cond_destroy(&watcher->condition);
    uv_mutex_destroy(&watcher->mutex);
    free(watcher);
}

ch_netwatch *ch_netwatch_start(uv_loop_t *loop,
                               const ch_netwatch_options *options,
                               ch_error *error) {
    ch_error_clear(error);
    if (loop == NULL || options == NULL || options->observation == NULL) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "netwatch loop, options, and observation are required");
        return NULL;
    }
    ch_netwatch *watcher = calloc(1U, sizeof(*watcher));
    if (watcher == NULL) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY, "allocate netwatch");
        return NULL;
    }
    watcher->options = *options;
    if (watcher->options.poll_milliseconds == 0U) {
        watcher->options.poll_milliseconds = CH_NETWATCH_DEFAULT_POLL_MS;
    }
    if (watcher->options.probe == NULL) {
        watcher->options.probe = ch_netwatch_current;
    }
    if (uv_mutex_init(&watcher->mutex) != 0) {
        free(watcher);
        ch_error_set(error, CH_ERROR_INTERNAL, "initialize netwatch mutex");
        return NULL;
    }
    if (uv_cond_init(&watcher->condition) != 0) {
        uv_mutex_destroy(&watcher->mutex);
        free(watcher);
        ch_error_set(error, CH_ERROR_INTERNAL, "initialize netwatch condition");
        return NULL;
    }
    if (uv_async_init(loop, &watcher->async, ch_netwatch_async) != 0) {
        uv_cond_destroy(&watcher->condition);
        uv_mutex_destroy(&watcher->mutex);
        free(watcher);
        ch_error_set(error, CH_ERROR_INTERNAL, "initialize netwatch async");
        return NULL;
    }
    watcher->async.data = watcher;
    if (uv_thread_create(&watcher->worker, ch_netwatch_worker, watcher) != 0) {
        watcher->stopping = true;
        uv_close((uv_handle_t *)&watcher->async, ch_netwatch_closed);
        ch_error_set(error, CH_ERROR_INTERNAL, "start netwatch worker");
        return NULL;
    }
    watcher->worker_started = true;
    return watcher;
}

void ch_netwatch_stop(ch_netwatch *watcher) {
    if (watcher == NULL) return;
    uv_mutex_lock(&watcher->mutex);
    if (watcher->stopping) {
        uv_mutex_unlock(&watcher->mutex);
        return;
    }
    watcher->stopping = true;
    uv_cond_signal(&watcher->condition);
    uv_mutex_unlock(&watcher->mutex);
    if (watcher->worker_started) {
        (void)uv_thread_join(&watcher->worker);
        watcher->worker_started = false;
    }
    if (!uv_is_closing((uv_handle_t *)&watcher->async)) {
        uv_close((uv_handle_t *)&watcher->async, ch_netwatch_closed);
    }
}
