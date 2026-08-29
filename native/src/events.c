// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

#include "clambhook/events.h"

#include <inttypes.h>
#include <pthread.h>
#include <stdint.h>
#include <stdlib.h>
#include <string.h>

#include "clambhook/json.h"
#include "internal.h"

struct ch_event_ring {
    pthread_mutex_t mutex;
    ch_event *events;
    size_t head;
    size_t size;
    size_t capacity;
    uint64_t total;
};

static void ch_event_clear(ch_event *event) {
    if (event == NULL) {
        return;
    }
    free(event->type);
    free(event->data_json);
    memset(event, 0, sizeof(*event));
}

static int ch_event_copy(ch_event *destination, const ch_event *source) {
    memset(destination, 0, sizeof(*destination));
    destination->sequence = source->sequence;
    destination->shard_id = source->shard_id;
    destination->lamport = source->lamport;
    destination->timestamp_ns = source->timestamp_ns;
    destination->type = ch_strdup(source->type == NULL ? "" : source->type);
    destination->data_json = ch_strdup(source->data_json == NULL ? "null" : source->data_json);
    if (destination->type == NULL || destination->data_json == NULL) {
        ch_event_clear(destination);
        return 0;
    }
    return 1;
}

ch_event_ring *ch_event_ring_create(size_t capacity, ch_error *error) {
    ch_error_clear(error);
    if (capacity == 0U || capacity > SIZE_MAX / sizeof(ch_event)) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT, "event ring capacity must be greater than zero");
        return NULL;
    }
    ch_event_ring *ring = calloc(1U, sizeof(*ring));
    if (ring == NULL) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY, "allocate event ring");
        return NULL;
    }
    ring->events = calloc(capacity, sizeof(*ring->events));
    if (ring->events == NULL) {
        free(ring);
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY, "allocate event ring storage");
        return NULL;
    }
    ring->capacity = capacity;
    if (pthread_mutex_init(&ring->mutex, NULL) != 0) {
        free(ring->events);
        free(ring);
        ch_error_set(error, CH_ERROR_INTERNAL, "initialize event ring mutex");
        return NULL;
    }
    return ring;
}

void ch_event_ring_destroy(ch_event_ring *ring) {
    if (ring == NULL) {
        return;
    }
    pthread_mutex_lock(&ring->mutex);
    for (size_t index = 0U; index < ring->capacity; ++index) {
        ch_event_clear(&ring->events[index]);
    }
    free(ring->events);
    ring->events = NULL;
    pthread_mutex_unlock(&ring->mutex);
    pthread_mutex_destroy(&ring->mutex);
    free(ring);
}

ch_status ch_event_ring_append(ch_event_ring *ring, const ch_event *event, ch_error *error) {
    ch_error_clear(error);
    if (ring == NULL || event == NULL) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT, "event ring and event are required");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    ch_event copy;
    if (!ch_event_copy(&copy, event)) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY, "copy event");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    pthread_mutex_lock(&ring->mutex);
    ++ring->total;
    copy.sequence = ring->total;
    ch_event_clear(&ring->events[ring->head]);
    ring->events[ring->head] = copy;
    ring->head = (ring->head + 1U) % ring->capacity;
    if (ring->size < ring->capacity) {
        ++ring->size;
    }
    pthread_mutex_unlock(&ring->mutex);
    return CH_OK;
}

