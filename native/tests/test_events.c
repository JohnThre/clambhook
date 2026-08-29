// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

#include "test.h"

#include <stdbool.h>
#include <stdint.h>
#include <stdlib.h>

#include "clambhook/events.h"

static ch_event test_event(uint64_t lamport) {
    ch_event event = {
        .shard_id = 1U,
        .lamport = lamport,
        .timestamp_ns = (int64_t)lamport,
        .type = "test",
        .data_json = "{}"
    };
    return event;
}

void ch_test_events(void) {
    ch_error error;
    ch_event_ring *ring = ch_event_ring_create(3U, &error);
    CH_TEST_ASSERT(ring != NULL);
    for (uint64_t lamport = 1U; lamport <= 5U; ++lamport) {
        ch_event event = test_event(lamport);
        CH_TEST_ASSERT(ch_event_ring_append(ring, &event, &error) == CH_OK);
    }
    CH_TEST_ASSERT(ch_event_ring_length(ring) == 3U);

    ch_event *snapshot = NULL;
    size_t snapshot_count = 0U;
    CH_TEST_ASSERT(ch_event_ring_snapshot(
        ring, &snapshot, &snapshot_count, &error) == CH_OK);
    CH_TEST_ASSERT(snapshot_count == 3U);
    CH_TEST_ASSERT(snapshot[0].lamport == 3U);
    CH_TEST_ASSERT(snapshot[2].lamport == 5U);
    CH_TEST_ASSERT(snapshot[0].sequence == 3U);
    CH_TEST_ASSERT(ch_event_ring_total(ring) == 5U);
    ch_events_free(snapshot, snapshot_count);
    CH_TEST_ASSERT(ch_event_ring_oldest_lamport(ring) == 3U);

    char *json = ch_event_ring_query_json(
        ring, "{\"after_sequence\":2,\"limit\":2}", &error);
    CH_TEST_ASSERT(json != NULL);
    CH_TEST_ASSERT(strstr(json, "\"sequence\":3") != NULL);
    CH_TEST_ASSERT(strstr(json, "\"sequence\":4") != NULL);
    CH_TEST_ASSERT(strstr(json, "\"sequence\":5") == NULL);
    CH_TEST_ASSERT(strstr(json, "\"complete\":false") != NULL);
    CH_TEST_ASSERT(strstr(json, "\"next_sequence\":4") != NULL);
    free(json);

    json = ch_event_ring_query_json(
        ring,
        "{\"after_sequence\":4,\"limit\":2,\"types\":[\"te*\"]}",
        &error);
    CH_TEST_ASSERT(json != NULL);
    CH_TEST_ASSERT(strstr(json, "\"sequence\":5") != NULL);
    CH_TEST_ASSERT(strstr(json, "\"complete\":true") != NULL);
    CH_TEST_ASSERT(strstr(json, "\"next_sequence\":5") != NULL);
    free(json);

    CH_TEST_ASSERT(ch_event_ring_query_json(
        ring, "{\"after_sequence\":0,\"limit\":0}", &error) == NULL);
    CH_TEST_ASSERT(error.code == CH_ERROR_INVALID_ARGUMENT);

    ch_event *events = NULL;
    size_t count = 0U;
    bool complete = false;
    CH_TEST_ASSERT(ch_event_ring_since(ring, 2U, &events, &count, &complete, &error) == CH_OK);
    CH_TEST_ASSERT(complete);
    CH_TEST_ASSERT(count == 3U);
    CH_TEST_ASSERT(events[0].lamport == 3U && events[2].lamport == 5U);
    CH_TEST_ASSERT_STRING("test", events[0].type);
    ch_events_free(events, count);

    CH_TEST_ASSERT(ch_event_ring_since(ring, 1U, &events, &count, &complete, &error) == CH_OK);
    CH_TEST_ASSERT(!complete);
    ch_events_free(events, count);

    CH_TEST_ASSERT(
        ch_event_ring_since(ring, UINT64_MAX, &events, &count, &complete, &error) == CH_OK
    );
    CH_TEST_ASSERT(complete && count == 0U && events == NULL);
    ch_event_ring_destroy(ring);

    CH_TEST_ASSERT(ch_event_ring_create(0U, &error) == NULL);
    CH_TEST_ASSERT(error.code == CH_ERROR_INVALID_ARGUMENT);
}
