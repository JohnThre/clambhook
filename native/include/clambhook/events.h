// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

#ifndef CLAMBHOOK_EVENTS_H
#define CLAMBHOOK_EVENTS_H

#include <stdbool.h>
#include <stddef.h>
#include <stdint.h>

#include "clambhook/error.h"

#ifdef __cplusplus
extern "C" {
#endif

typedef struct ch_event {
    /* Process-local insertion order used by live subscribers. */
    uint64_t sequence;
    uint64_t shard_id;
    uint64_t lamport;
    int64_t timestamp_ns;
    char *type;
    char *data_json;
} ch_event;

typedef struct ch_event_ring ch_event_ring;

ch_event_ring *ch_event_ring_create(size_t capacity, ch_error *error);
void ch_event_ring_destroy(ch_event_ring *ring);
ch_status ch_event_ring_append(ch_event_ring *ring, const ch_event *event, ch_error *error);

/* Returns an owned chronological snapshot containing Lamport values > since. */
ch_status ch_event_ring_since(
    ch_event_ring *ring,
    uint64_t since,
    ch_event **events,
    size_t *event_count,
    bool *complete,
    ch_error *error
);

/* Returns an owned chronological copy of the ring's retained events. */
ch_status ch_event_ring_snapshot(ch_event_ring *ring,
                                 ch_event **events,
                                 size_t *event_count,
                                 ch_error *error);

uint64_t ch_event_ring_oldest_lamport(ch_event_ring *ring);
size_t ch_event_ring_length(ch_event_ring *ring);
uint64_t ch_event_ring_total(ch_event_ring *ring);
void ch_events_free(ch_event *events, size_t event_count);

#ifdef __cplusplus
}
#endif

#endif