ch_status ch_event_ring_since(
    ch_event_ring *ring,
    uint64_t since,
    ch_event **events,
    size_t *event_count,
    bool *complete,
    ch_error *error
) {
    ch_error_clear(error);
    if (ring == NULL || events == NULL || event_count == NULL || complete == NULL) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT, "ring and output pointers are required");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    *events = NULL;
    *event_count = 0U;
    *complete = true;

    pthread_mutex_lock(&ring->mutex);
    if (ring->size == 0U) {
        pthread_mutex_unlock(&ring->mutex);
        return CH_OK;
    }
    size_t oldest_index = (ring->head + ring->capacity - ring->size) % ring->capacity;
    uint64_t oldest = ring->events[oldest_index].lamport;
    *complete = since == UINT64_MAX || oldest <= since + 1U;

    size_t count = 0U;
    for (size_t offset = 0U; offset < ring->size; ++offset) {
        size_t index = (oldest_index + offset) % ring->capacity;
        if (ring->events[index].lamport > since) {
            ++count;
        }
    }
    if (count == 0U) {
        pthread_mutex_unlock(&ring->mutex);
        return CH_OK;
    }
    ch_event *snapshot = calloc(count, sizeof(*snapshot));
    if (snapshot == NULL) {
        pthread_mutex_unlock(&ring->mutex);
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY, "allocate event replay");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    size_t written = 0U;
    for (size_t offset = 0U; offset < ring->size; ++offset) {
        size_t index = (oldest_index + offset) % ring->capacity;
        if (ring->events[index].lamport <= since) {
            continue;
        }
        if (!ch_event_copy(&snapshot[written], &ring->events[index])) {
            pthread_mutex_unlock(&ring->mutex);
            ch_events_free(snapshot, written);
            ch_error_set(error, CH_ERROR_OUT_OF_MEMORY, "copy event replay");
            return CH_ERROR_OUT_OF_MEMORY;
        }
        ++written;
    }
    pthread_mutex_unlock(&ring->mutex);
    *events = snapshot;
    *event_count = written;
    return CH_OK;
}

ch_status ch_event_ring_snapshot(ch_event_ring *ring,
                                 ch_event **events,
                                 size_t *event_count,
                                 ch_error *error) {
    ch_error_clear(error);
    if (ring == NULL || events == NULL || event_count == NULL) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "ring and output pointers are required");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    *events = NULL;
    *event_count = 0U;
    pthread_mutex_lock(&ring->mutex);
    if (ring->size == 0U) {
        pthread_mutex_unlock(&ring->mutex);
        return CH_OK;
    }
    ch_event *snapshot = calloc(ring->size, sizeof(*snapshot));
    if (snapshot == NULL) {
        pthread_mutex_unlock(&ring->mutex);
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "allocate event snapshot");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    size_t oldest = (ring->head + ring->capacity - ring->size) %
        ring->capacity;
    for (size_t offset = 0U; offset < ring->size; ++offset) {
        size_t index = (oldest + offset) % ring->capacity;
        if (!ch_event_copy(&snapshot[offset], &ring->events[index])) {
            pthread_mutex_unlock(&ring->mutex);
            ch_events_free(snapshot, offset);
            ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                         "copy event snapshot");
            return CH_ERROR_OUT_OF_MEMORY;
        }
    }
    *events = snapshot;
    *event_count = ring->size;
    pthread_mutex_unlock(&ring->mutex);
    return CH_OK;
}

uint64_t ch_event_ring_oldest_lamport(ch_event_ring *ring) {
    if (ring == NULL) {
        return 0U;
    }
    pthread_mutex_lock(&ring->mutex);
    uint64_t oldest = 0U;
    if (ring->size > 0U) {
        size_t index = (ring->head + ring->capacity - ring->size) % ring->capacity;
        oldest = ring->events[index].lamport;
    }
    pthread_mutex_unlock(&ring->mutex);
    return oldest;
}

size_t ch_event_ring_length(ch_event_ring *ring) {
    if (ring == NULL) {
        return 0U;
    }
    pthread_mutex_lock(&ring->mutex);
    size_t length = ring->size;
    pthread_mutex_unlock(&ring->mutex);
    return length;
}

uint64_t ch_event_ring_total(ch_event_ring *ring) {
    if (ring == NULL) return 0U;
    pthread_mutex_lock(&ring->mutex);
    uint64_t total = ring->total;
    pthread_mutex_unlock(&ring->mutex);
    return total;
}

