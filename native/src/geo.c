// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

#include "geo.h"

#include <arpa/inet.h>
#include <ctype.h>
#include <errno.h>
#include <netdb.h>
#include <pthread.h>
#include <stdbool.h>
#include <stddef.h>
#include <stdlib.h>
#include <string.h>
#include <sys/socket.h>
#include <time.h>

#include <maxminddb.h>

#include "internal.h"

struct ch_geo_reader {
    pthread_mutex_t reference_mutex;
    size_t references;
    MMDB_s database;
};

static char *ch_geo_config_database(const ch_config *config) {
    const ch_config_table *root = ch_config_root(config);
    const ch_config_table *geo = ch_config_table_get_table(root, "geo");
    char *path = NULL;
    ch_error ignored;
    if (geo == NULL || ch_config_table_get_string(
            geo, "database", &path, &ignored) != CH_OK) {
        free(path);
        return ch_strdup("");
    }
    return path;
}

ch_status ch_geo_reader_open_config(const ch_config *config,
                                    ch_geo_reader **out_reader,
                                    ch_error *error) {
    ch_error_clear(error);
    if (out_reader == NULL) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "geo reader output is required");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    *out_reader = NULL;
    if (config == NULL) return CH_OK;
    char *configured = ch_geo_config_database(config);
    if (configured == NULL) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "copy geo database path");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    if (configured[0] == '\0') {
        free(configured);
        return CH_OK;
    }
    char *resolved = NULL;
    ch_status status = ch_config_resolve_path(
        config, configured, &resolved, error);
    free(configured);
    if (status != CH_OK) return status;
    ch_geo_reader *reader = calloc(1U, sizeof(*reader));
    if (reader == NULL) {
        free(resolved);
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "allocate geo reader");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    if (pthread_mutex_init(&reader->reference_mutex, NULL) != 0) {
        free(reader);
        free(resolved);
        ch_error_set(error, CH_ERROR_INTERNAL,
                     "initialize geo reader lock");
        return CH_ERROR_INTERNAL;
    }
    int mmdb_status = MMDB_open(resolved, MMDB_MODE_MMAP, &reader->database);
    if (mmdb_status != MMDB_SUCCESS) {
        pthread_mutex_destroy(&reader->reference_mutex);
        free(reader);
        ch_error_set(error, CH_ERROR_IO, "open geo database %s: %s",
                     resolved, MMDB_strerror(mmdb_status));
        free(resolved);
        return CH_ERROR_IO;
    }
    free(resolved);
    reader->references = 1U;
    *out_reader = reader;
    return CH_OK;
}

void ch_geo_reader_retain(ch_geo_reader *reader) {
    if (reader == NULL) return;
    pthread_mutex_lock(&reader->reference_mutex);
    if (reader->references < SIZE_MAX) ++reader->references;
    pthread_mutex_unlock(&reader->reference_mutex);
}

void ch_geo_reader_release(ch_geo_reader *reader) {
    if (reader == NULL) return;
    int destroy = 0;
    pthread_mutex_lock(&reader->reference_mutex);
    if (reader->references > 0U) {
        --reader->references;
        destroy = reader->references == 0U;
    }
    pthread_mutex_unlock(&reader->reference_mutex);
    if (!destroy) return;
    MMDB_close(&reader->database);
    pthread_mutex_destroy(&reader->reference_mutex);
    free(reader);
}

void ch_geo_location_clear(ch_geo_location *location) {
    if (location == NULL) return;
    free(location->country);
    free(location->country_code);
    free(location->city);
    memset(location, 0, sizeof(*location));
}

static char *ch_geo_copy_range(const char *start, size_t length) {
    if (length == SIZE_MAX) return NULL;
    char *copy = malloc(length + 1U);
    if (copy == NULL) return NULL;
    memcpy(copy, start, length);
    copy[length] = '\0';
    return copy;
}

static int ch_geo_is_service(const char *value) {
    if (value == NULL || value[0] == '\0') return 0;
    for (const unsigned char *cursor = (const unsigned char *)value;
         *cursor != '\0'; ++cursor) {
        if (!isdigit(*cursor)) return 0;
    }
    return 1;
}

static char *ch_geo_address_host(const char *address, ch_error *error) {
    if (address == NULL || address[0] == '\0') {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT, "empty address");
        return NULL;
    }
    if (address[0] == '[') {
        const char *closing = strchr(address + 1U, ']');
        if (closing != NULL && closing > address + 1U) {
            return ch_geo_copy_range(address + 1U,
                                     (size_t)(closing - address - 1));
        }
    }
    unsigned char parsed[sizeof(struct in6_addr)];
    if (inet_pton(AF_INET, address, parsed) == 1 ||
        inet_pton(AF_INET6, address, parsed) == 1) {
        return ch_strdup(address);
    }
    const char *colon = strrchr(address, ':');
    if (colon != NULL && strchr(address, ':') == colon &&
        ch_geo_is_service(colon + 1U)) {
        if (colon == address) {
            ch_error_set(error, CH_ERROR_INVALID_ARGUMENT, "empty address");
            return NULL;
        }
        return ch_geo_copy_range(address, (size_t)(colon - address));
    }
    return ch_strdup(address);
}

