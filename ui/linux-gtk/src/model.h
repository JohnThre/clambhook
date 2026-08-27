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

typedef enum ch_gtk_page_model_kind {
    CH_GTK_PAGE_SERVERS,
    CH_GTK_PAGE_POLICIES,
    CH_GTK_PAGE_PROMPTS,
    CH_GTK_PAGE_DNS,
    CH_GTK_PAGE_CAPTURES,
    CH_GTK_PAGE_CONDITIONER
} ch_gtk_page_model_kind;

void ch_gtk_row_free(ch_gtk_row *row);
void ch_gtk_status_model_clear(ch_gtk_status_model *model);
void ch_gtk_profiles_model_clear(ch_gtk_profiles_model *model);
void ch_gtk_traffic_model_clear(ch_gtk_traffic_model *model);

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

char *ch_gtk_format_bytes(guint64 value);
char *ch_gtk_format_rate(double value);
char *ch_gtk_profile_body(const char *name);
char *ch_gtk_policy_selection_body(const char *group, const char *chain);
char *ch_gtk_prompt_resolution_body(const char *action, const char *scope,
                                    gboolean match_host,
                                    gboolean match_port,
                                    gboolean match_protocol);
char *ch_gtk_capture_enabled_body(gboolean enabled);
char *ch_gtk_prompt_resolution_path(const char *identifier);

#endif
