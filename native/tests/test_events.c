#include "test.h"

#include <stdbool.h>
#include <stdint.h>

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
    CH_TEST_ASSERT(ch_event_ring_oldest_lamport(ring) == 3U);

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