static int event_type_matches(const ch_json_value *types,
                              const char *event_type) {
    if (types == NULL || ch_json_array_size(types) == 0U) return 1;
    size_t count = ch_json_array_size(types);
    for (size_t index = 0U; index < count; ++index) {
        const char *pattern = ch_json_string_value(
            ch_json_array_get(types, index));
        if (pattern == NULL) return 0;
        size_t length = strlen(pattern);
        if (length > 0U && pattern[length - 1U] == '*') {
            if (strncmp(event_type, pattern, length - 1U) == 0) return 1;
        } else if (strcmp(event_type, pattern) == 0) {
            return 1;
        }
    }
    return 0;
}

static int event_connection_matches(const ch_json_value *conn_ids,
                                    const char *data_json) {
    if (conn_ids == NULL || ch_json_array_size(conn_ids) == 0U) return 1;
    ch_error ignored;
    const char *document = data_json == NULL ? "{}" : data_json;
    ch_json_value *data = ch_json_parse(document, strlen(document), &ignored);
    const char *conn_id = data == NULL ? NULL : ch_json_string_value(
        ch_json_object_get(data, "conn_id"));
    int matches = 0;
    size_t count = ch_json_array_size(conn_ids);
    for (size_t index = 0U; conn_id != NULL && index < count; ++index) {
        const char *candidate = ch_json_string_value(
            ch_json_array_get(conn_ids, index));
        if (candidate != NULL && strcmp(candidate, conn_id) == 0) {
            matches = 1;
            break;
        }
    }
    ch_json_value_destroy(data);
    return matches;
}

static int event_cursor(const ch_json_value *cursors, uint64_t shard_id,
                        uint64_t *out_lamport) {
    size_t count = ch_json_array_size(cursors);
    for (size_t index = 0U; index < count; ++index) {
        const ch_json_value *cursor = ch_json_array_get(cursors, index);
        int64_t shard = -1;
        int64_t lamport = -1;
        if (cursor == NULL ||
            !ch_json_int64_value(ch_json_object_get(cursor, "shard_id"),
                                 &shard) ||
            !ch_json_int64_value(ch_json_object_get(cursor, "lamport"),
                                 &lamport) ||
            shard < 0 || lamport < 0) {
            continue;
        }
        if ((uint64_t)shard == shard_id) {
            *out_lamport = (uint64_t)lamport;
            return 1;
        }
    }
    return 0;
}