typedef struct ch_geo_resolve_task {
    pthread_mutex_t mutex;
    pthread_cond_t condition;
    char *host;
    struct sockaddr_storage address;
    int done;
    int abandoned;
    int gai_status;
} ch_geo_resolve_task;

static void ch_geo_resolve_task_destroy(ch_geo_resolve_task *task) {
    if (task == NULL) return;
    pthread_cond_destroy(&task->condition);
    pthread_mutex_destroy(&task->mutex);
    free(task->host);
    free(task);
}

static void *ch_geo_resolve_main(void *context) {
    ch_geo_resolve_task *task = context;
    struct addrinfo hints;
    memset(&hints, 0, sizeof(hints));
    hints.ai_family = AF_UNSPEC;
    hints.ai_socktype = SOCK_STREAM;
    struct addrinfo *addresses = NULL;
    int result = getaddrinfo(task->host, NULL, &hints, &addresses);
    const struct addrinfo *selected = NULL;
    if (result == 0) {
        for (const struct addrinfo *item = addresses; item != NULL;
             item = item->ai_next) {
            if (item->ai_family == AF_INET) {
                selected = item;
                break;
            }
            if (selected == NULL && item->ai_family == AF_INET6) {
                selected = item;
            }
        }
    }
    pthread_mutex_lock(&task->mutex);
    if (result == 0 && selected != NULL && selected->ai_addr != NULL &&
        selected->ai_addrlen <= sizeof(task->address)) {
        memcpy(&task->address, selected->ai_addr, selected->ai_addrlen);
    } else {
        task->gai_status = result == 0 ? EAI_NONAME : result;
    }
    freeaddrinfo(addresses);
    task->done = 1;
    int abandoned = task->abandoned;
    pthread_cond_signal(&task->condition);
    pthread_mutex_unlock(&task->mutex);
    if (abandoned) ch_geo_resolve_task_destroy(task);
    return NULL;
}

static int ch_geo_numeric_address(const char *host,
                                  struct sockaddr_storage *out_address) {
    struct sockaddr_in *ipv4 = (struct sockaddr_in *)out_address;
    memset(out_address, 0, sizeof(*out_address));
    if (inet_pton(AF_INET, host, &ipv4->sin_addr) == 1) {
        ipv4->sin_family = AF_INET;
        return 1;
    }
    struct sockaddr_in6 *ipv6 = (struct sockaddr_in6 *)out_address;
    if (inet_pton(AF_INET6, host, &ipv6->sin6_addr) == 1) {
        ipv6->sin6_family = AF_INET6;
        return 1;
    }
    return 0;
}

static ch_status ch_geo_resolve(const char *host,
                                struct sockaddr_storage *out_address,
                                ch_error *error) {
    if (ch_geo_numeric_address(host, out_address)) return CH_OK;
    ch_geo_resolve_task *task = calloc(1U, sizeof(*task));
    if (task == NULL) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "allocate geo resolver task");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    task->host = ch_strdup(host);
    if (task->host == NULL || pthread_mutex_init(&task->mutex, NULL) != 0) {
        free(task->host);
        free(task);
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "initialize geo resolver task");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    if (pthread_cond_init(&task->condition, NULL) != 0) {
        pthread_mutex_destroy(&task->mutex);
        free(task->host);
        free(task);
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "initialize geo resolver condition");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    pthread_t worker;
    if (pthread_create(&worker, NULL, ch_geo_resolve_main, task) != 0) {
        ch_geo_resolve_task_destroy(task);
        ch_error_set(error, CH_ERROR_IO, "start geo hostname resolver");
        return CH_ERROR_IO;
    }
    (void)pthread_detach(worker);
    struct timespec deadline;
    if (clock_gettime(CLOCK_REALTIME, &deadline) != 0) {
        time_t now = time(NULL);
        deadline = (struct timespec){
            .tv_sec = now == (time_t)-1 ? (time_t)0 : now,
            .tv_nsec = 0
        };
    }
    if (deadline.tv_sec <= (time_t)(INT64_MAX - 2)) {
        deadline.tv_sec += 2;
    }
    pthread_mutex_lock(&task->mutex);
    int wait_status = 0;
    while (!task->done && wait_status == 0) {
        wait_status = pthread_cond_timedwait(
            &task->condition, &task->mutex, &deadline);
    }
    if (!task->done) {
        task->abandoned = 1;
        pthread_mutex_unlock(&task->mutex);
        ch_error_set(error, CH_ERROR_IO, "resolve %s: timeout", host);
        return CH_ERROR_IO;
    }
    int gai_status = task->gai_status;
    *out_address = task->address;
    pthread_mutex_unlock(&task->mutex);
    ch_geo_resolve_task_destroy(task);
    if (gai_status != 0) {
        ch_error_set(error, CH_ERROR_IO, "resolve %s: %s", host,
                     gai_strerror(gai_status));
        return CH_ERROR_IO;
    }
    return CH_OK;
}

