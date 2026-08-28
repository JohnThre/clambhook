#ifndef CLAMBHOOK_LINUX_GTK_MODEL_H
#define CLAMBHOOK_LINUX_GTK_MODEL_H

#include <glib.h>

typedef struct ch_gtk_row {
    char *title;
    char *detail;
    char *identifier;
    char *selected;
    GPtrArray *options;
    gboolean selectable;
} ch_gtk_row;

typedef struct ch_gtk_status_model {
    gboolean running;
    char *profile;
    char *mode;
    GPtrArray *listeners;
} ch_gtk_status_model;

typedef struct ch_gtk_profiles_model {
    char *active;
    GPtrArray *names;
} ch_gtk_profiles_model;

typedef struct ch_gtk_traffic_model {
    guint64 active_connections;
    double rx_bps;
    double tx_bps;
    guint64 rx_total;
    guint64 tx_total;
    GPtrArray *rows;
} ch_gtk_traffic_model;

typedef struct ch_gtk_capture_detail {
    char *identifier;
    char *method;
    char *url;
    char *host;
    char *profile;
    char *chain;
    char *started_at;
    char *finished_at;
    char *error_message;
    gint status;
    char *request_headers;
    char *request_body;
    char *response_headers;
    char *response_body;
} ch_gtk_capture_detail;

typedef struct ch_gtk_conditioner_model {
    char *profile;
    gboolean enabled;
    guint64 download_kbps;
    guint64 upload_kbps;
    char *latency;
    char *jitter;
    double loss_percent;
} ch_gtk_conditioner_model;

typedef struct ch_gtk_dns_model {
    char *profile;
    gboolean enabled;
    char *timeout;
    char *upstreams_json;
} ch_gtk_dns_model;

typedef struct ch_gtk_curl_import {
    char *method;
    char *url;
    char *headers;
    char *body;
} ch_gtk_curl_import;

typedef struct ch_gtk_license_state {
    char *install_id;
    char *email;
    char *snapshot_json;
    char *grant_json;
    char *device_state_json;
} ch_gtk_license_state;

typedef struct ch_gtk_license_view {
    gboolean can_use_app;
    gboolean current_device_active;
    char *title;
    char *detail;
    char *current_device_id;
    guint active_devices;
    guint max_active_devices;
    GPtrArray *devices;
} ch_gtk_license_view;

typedef enum ch_gtk_page_model_kind {
    CH_GTK_PAGE_SERVERS,
    CH_GTK_PAGE_RULES,
    CH_GTK_PAGE_POLICIES,
    CH_GTK_PAGE_PROMPTS,
    CH_GTK_PAGE_SILENT_DECISIONS,
    CH_GTK_PAGE_DNS,
    CH_GTK_PAGE_CAPTURES,
    CH_GTK_PAGE_CONDITIONER
} ch_gtk_page_model_kind;

void ch_gtk_row_free(ch_gtk_row *row);
void ch_gtk_status_model_clear(ch_gtk_status_model *model);
void ch_gtk_profiles_model_clear(ch_gtk_profiles_model *model);
void ch_gtk_traffic_model_clear(ch_gtk_traffic_model *model);
void ch_gtk_capture_detail_clear(ch_gtk_capture_detail *detail);
void ch_gtk_conditioner_model_clear(ch_gtk_conditioner_model *model);
void ch_gtk_dns_model_clear(ch_gtk_dns_model *model);
void ch_gtk_curl_import_clear(ch_gtk_curl_import *model);
void ch_gtk_license_state_clear(ch_gtk_license_state *state);
void ch_gtk_license_view_clear(ch_gtk_license_view *view);

gboolean ch_gtk_parse_status(const guint8 *data, gsize length,
                             ch_gtk_status_model *out, GError **error);
gboolean ch_gtk_parse_profiles(const guint8 *data, gsize length,
                               ch_gtk_profiles_model *out, GError **error);
gboolean ch_gtk_parse_traffic(const guint8 *data, gsize length,
                              ch_gtk_traffic_model *out, GError **error);
gboolean ch_gtk_parse_page_rows(ch_gtk_page_model_kind kind,
                                const guint8 *data, gsize length,
                                GPtrArray **out_rows, char **out_summary,
                                GError **error);
gboolean ch_gtk_parse_capture_detail(const guint8 *data, gsize length,
                                     ch_gtk_capture_detail *out,
                                     GError **error);
char *ch_gtk_parse_curl_export(const guint8 *data, gsize length,
                               GError **error);
gboolean ch_gtk_parse_conditioner(const guint8 *data, gsize length,
                                  ch_gtk_conditioner_model *out,
                                  GError **error);
gboolean ch_gtk_parse_dns(const guint8 *data, gsize length,
                          ch_gtk_dns_model *out, GError **error);
gboolean ch_gtk_parse_curl_import(const guint8 *data, gsize length,
                                  ch_gtk_curl_import *out,
                                  GError **error);
gboolean ch_gtk_parse_license_state(const guint8 *data, gsize length,
                                    ch_gtk_license_state *out,
                                    GError **error);
gboolean ch_gtk_license_state_apply(const guint8 *data, gsize length,
                                    ch_gtk_license_state *state,
                                    GError **error);
gboolean ch_gtk_parse_license_view(const char *status_json,
                                   const char *device_state_json,
                                   ch_gtk_license_view *out,
                                   GError **error);

char *ch_gtk_format_bytes(guint64 value);
char *ch_gtk_format_rate(double value);
char *ch_gtk_profile_body(const char *name);
char *ch_gtk_policy_selection_body(const char *group, const char *chain);
char *ch_gtk_prompt_resolution_body(const char *action, const char *scope,
                                    gboolean match_host,
                                    gboolean match_port,
                                    gboolean match_protocol);
char *ch_gtk_silent_promotion_body(const char *scope,
                                   gboolean match_host,
                                   gboolean match_port,
                                   gboolean match_protocol);
char *ch_gtk_capture_enabled_body(gboolean enabled);
char *ch_gtk_prompt_resolution_path(const char *identifier);
char *ch_gtk_silent_promotion_path(const char *identifier);
char *ch_gtk_capture_entries_path(const char *query, const char *method,
                                  gboolean error_only, guint limit);
char *ch_gtk_capture_detail_path(const char *identifier);
char *ch_gtk_capture_curl_path(const char *identifier);
char *ch_gtk_conditioner_body(const char *profile, gboolean enabled,
                              const char *download_kbps,
                              const char *upload_kbps,
                              const char *latency, const char *jitter,
                              const char *loss_percent, GError **error);
char *ch_gtk_dns_body(const char *profile, gboolean enabled,
                      const char *timeout, const char *upstreams_json,
                      GError **error);
char *ch_gtk_curl_import_body(const char *command);
char *ch_gtk_composed_request_body(const char *method, const char *url,
                                   const char *headers, const char *body,
                                   GError **error);
char *ch_gtk_repeat_request_body(const char *identifier);
char *ch_gtk_rule_create_body(const char *name, const char *action,
                              const char *domains,
                              const char *domain_suffixes,
                              const char *domain_keywords,
                              const char *cidrs, const char *ports,
                              const char *networks, gboolean prepend,
                              GError **error);
char *ch_gtk_license_state_json(const ch_gtk_license_state *state);
char *ch_gtk_license_registration_body(const char *install_id,
                                       const char *display_name,
                                       const char *architecture,
                                       const char *app_version);

#endif