char *ch_event_ring_query_json(ch_event_ring *ring,
                               const char *request_json,
                               ch_error *error) {
    ch_error_clear(error);
    if (ring == NULL) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "event ring is required");
        return NULL;
    }
    const char *document = request_json == NULL || request_json[0] == '\0' ?
        "{}" : request_json;
    ch_json_value *request = ch_json_parse(document, strlen(document), error);
    if (request == NULL) return NULL;
    if (ch_json_value_type(request) != CH_JSON_OBJECT) {
        ch_json_value_destroy(request);
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "event request must be a JSON object");
        return NULL;
    }
    const ch_json_value *types = ch_json_object_get(request, "types");
    const ch_json_value *conn_ids = ch_json_object_get(request, "conn_ids");
    const ch_json_value *cursors = ch_json_object_get(request, "since");
    if ((types != NULL && ch_json_value_type(types) != CH_JSON_ARRAY) ||
        (conn_ids != NULL && ch_json_value_type(conn_ids) != CH_JSON_ARRAY) ||
        (cursors != NULL && ch_json_value_type(cursors) != CH_JSON_ARRAY)) {
        ch_json_value_destroy(request);
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "event filters must be arrays");
        return NULL;
    }
    int64_t after_signed = -1;
    const ch_json_value *after_value = ch_json_object_get(
        request, "after_sequence");
    int has_after = after_value != NULL &&
        ch_json_int64_value(after_value, &after_signed) && after_signed >= 0;
    if (after_value != NULL && !has_after) {
        ch_json_value_destroy(request);
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "after_sequence must be a non-negative integer");
        return NULL;
    }
    int64_t limit_signed = 256;
    const ch_json_value *limit_value = ch_json_object_get(request, "limit");
    if (limit_value != NULL &&
        (!ch_json_int64_value(limit_value, &limit_signed) ||
         limit_signed <= 0 || limit_signed > 4096)) {
        ch_json_value_destroy(request);
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "event limit must be between 1 and 4096");
        return NULL;
    }
    size_t limit = (size_t)limit_signed;
    uint64_t after = has_after ? (uint64_t)after_signed : 0U;
    ch_event *events = NULL;
    size_t count = 0U;
    if (ch_event_ring_snapshot(ring, &events, &count, error) != CH_OK) {
        ch_json_value_destroy(request);
        return NULL;
    }
    uint64_t next_sequence = ch_event_ring_total(ring);
    int complete = 1;
    if (has_after && count > 0U && events[0].sequence > after + 1U) {
        complete = 0;
    }
    size_t cursor_count = ch_json_array_size(cursors);
    for (size_t cursor_index = 0U; cursor_index < cursor_count;
         ++cursor_index) {
        const ch_json_value *cursor = ch_json_array_get(cursors,
                                                        cursor_index);
        int64_t shard = -1;
        int64_t lamport = -1;
        if (cursor == NULL ||
            !ch_json_int64_value(ch_json_object_get(cursor, "shard_id"),
                                 &shard) ||
            !ch_json_int64_value(ch_json_object_get(cursor, "lamport"),
                                 &lamport) || shard < 0 || lamport < 0) {
            complete = 0;
            continue;
        }
        uint64_t oldest = UINT64_MAX;
        for (size_t event_index = 0U; event_index < count; ++event_index) {
            if (events[event_index].shard_id == (uint64_t)shard &&
                events[event_index].lamport < oldest) {
                oldest = events[event_index].lamport;
            }
        }
        if (oldest == UINT64_MAX ||
            (oldest > 0U && oldest > (uint64_t)lamport + 1U)) {
            complete = 0;
        }
    }
    ch_json_buffer json;
    ch_json_init(&json);
    int okay = ch_json_append(&json, "{\"events\":[");
    size_t emitted = 0U;
    uint64_t response_sequence = next_sequence;
    uint64_t last_scanned_sequence = after;
    for (size_t index = 0U; okay && index < count; ++index) {
        const ch_event *event = &events[index];
        uint64_t cursor = 0U;
        int has_cursor = event_cursor(cursors, event->shard_id, &cursor);
        int eligible = (has_after && event->sequence > after) ||
            (has_cursor && event->lamport > cursor);
        if (!eligible) {
            continue;
        }
        int matches = event_type_matches(types, event->type) &&
            event_connection_matches(conn_ids, event->data_json);
        if (!matches) {
            if (has_after) last_scanned_sequence = event->sequence;
            continue;
        }
        if (emitted == limit) {
            complete = 0;
            response_sequence = last_scanned_sequence;
            break;
        }
        if (emitted > 0U) okay = ch_json_append(&json, ",");
        if (okay) {
            okay = ch_json_append_format(
                &json,
                "{\"sequence\":%" PRIu64 ",\"shard_id\":%" PRIu64
                ",\"lamport\":%" PRIu64 ",\"ts_ns\":%" PRId64
                ",\"type\":",
                event->sequence, event->shard_id, event->lamport,
                event->timestamp_ns) &&
                ch_json_append_string(&json, event->type) &&
                ch_json_append(&json, ",\"data\":") &&
                ch_json_append(&json, event->data_json) &&
                ch_json_append(&json, "}");
        }
        if (has_after) last_scanned_sequence = event->sequence;
        ++emitted;
    }
    if (okay) {
        okay = ch_json_append_format(
            &json, "],\"complete\":%s,\"next_sequence\":%" PRIu64 "}",
            complete ? "true" : "false", response_sequence);
    }
    ch_events_free(events, count);
    ch_json_value_destroy(request);
    char *result = okay ? ch_json_take(&json) : NULL;
    ch_json_dispose(&json);
    if (result == NULL) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "encode event replay");
    }
    return result;
}

void ch_events_free(ch_event *events, size_t event_count) {
    if (events == NULL) {
        return;
    }
    for (size_t index = 0U; index < event_count; ++index) {
        ch_event_clear(&events[index]);
    }
    free(events);
}