static ch_status ch_geo_entry_string(MMDB_entry_s *entry,
                                     const char *first,
                                     const char *second,
                                     const char *third,
                                     char **out_value,
                                     ch_error *error) {
    *out_value = ch_strdup("");
    if (*out_value == NULL) return CH_ERROR_OUT_OF_MEMORY;
    MMDB_entry_data_s data;
    int status;
    if (third != NULL) {
        status = MMDB_get_value(entry, &data, first, second, third, NULL);
    } else if (second != NULL) {
        status = MMDB_get_value(entry, &data, first, second, NULL);
    } else {
        status = MMDB_get_value(entry, &data, first, NULL);
    }
    if (status == MMDB_LOOKUP_PATH_DOES_NOT_MATCH_DATA_ERROR ||
        (status == MMDB_SUCCESS && !data.has_data)) return CH_OK;
    if (status != MMDB_SUCCESS || data.type != MMDB_DATA_TYPE_UTF8_STRING) {
        free(*out_value);
        *out_value = NULL;
        ch_error_set(error, CH_ERROR_PARSE,
                     "decode geo database field: %s",
                     status == MMDB_SUCCESS ? "unexpected data type" :
                                              MMDB_strerror(status));
        return CH_ERROR_PARSE;
    }
    char *copy = ch_geo_copy_range(data.utf8_string, data.data_size);
    if (copy == NULL) {
        free(*out_value);
        *out_value = NULL;
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY, "copy geo field");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    free(*out_value);
    *out_value = copy;
    return CH_OK;
}

static ch_status ch_geo_entry_number(MMDB_entry_s *entry,
                                     const char *field,
                                     double *out_value,
                                     ch_error *error) {
    MMDB_entry_data_s data;
    int status = MMDB_get_value(
        entry, &data, "location", field, NULL);
    if (status == MMDB_LOOKUP_PATH_DOES_NOT_MATCH_DATA_ERROR ||
        (status == MMDB_SUCCESS && !data.has_data)) {
        *out_value = 0.0;
        return CH_OK;
    }
    if (status != MMDB_SUCCESS ||
        (data.type != MMDB_DATA_TYPE_DOUBLE &&
         data.type != MMDB_DATA_TYPE_FLOAT)) {
        ch_error_set(error, CH_ERROR_PARSE,
                     "decode geo database coordinate: %s",
                     status == MMDB_SUCCESS ? "unexpected data type" :
                                              MMDB_strerror(status));
        return CH_ERROR_PARSE;
    }
    *out_value = data.type == MMDB_DATA_TYPE_DOUBLE ? data.double_value :
                                                     (double)data.float_value;
    return CH_OK;
}

ch_status ch_geo_lookup(ch_geo_reader *reader, const char *address,
                        ch_geo_location *out_location, ch_error *error) {
    ch_error_clear(error);
    if (out_location == NULL) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "geo location output is required");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    memset(out_location, 0, sizeof(*out_location));
    if (reader == NULL) return CH_OK;
    char *host = ch_geo_address_host(address, error);
    if (host == NULL) {
        if (error == NULL || error->code == CH_OK) {
            ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                         "copy geo lookup host");
        }
        return error == NULL ? CH_ERROR_OUT_OF_MEMORY : error->code;
    }
    struct sockaddr_storage resolved;
    ch_status result = ch_geo_resolve(host, &resolved, error);
    free(host);
    if (result != CH_OK) return result;
    int mmdb_status = MMDB_SUCCESS;
    MMDB_lookup_result_s lookup = MMDB_lookup_sockaddr(
        &reader->database, (const struct sockaddr *)&resolved, &mmdb_status);
    if (mmdb_status != MMDB_SUCCESS) {
        ch_error_set(error, CH_ERROR_PARSE, "geo lookup: %s",
                     MMDB_strerror(mmdb_status));
        return CH_ERROR_PARSE;
    }
    if (!lookup.found_entry) return CH_OK;
    result = ch_geo_entry_string(&lookup.entry, "country", "names", "en",
                                 &out_location->country, error);
    if (result == CH_OK) {
        result = ch_geo_entry_string(&lookup.entry, "country", "iso_code",
                                     NULL, &out_location->country_code,
                                     error);
    }
    if (result == CH_OK) {
        result = ch_geo_entry_string(&lookup.entry, "city", "names", "en",
                                     &out_location->city, error);
    }
    if (result == CH_OK) {
        result = ch_geo_entry_number(&lookup.entry, "latitude",
                                     &out_location->latitude, error);
    }
    if (result == CH_OK) {
        result = ch_geo_entry_number(&lookup.entry, "longitude",
                                     &out_location->longitude, error);
    }
    if (result != CH_OK) ch_geo_location_clear(out_location);
    return result;
}
