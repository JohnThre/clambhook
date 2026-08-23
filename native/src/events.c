#include "clambhook/events.h"

#include <stdint.h>
#include <stdlib.h>
#include <string.h>

#include <uv.h>

#include "internal.h"

struct ch_event_ring {
    uv_mutex_t mutex;
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
    if (uv_mutex_init(&ring->mutex) != 0) {
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
    uv_mutex_lock(&ring->mutex);
    for (size_t index = 0U; index < ring->capacity; ++index) {
        ch_event_clear(&ring->events[index]);
    }
    free(ring->events);
    ring->events = NULL;
    uv_mutex_unlock(&ring->mutex);
    uv_mutex_destroy(&ring->mutex);
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
    uv_mutex_lock(&ring->mutex);
    ch_event_clear(&ring->events[ring->head]);
    ring->events[ring->head] = copy;
    ring->head = (ring->head + 1U) % ring->capacity;
    if (ring->size < ring->capacity) {
        ++ring->size;
    }
    ++ring->total;
    uv_mutex_unlock(&ring->mutex);
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

    uv_mutex_lock(&ring->mutex);
    if (ring->size == 0U) {
        uv_mutex_unlock(&ring->mutex);
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
        uv_mutex_unlock(&ring->mutex);
        return CH_OK;
    }
    ch_event *snapshot = calloc(count, sizeof(*snapshot));
    if (snapshot == NULL) {
        uv_mutex_unlock(&ring->mutex);
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
            uv_mutex_unlock(&ring->mutex);
            ch_events_free(snapshot, written);
            ch_error_set(error, CH_ERROR_OUT_OF_MEMORY, "copy event replay");
            return CH_ERROR_OUT_OF_MEMORY;
        }
        ++written;
    }
    uv_mutex_unlock(&ring->mutex);
    *events = snapshot;
    *event_count = written;
    return CH_OK;
}

uint64_t ch_event_ring_oldest_lamport(ch_event_ring *ring) {
    if (ring == NULL) {
        return 0U;
    }
    uv_mutex_lock(&ring->mutex);
    uint64_t oldest = 0U;
    if (ring->size > 0U) {
        size_t index = (ring->head + ring->capacity - ring->size) % ring->capacity;
        oldest = ring->events[index].lamport;
    }
    uv_mutex_unlock(&ring->mutex);
    return oldest;
}

size_t ch_event_ring_length(ch_event_ring *ring) {
    if (ring == NULL) {
        return 0U;
    }
    uv_mutex_lock(&ring->mutex);
    size_t length = ring->size;
    uv_mutex_unlock(&ring->mutex);
    return length;
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
