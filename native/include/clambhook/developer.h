#ifndef CLAMBHOOK_DEVELOPER_H
#define CLAMBHOOK_DEVELOPER_H

#include <stddef.h>
#include <stdint.h>

#include "clambhook/config.h"
#include "clambhook/error.h"

#ifdef __cplusplus
extern "C" {
#endif

typedef struct ch_developer_manager ch_developer_manager;
typedef struct ch_developer_capture ch_developer_capture;

typedef struct ch_developer_capture_metadata {
    uint64_t flow_id;
    const char *profile;
    const char *client_address;
    const char *chain_name;
    const char *method;
    const char *url;
    const char *scheme;
    const char *host;
    const char *request_headers;
    size_t request_headers_length;
} ch_developer_capture_metadata;

ch_developer_manager *ch_developer_manager_create(ch_error *error);
void ch_developer_manager_destroy(ch_developer_manager *manager);

/* Applies the validated root developer table. Disabled configurations clear
 * the in-memory capture ring, matching the legacy manager lifecycle. */
ch_status ch_developer_manager_configure(ch_developer_manager *manager,
                                         const ch_config *config,
                                         ch_error *error);

ch_developer_capture *ch_developer_capture_begin(
    ch_developer_manager *manager,
    const ch_developer_capture_metadata *metadata,
    ch_error *error
);
void ch_developer_capture_request_body(ch_developer_capture *capture,
                                       const uint8_t *bytes, size_t length);
void ch_developer_capture_response(ch_developer_capture *capture,
                                   const uint8_t *bytes, size_t length);
void ch_developer_capture_finish(ch_developer_capture *capture,
                                 const char *error_message);

char *ch_developer_status_json(ch_developer_manager *manager,
                               ch_error *error);
char *ch_developer_entries_json(ch_developer_manager *manager,
                                const char *request_json,
                                ch_error *error);
char *ch_developer_entry_json(ch_developer_manager *manager,
                              const char *identifier,
                              ch_error *error);
char *ch_developer_entry_curl_json(ch_developer_manager *manager,
                                   const char *identifier,
                                   ch_error *error);
char *ch_developer_har_json(ch_developer_manager *manager,
                            ch_error *error);
char *ch_developer_clear_json(ch_developer_manager *manager,
                              ch_error *error);
/* Sends an independently composed public HTTP(S) request and stores the
 * bounded, redacted response as a developer entry. */
char *ch_developer_send_json(ch_developer_manager *manager,
                             const char *request_json,
                             ch_error *error);
/* Repeats a stored request, applying optional method, URL, header, and body
 * overrides. Redacted headers are never replayed. */
char *ch_developer_repeat_json(ch_developer_manager *manager,
                               const char *request_json,
                               ch_error *error);

#ifdef __cplusplus
}
#endif

#endif
