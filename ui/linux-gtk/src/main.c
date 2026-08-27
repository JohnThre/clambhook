#include <gtk/gtk.h>
#include <json-glib/json-glib.h>
#include <libsoup/soup.h>

#include <stdlib.h>
#include <string.h>

#include "model.h"

#ifndef CLAMBHOOK_VERSION
#define CLAMBHOOK_VERSION "dev"
#endif

typedef enum request_kind {
    REQUEST_STATUS,
    REQUEST_PROFILES,
    REQUEST_TRAFFIC,
    REQUEST_SERVERS,
    REQUEST_POLICIES,
    REQUEST_PROMPTS,
    REQUEST_SILENT_DECISIONS,
    REQUEST_DNS,
    REQUEST_CAPTURE_STATUS,
    REQUEST_CAPTURES,
    REQUEST_CONDITIONER,
    REQUEST_CONNECT,
    REQUEST_PROFILE,
    REQUEST_POLICY_TEST,
    REQUEST_POLICY_SELECT,
    REQUEST_PROMPT_RESOLVE,
    REQUEST_CAPTURE_TOGGLE,
    REQUEST_CAPTURE_CLEAR,
    REQUEST_CAPTURE_DETAIL,
    REQUEST_CAPTURE_CURL,
    REQUEST_CONDITIONER_UPDATE,
    REQUEST_DNS_UPDATE,
    REQUEST_SILENT_PROMOTE,
    REQUEST_CAPTURE_CURL_IMPORT,
    REQUEST_CAPTURE_HAR
} RequestKind;

typedef enum page_slot {
    PAGE_SERVERS,
    PAGE_POLICIES,
    PAGE_PROMPTS,
    PAGE_SILENT_DECISIONS,
    PAGE_DNS,
    PAGE_CAPTURES,
    PAGE_CONDITIONER,
    PAGE_SLOT_COUNT
} PageSlot;

typedef struct clambhook_linux_app {
    GtkApplication *application;
    GtkWidget *window;
    GtkWidget *status_label;
    GtkWidget *profile_label;
    GtkWidget *mode_label;
    GtkWidget *api_label;
    GtkWidget *connections_label;
    GtkWidget *bandwidth_label;
    GtkWidget *totals_label;
    GtkWidget *error_label;
    GtkWidget *connect_button;
    GtkWidget *refresh_button;
    GtkWidget *spinner;
    GtkWidget *profile_dropdown;
    GtkWidget *activity_list;
    GtkWidget *listener_list;
    GtkWidget *page_lists[PAGE_SLOT_COUNT];
    GtkWidget *page_summaries[PAGE_SLOT_COUNT];
    GtkWidget *capture_state_label;
    GtkWidget *capture_toggle_button;
    GtkWidget *capture_clear_button;
    GtkWidget *capture_query;
    GtkWidget *capture_method;
    GtkWidget *capture_error_only;
    GtkWidget *capture_filter_button;
    GtkWidget *capture_import_button;
    GtkWidget *capture_har_button;
    GtkWidget *conditioner_editor;
    GtkWidget *conditioner_profile_label;
    GtkWidget *conditioner_enabled;
    GtkWidget *conditioner_download;
    GtkWidget *conditioner_upload;
    GtkWidget *conditioner_latency;
    GtkWidget *conditioner_jitter;
    GtkWidget *conditioner_loss;
    GtkWidget *conditioner_save_button;
    GtkWidget *dns_editor;
    GtkWidget *dns_profile_label;
    GtkWidget *dns_enabled;
    GtkWidget *dns_timeout;
    GtkTextBuffer *dns_upstreams;
    GtkWidget *dns_save_button;
    GtkWidget *policy_test_button;
    GtkWidget *prompt_match_host;
    GtkWidget *prompt_match_port;
    GtkWidget *prompt_match_protocol;
    SoupSession *session;
    SoupWebsocketConnection *event_connection;
    char *api_url;
    char *api_token;
    gboolean running;
    gboolean capture_enabled;
    gboolean updating_profiles;
    guint active_requests;
    guint event_refresh_source;
    guint event_reconnect_source;
    gboolean event_connecting;
    gboolean shutting_down;
    char *conditioner_profile;
    char *dns_profile;
} ClambhookLinuxApp;

typedef struct event_connect_context {
    ClambhookLinuxApp *app;
    SoupMessage *message;
} EventConnectContext;

typedef struct request_context {
    ClambhookLinuxApp *app;
    SoupMessage *message;
    RequestKind kind;
    GFile *destination;
} RequestContext;

typedef struct policy_selection_context {
    ClambhookLinuxApp *app;
    char *group;
    char *selected;
} PolicySelectionContext;

typedef struct prompt_action_context {
    ClambhookLinuxApp *app;
    char *identifier;
    char *action;
    char *scope;
} PromptActionContext;

typedef struct silent_action_context {
    ClambhookLinuxApp *app;
    char *identifier;
    char *scope;
} SilentActionContext;

typedef struct har_write_context {
    ClambhookLinuxApp *app;
    GBytes *contents;
} HarWriteContext;

static void refresh_all(ClambhookLinuxApp *app);
static void send_request(ClambhookLinuxApp *app, RequestKind kind,
                         const char *method, const char *path,
                         const char *body);
static void send_request_to_file(ClambhookLinuxApp *app, RequestKind kind,
                                 const char *method, const char *path,
                                 GFile *destination);
static void populate_policy_rows(ClambhookLinuxApp *app, GPtrArray *rows);
static void populate_prompt_rows(ClambhookLinuxApp *app, GPtrArray *rows);
static void populate_silent_rows(ClambhookLinuxApp *app, GPtrArray *rows);
static void populate_capture_rows(ClambhookLinuxApp *app, GPtrArray *rows);
static void start_event_stream(ClambhookLinuxApp *app);

static char *join_url(const char *base, const char *path) {
    gsize base_length = strlen(base);
    gboolean slash = base_length > 0U && base[base_length - 1U] == '/';
    return g_strdup_printf("%s%s%s", base, slash ? "" : "/",
                           path[0] == '/' ? path + 1U : path);
}

static char *websocket_url(const char *base, const char *path) {
    const char *scheme = NULL;
    const char *rest = NULL;
    if (g_str_has_prefix(base, "http://")) {
        scheme = "ws://";
        rest = base + strlen("http://");
    } else if (g_str_has_prefix(base, "https://")) {
        scheme = "wss://";
        rest = base + strlen("https://");
    } else {
        return NULL;
    }
    g_autofree char *websocket_base = g_strdup_printf("%s%s", scheme, rest);
    return join_url(websocket_base, path);
}

static gboolean event_refresh_timeout(gpointer user_data) {
    ClambhookLinuxApp *app = user_data;
    app->event_refresh_source = 0U;
    if (app->shutting_down) return G_SOURCE_REMOVE;
    send_request(app, REQUEST_STATUS, "GET", "/api/v1/status", NULL);
    send_request(app, REQUEST_TRAFFIC, "GET",
                 "/api/v1/traffic?limit=200", NULL);
    return G_SOURCE_REMOVE;
}

static void event_message(SoupWebsocketConnection *connection, gint type,
                          GBytes *message, gpointer user_data) {
    (void)connection;
    (void)message;
    ClambhookLinuxApp *app = user_data;
    if (type != SOUP_WEBSOCKET_DATA_TEXT || app->shutting_down ||
        app->event_refresh_source != 0U) return;
    app->event_refresh_source = g_timeout_add(
        750U, event_refresh_timeout, app);
}

static gboolean event_reconnect_timeout(gpointer user_data) {
    ClambhookLinuxApp *app = user_data;
    app->event_reconnect_source = 0U;
    start_event_stream(app);
    return G_SOURCE_REMOVE;
}

static void schedule_event_reconnect(ClambhookLinuxApp *app) {
    if (app->shutting_down || app->event_reconnect_source != 0U) return;
    app->event_reconnect_source = g_timeout_add_seconds(
        2U, event_reconnect_timeout, app);
}

static void event_closed(SoupWebsocketConnection *connection,
                         gpointer user_data) {
    ClambhookLinuxApp *app = user_data;
    if (app->event_connection == connection) {
        g_clear_object(&app->event_connection);
    }
    schedule_event_reconnect(app);
}

static void event_error(SoupWebsocketConnection *connection, GError *error,
                        gpointer user_data) {
    (void)connection;
    (void)error;
    (void)user_data;
}

static void event_connected(GObject *source, GAsyncResult *result,
                            gpointer user_data) {
    EventConnectContext *context = user_data;
    ClambhookLinuxApp *app = context->app;
    g_autoptr(GError) error = NULL;
    SoupWebsocketConnection *connection =
        soup_session_websocket_connect_finish(
            SOUP_SESSION(source), result, &error);
    app->event_connecting = FALSE;
    if (!app->shutting_down && connection != NULL) {
        g_clear_object(&app->event_connection);
        app->event_connection = connection;
        soup_websocket_connection_set_max_incoming_payload_size(
            connection, 1024U * 1024U);
        soup_websocket_connection_set_keepalive_interval(connection, 30U);
        g_signal_connect(connection, "message", G_CALLBACK(event_message),
                         app);
        g_signal_connect(connection, "error", G_CALLBACK(event_error), app);
        g_signal_connect(connection, "closed", G_CALLBACK(event_closed), app);
    } else {
        g_clear_object(&connection);
        schedule_event_reconnect(app);
    }
    g_object_unref(context->message);
    g_free(context);
}

static void start_event_stream(ClambhookLinuxApp *app) {
    if (app->shutting_down || app->event_connecting ||
        app->event_connection != NULL) return;
    g_autofree char *url = websocket_url(
        app->api_url, "/api/v1/events?types=connection.*,rule.*");
    if (url == NULL) return;
    SoupMessage *message = soup_message_new("GET", url);
    if (message == NULL) {
        schedule_event_reconnect(app);
        return;
    }
    SoupMessageHeaders *headers = soup_message_get_request_headers(message);
    if (app->api_token[0] != '\0') {
        g_autofree char *authorization = g_strdup_printf(
            "Bearer %s", app->api_token);
        soup_message_headers_replace(headers, "Authorization", authorization);
    }
    EventConnectContext *context = g_new0(EventConnectContext, 1U);
    context->app = app;
    context->message = message;
    app->event_connecting = TRUE;
    soup_session_websocket_connect_async(
        app->session, message, NULL, NULL, G_PRIORITY_DEFAULT, NULL,
        event_connected, context);
}

static void set_request_activity(ClambhookLinuxApp *app, int delta) {
    if (delta > 0) {
        app->active_requests += (guint)delta;
    } else if (app->active_requests > 0U) {
        --app->active_requests;
    }
    gboolean active = app->active_requests > 0U;
    gtk_widget_set_sensitive(app->connect_button, !active);
    gtk_widget_set_sensitive(app->refresh_button, !active);
    gtk_widget_set_sensitive(app->profile_dropdown, !active);
    gtk_widget_set_sensitive(app->policy_test_button, !active);
    gtk_widget_set_sensitive(app->capture_toggle_button, !active);
    gtk_widget_set_sensitive(app->capture_clear_button,
                             !active && app->capture_enabled);
    gtk_widget_set_sensitive(app->capture_filter_button, !active);
    gtk_widget_set_sensitive(app->capture_import_button, !active);
    gtk_widget_set_sensitive(app->capture_har_button, !active);
    gtk_widget_set_sensitive(app->conditioner_editor, !active);
    gtk_widget_set_sensitive(app->conditioner_save_button, !active);
    gtk_widget_set_sensitive(app->dns_editor, !active);
    gtk_widget_set_sensitive(app->dns_save_button, !active);
    gtk_widget_set_sensitive(app->page_lists[PAGE_POLICIES], !active);
    gtk_widget_set_sensitive(app->page_lists[PAGE_PROMPTS], !active);
    gtk_widget_set_sensitive(app->page_lists[PAGE_SILENT_DECISIONS], !active);
    gtk_widget_set_sensitive(app->page_lists[PAGE_CAPTURES], !active);
    gtk_widget_set_visible(app->spinner, active);
    if (active) gtk_spinner_start(GTK_SPINNER(app->spinner));
    else gtk_spinner_stop(GTK_SPINNER(app->spinner));
}

static void clear_list(GtkWidget *list) {
    GtkWidget *child = gtk_widget_get_first_child(list);
    while (child != NULL) {
        GtkWidget *next = gtk_widget_get_next_sibling(child);
        gtk_list_box_remove(GTK_LIST_BOX(list), child);
        child = next;
    }
}

static GtkWidget *row_box(const char *title, const char *detail) {
    GtkWidget *box = gtk_box_new(GTK_ORIENTATION_VERTICAL, 3);
    gtk_widget_set_margin_top(box, 10);
    gtk_widget_set_margin_bottom(box, 10);
    gtk_widget_set_margin_start(box, 12);
    gtk_widget_set_margin_end(box, 12);
    GtkWidget *title_label = gtk_label_new(
        title == NULL || title[0] == '\0' ? "—" : title);
    gtk_label_set_xalign(GTK_LABEL(title_label), 0.0F);
    gtk_label_set_selectable(GTK_LABEL(title_label), TRUE);
    gtk_label_set_ellipsize(GTK_LABEL(title_label), PANGO_ELLIPSIZE_END);
    gtk_widget_add_css_class(title_label, "heading");
    gtk_box_append(GTK_BOX(box), title_label);
    if (detail != NULL && detail[0] != '\0') {
        GtkWidget *detail_label = gtk_label_new(detail);
        gtk_label_set_xalign(GTK_LABEL(detail_label), 0.0F);
        gtk_label_set_wrap(GTK_LABEL(detail_label), TRUE);
        gtk_label_set_selectable(GTK_LABEL(detail_label), TRUE);
        gtk_widget_add_css_class(detail_label, "dim-label");
        gtk_box_append(GTK_BOX(box), detail_label);
    }
    return box;
}

static GtkWidget *list_row(const char *title, const char *detail) {
    GtkWidget *row = gtk_list_box_row_new();
    gtk_list_box_row_set_child(GTK_LIST_BOX_ROW(row),
                               row_box(title, detail));
    return row;
}

static void populate_rows(GtkWidget *list, GPtrArray *rows,
                          const char *empty_message) {
    clear_list(list);
    if (rows == NULL || rows->len == 0U) {
        gtk_list_box_append(GTK_LIST_BOX(list),
                            list_row(empty_message, ""));
        return;
    }
    for (guint index = 0U; index < rows->len; ++index) {
        ch_gtk_row *row = g_ptr_array_index(rows, index);
        gtk_list_box_append(GTK_LIST_BOX(list),
                            list_row(row->title, row->detail));
    }
}

static void populate_strings(GtkWidget *list, GPtrArray *values,
                             const char *empty_message) {
    clear_list(list);
    if (values == NULL || values->len == 0U) {
        gtk_list_box_append(GTK_LIST_BOX(list),
                            list_row(empty_message, ""));
        return;
    }
    for (guint index = 0U; index < values->len; ++index) {
        gtk_list_box_append(GTK_LIST_BOX(list), list_row(
            g_ptr_array_index(values, index), ""));
    }
}

static void set_error(ClambhookLinuxApp *app, const char *message) {
    gtk_label_set_text(GTK_LABEL(app->error_label),
                       message == NULL ? "" : message);
    gtk_widget_set_visible(app->error_label,
                           message != NULL && message[0] != '\0');
}

static char *json_prompt_body(ClambhookLinuxApp *app, const char *action,
                              const char *scope) {
    return ch_gtk_prompt_resolution_body(
        action, scope,
        gtk_check_button_get_active(
            GTK_CHECK_BUTTON(app->prompt_match_host)),
        gtk_check_button_get_active(
            GTK_CHECK_BUTTON(app->prompt_match_port)),
        gtk_check_button_get_active(
            GTK_CHECK_BUTTON(app->prompt_match_protocol)));
}

static void apply_status(ClambhookLinuxApp *app, const guint8 *data,
                         gsize length) {
    ch_gtk_status_model status;
    g_autoptr(GError) error = NULL;
    if (!ch_gtk_parse_status(data, length, &status, &error)) {
        set_error(app, error->message);
        return;
    }
    app->running = status.running;
    gtk_label_set_text(GTK_LABEL(app->status_label),
                       app->running ? "Connected" : "Disconnected");
    gtk_label_set_text(GTK_LABEL(app->profile_label),
                       status.profile[0] == '\0' ? "—" : status.profile);
    gtk_label_set_text(GTK_LABEL(app->mode_label),
                       status.mode[0] == '\0' ? "—" : status.mode);
    gtk_label_set_text(GTK_LABEL(app->api_label), "API online");
    gtk_button_set_label(GTK_BUTTON(app->connect_button),
                         app->running ? "Disconnect" : "Connect");
    gtk_widget_remove_css_class(app->status_label,
                                app->running ? "error" : "success");
    gtk_widget_add_css_class(app->status_label,
                             app->running ? "success" : "error");
    populate_strings(app->listener_list, status.listeners,
                     "No active proxy listeners");
    ch_gtk_status_model_clear(&status);
}

static void apply_profiles(ClambhookLinuxApp *app, const guint8 *data,
                           gsize length) {
    ch_gtk_profiles_model profiles;
    g_autoptr(GError) error = NULL;
    if (!ch_gtk_parse_profiles(data, length, &profiles, &error)) {
        set_error(app, error->message);
        return;
    }
    const char **strings = g_new0(const char *, profiles.names->len + 1U);
    guint selected = GTK_INVALID_LIST_POSITION;
    for (guint index = 0U; index < profiles.names->len; ++index) {
        strings[index] = g_ptr_array_index(profiles.names, index);
        if (strcmp(strings[index], profiles.active) == 0) selected = index;
    }
    app->updating_profiles = TRUE;
    GtkStringList *model = gtk_string_list_new(strings);
    gtk_drop_down_set_model(GTK_DROP_DOWN(app->profile_dropdown),
                            G_LIST_MODEL(model));
    g_object_unref(model);
    gtk_drop_down_set_selected(GTK_DROP_DOWN(app->profile_dropdown), selected);
    app->updating_profiles = FALSE;
    g_free(strings);
    ch_gtk_profiles_model_clear(&profiles);
}

static void apply_traffic(ClambhookLinuxApp *app, const guint8 *data,
                          gsize length) {
    ch_gtk_traffic_model traffic;
    g_autoptr(GError) error = NULL;
    if (!ch_gtk_parse_traffic(data, length, &traffic, &error)) {
        set_error(app, error->message);
        return;
    }
    g_autofree char *connections = g_strdup_printf(
        "%" G_GUINT64_FORMAT " active connections",
        traffic.active_connections);
    g_autofree char *rx_rate = ch_gtk_format_rate(traffic.rx_bps);
    g_autofree char *tx_rate = ch_gtk_format_rate(traffic.tx_bps);
    g_autofree char *bandwidth = g_strdup_printf(
        "%s down / %s up", rx_rate, tx_rate);
    g_autofree char *rx_total = ch_gtk_format_bytes(traffic.rx_total);
    g_autofree char *tx_total = ch_gtk_format_bytes(traffic.tx_total);
    g_autofree char *totals = g_strdup_printf(
        "%s received / %s sent", rx_total, tx_total);
    gtk_label_set_text(GTK_LABEL(app->connections_label), connections);
    gtk_label_set_text(GTK_LABEL(app->bandwidth_label), bandwidth);
    gtk_label_set_text(GTK_LABEL(app->totals_label), totals);
    populate_rows(app->activity_list, traffic.rows,
                  "No recorded connections");
    ch_gtk_traffic_model_clear(&traffic);
}

static gboolean request_page(RequestKind kind, PageSlot *slot,
                             ch_gtk_page_model_kind *model_kind) {
    switch (kind) {
        case REQUEST_SERVERS:
            *slot = PAGE_SERVERS; *model_kind = CH_GTK_PAGE_SERVERS; return TRUE;
        case REQUEST_POLICIES:
        case REQUEST_POLICY_TEST:
            *slot = PAGE_POLICIES; *model_kind = CH_GTK_PAGE_POLICIES; return TRUE;
        case REQUEST_PROMPTS:
            *slot = PAGE_PROMPTS; *model_kind = CH_GTK_PAGE_PROMPTS; return TRUE;
        case REQUEST_SILENT_DECISIONS:
            *slot = PAGE_SILENT_DECISIONS;
            *model_kind = CH_GTK_PAGE_SILENT_DECISIONS;
            return TRUE;
        case REQUEST_DNS:
        case REQUEST_DNS_UPDATE:
            *slot = PAGE_DNS; *model_kind = CH_GTK_PAGE_DNS; return TRUE;
        case REQUEST_CAPTURES:
            *slot = PAGE_CAPTURES; *model_kind = CH_GTK_PAGE_CAPTURES; return TRUE;
        case REQUEST_CONDITIONER:
        case REQUEST_CONDITIONER_UPDATE:
            *slot = PAGE_CONDITIONER; *model_kind = CH_GTK_PAGE_CONDITIONER; return TRUE;
        default:
            return FALSE;
    }
}

static gboolean apply_conditioner_editor(ClambhookLinuxApp *app,
                                         const guint8 *data,
                                         gsize length) {
    ch_gtk_conditioner_model model;
    g_autoptr(GError) error = NULL;
    if (!ch_gtk_parse_conditioner(data, length, &model, &error)) {
        set_error(app, error->message);
        return FALSE;
    }
    g_free(app->conditioner_profile);
    app->conditioner_profile = g_strdup(model.profile);
    g_autofree char *profile = g_strdup_printf(
        "Profile: %s", model.profile[0] == '\0' ? "active profile" :
                                                    model.profile);
    gtk_label_set_text(GTK_LABEL(app->conditioner_profile_label), profile);
    gtk_check_button_set_active(GTK_CHECK_BUTTON(app->conditioner_enabled),
                                model.enabled);
    g_autofree char *download = model.download_kbps == 0U ? g_strdup("") :
        g_strdup_printf("%" G_GUINT64_FORMAT, model.download_kbps);
    g_autofree char *upload = model.upload_kbps == 0U ? g_strdup("") :
        g_strdup_printf("%" G_GUINT64_FORMAT, model.upload_kbps);
    g_autofree char *loss = model.loss_percent == 0.0 ? g_strdup("") :
        g_strdup_printf("%.17g", model.loss_percent);
    gtk_editable_set_text(GTK_EDITABLE(app->conditioner_download), download);
    gtk_editable_set_text(GTK_EDITABLE(app->conditioner_upload), upload);
    gtk_editable_set_text(GTK_EDITABLE(app->conditioner_latency),
                          model.latency);
    gtk_editable_set_text(GTK_EDITABLE(app->conditioner_jitter), model.jitter);
    gtk_editable_set_text(GTK_EDITABLE(app->conditioner_loss), loss);
    ch_gtk_conditioner_model_clear(&model);
    return TRUE;
}

static gboolean apply_dns_editor(ClambhookLinuxApp *app,
                                 const guint8 *data, gsize length) {
    ch_gtk_dns_model model;
    g_autoptr(GError) error = NULL;
    if (!ch_gtk_parse_dns(data, length, &model, &error)) {
        set_error(app, error->message);
        return FALSE;
    }
    g_free(app->dns_profile);
    app->dns_profile = g_strdup(model.profile);
    g_autofree char *profile = g_strdup_printf(
        "Profile: %s", model.profile[0] == '\0' ? "active profile" :
                                                    model.profile);
    gtk_label_set_text(GTK_LABEL(app->dns_profile_label), profile);
    gtk_check_button_set_active(GTK_CHECK_BUTTON(app->dns_enabled),
                                model.enabled);
    gtk_editable_set_text(GTK_EDITABLE(app->dns_timeout), model.timeout);
    gtk_text_buffer_set_text(app->dns_upstreams, model.upstreams_json, -1);
    ch_gtk_dns_model_clear(&model);
    return TRUE;
}

static void apply_page(ClambhookLinuxApp *app, RequestKind kind,
                       const guint8 *data, gsize length) {
    PageSlot slot;
    ch_gtk_page_model_kind model_kind;
    if (!request_page(kind, &slot, &model_kind)) return;
    if (slot == PAGE_CONDITIONER &&
        !apply_conditioner_editor(app, data, length)) return;
    if (slot == PAGE_DNS && !apply_dns_editor(app, data, length)) return;
    GPtrArray *rows = NULL;
    char *summary = NULL;
    g_autoptr(GError) error = NULL;
    if (!ch_gtk_parse_page_rows(model_kind, data, length, &rows,
                                &summary, &error)) {
        set_error(app, error->message);
        return;
    }
    gtk_label_set_text(GTK_LABEL(app->page_summaries[slot]), summary);
    static const char *const empty_messages[PAGE_SLOT_COUNT] = {
        "No configured servers", "No policy groups", "No pending prompts",
        "No Silent Mode decisions", "No encrypted DNS upstreams",
        "No captured transactions", "No conditioner state"
    };
    if (slot == PAGE_POLICIES) {
        populate_policy_rows(app, rows);
    } else if (slot == PAGE_PROMPTS) {
        populate_prompt_rows(app, rows);
    } else if (slot == PAGE_SILENT_DECISIONS) {
        populate_silent_rows(app, rows);
    } else if (slot == PAGE_CAPTURES) {
        populate_capture_rows(app, rows);
    } else {
        populate_rows(app->page_lists[slot], rows, empty_messages[slot]);
    }
    g_ptr_array_unref(rows);
    g_free(summary);
}

static void apply_capture_status(ClambhookLinuxApp *app, const guint8 *data,
                                 gsize length) {
    g_autoptr(JsonParser) parser = json_parser_new();
    g_autoptr(GError) error = NULL;
    if (!json_parser_load_from_data(parser, (const char *)data,
                                    (gssize)length, &error)) {
        set_error(app, error->message);
        return;
    }
    JsonNode *root = json_parser_get_root(parser);
    if (root == NULL || !JSON_NODE_HOLDS_OBJECT(root)) return;
    JsonObject *object = json_node_get_object(root);
    gboolean enabled = json_object_has_member(object, "enabled") &&
        json_object_get_boolean_member(object, "enabled");
    app->capture_enabled = enabled;
    gint64 count = json_object_has_member(object, "capture_count") ?
        json_object_get_int_member(object, "capture_count") : 0;
    g_autofree char *text = g_strdup_printf(
        "%s · %" G_GINT64_FORMAT " stored transactions",
        enabled ? "Capture enabled" : "Capture disabled", count);
    gtk_label_set_text(GTK_LABEL(app->capture_state_label), text);
    gtk_button_set_label(GTK_BUTTON(app->capture_toggle_button),
                         enabled ? "Disable capture" : "Enable capture");
    gtk_widget_set_sensitive(app->capture_clear_button,
                             enabled && app->active_requests == 0U);
}

static GtkWidget *capture_detail_section(const char *title,
                                         const char *text) {
    GtkWidget *section = gtk_box_new(GTK_ORIENTATION_VERTICAL, 5);
    GtkWidget *heading = gtk_label_new(title);
    gtk_label_set_xalign(GTK_LABEL(heading), 0.0F);
    gtk_widget_add_css_class(heading, "heading");
    gtk_box_append(GTK_BOX(section), heading);
    GtkWidget *content = gtk_label_new(text);
    gtk_label_set_xalign(GTK_LABEL(content), 0.0F);
    gtk_label_set_yalign(GTK_LABEL(content), 0.0F);
    gtk_label_set_wrap(GTK_LABEL(content), TRUE);
    gtk_label_set_selectable(GTK_LABEL(content), TRUE);
    gtk_widget_add_css_class(content, "monospace");
    gtk_box_append(GTK_BOX(section), content);
    return section;
}

static void capture_detail_close(GtkButton *button, gpointer user_data) {
    (void)user_data;
    GtkRoot *root = gtk_widget_get_root(GTK_WIDGET(button));
    if (GTK_IS_WINDOW(root)) gtk_window_destroy(GTK_WINDOW(root));
}

static void capture_copy_curl_clicked(GtkButton *button, gpointer user_data) {
    ClambhookLinuxApp *app = user_data;
    const char *identifier = g_object_get_data(
        G_OBJECT(button), "clambhook-capture-id");
    if (identifier == NULL || identifier[0] == '\0') return;
    g_autofree char *path = ch_gtk_capture_curl_path(identifier);
    send_request(app, REQUEST_CAPTURE_CURL, "GET", path, NULL);
}

static void show_capture_detail(ClambhookLinuxApp *app, const guint8 *data,
                                gsize length) {
    ch_gtk_capture_detail detail;
    g_autoptr(GError) error = NULL;
    if (!ch_gtk_parse_capture_detail(data, length, &detail, &error)) {
        set_error(app, error->message);
        return;
    }
    GtkWidget *dialog = gtk_window_new();
    g_autofree char *window_title = g_strdup_printf(
        "%s capture", detail.method);
    gtk_window_set_title(GTK_WINDOW(dialog), window_title);
    gtk_window_set_transient_for(GTK_WINDOW(dialog), GTK_WINDOW(app->window));
    gtk_window_set_modal(GTK_WINDOW(dialog), TRUE);
    gtk_window_set_default_size(GTK_WINDOW(dialog), 760, 680);

    GtkWidget *root = gtk_box_new(GTK_ORIENTATION_VERTICAL, 14);
    gtk_widget_set_margin_top(root, 22);
    gtk_widget_set_margin_bottom(root, 22);
    gtk_widget_set_margin_start(root, 22);
    gtk_widget_set_margin_end(root, 22);
    GtkWidget *title = gtk_label_new(
        detail.url[0] == '\0' ? detail.host : detail.url);
    gtk_label_set_xalign(GTK_LABEL(title), 0.0F);
    gtk_label_set_wrap(GTK_LABEL(title), TRUE);
    gtk_label_set_selectable(GTK_LABEL(title), TRUE);
    gtk_widget_add_css_class(title, "title-2");
    gtk_box_append(GTK_BOX(root), title);
    g_autofree char *status_code = detail.status == 0 ?
        g_strdup("pending") : g_strdup_printf("HTTP %d", detail.status);
    g_autofree char *status = g_strdup_printf(
        "%s · %s · %s%s%s%s%s%s",
        detail.profile[0] == '\0' ? "default profile" : detail.profile,
        detail.chain[0] == '\0' ? "direct" : detail.chain,
        status_code,
        detail.started_at[0] == '\0' ? "" : " · started ",
        detail.started_at,
        detail.finished_at[0] == '\0' ? "" : " · finished ",
        detail.finished_at,
        detail.error_message[0] == '\0' ? "" : " · request failed");
    GtkWidget *metadata = gtk_label_new(status);
    gtk_label_set_xalign(GTK_LABEL(metadata), 0.0F);
    gtk_label_set_wrap(GTK_LABEL(metadata), TRUE);
    gtk_widget_add_css_class(metadata, "dim-label");
    gtk_box_append(GTK_BOX(root), metadata);
    if (detail.error_message[0] != '\0') {
        GtkWidget *failure = gtk_label_new(detail.error_message);
        gtk_label_set_xalign(GTK_LABEL(failure), 0.0F);
        gtk_label_set_wrap(GTK_LABEL(failure), TRUE);
        gtk_widget_add_css_class(failure, "error");
        gtk_box_append(GTK_BOX(root), failure);
    }

    GtkWidget *scroll = gtk_scrolled_window_new();
    gtk_widget_set_vexpand(scroll, TRUE);
    GtkWidget *sections = gtk_box_new(GTK_ORIENTATION_VERTICAL, 18);
    gtk_box_append(GTK_BOX(sections), capture_detail_section(
        "Request headers", detail.request_headers));
    gtk_box_append(GTK_BOX(sections), capture_detail_section(
        "Request body", detail.request_body));
    gtk_box_append(GTK_BOX(sections), capture_detail_section(
        "Response headers", detail.response_headers));
    gtk_box_append(GTK_BOX(sections), capture_detail_section(
        "Response body", detail.response_body));
    gtk_scrolled_window_set_child(GTK_SCROLLED_WINDOW(scroll), sections);
    gtk_box_append(GTK_BOX(root), scroll);

    GtkWidget *actions = gtk_box_new(GTK_ORIENTATION_HORIZONTAL, 8);
    gtk_widget_set_halign(actions, GTK_ALIGN_END);
    GtkWidget *copy = gtk_button_new_with_label("Copy cURL");
    g_object_set_data_full(G_OBJECT(copy), "clambhook-capture-id",
                           g_strdup(detail.identifier), g_free);
    g_signal_connect(copy, "clicked",
                     G_CALLBACK(capture_copy_curl_clicked), app);
    gtk_box_append(GTK_BOX(actions), copy);
    GtkWidget *close = gtk_button_new_with_label("Close");
    g_signal_connect(close, "clicked", G_CALLBACK(capture_detail_close), NULL);
    gtk_box_append(GTK_BOX(actions), close);
    gtk_box_append(GTK_BOX(root), actions);
    gtk_window_set_child(GTK_WINDOW(dialog), root);
    gtk_window_present(GTK_WINDOW(dialog));
    ch_gtk_capture_detail_clear(&detail);
}

static void copy_curl_export(ClambhookLinuxApp *app, const guint8 *data,
                             gsize length) {
    g_autoptr(GError) error = NULL;
    g_autofree char *curl = ch_gtk_parse_curl_export(
        data, length, &error);
    if (curl == NULL || curl[0] == '\0') {
        set_error(app, error == NULL ?
                  "The daemon returned an empty cURL command." :
                  error->message);
        return;
    }
    GdkClipboard *clipboard = gdk_display_get_clipboard(
        gtk_widget_get_display(app->window));
    gdk_clipboard_set_text(clipboard, curl);
    gtk_label_set_text(GTK_LABEL(app->capture_state_label),
                       "cURL command copied to the clipboard");
}

static void show_curl_import(ClambhookLinuxApp *app, const guint8 *data,
                             gsize length) {
    ch_gtk_curl_import imported;
    g_autoptr(GError) error = NULL;
    if (!ch_gtk_parse_curl_import(data, length, &imported, &error)) {
        set_error(app, error->message);
        return;
    }
    GtkWidget *dialog = gtk_window_new();
    gtk_window_set_title(GTK_WINDOW(dialog), "Imported cURL preview");
    gtk_window_set_transient_for(GTK_WINDOW(dialog), GTK_WINDOW(app->window));
    gtk_window_set_modal(GTK_WINDOW(dialog), TRUE);
    gtk_window_set_default_size(GTK_WINDOW(dialog), 700, 520);
    GtkWidget *root = gtk_box_new(GTK_ORIENTATION_VERTICAL, 14);
    gtk_widget_set_margin_top(root, 22);
    gtk_widget_set_margin_bottom(root, 22);
    gtk_widget_set_margin_start(root, 22);
    gtk_widget_set_margin_end(root, 22);
    g_autofree char *title_text = g_strdup_printf(
        "%s  %s", imported.method, imported.url);
    GtkWidget *title = gtk_label_new(title_text);
    gtk_label_set_xalign(GTK_LABEL(title), 0.0F);
    gtk_label_set_wrap(GTK_LABEL(title), TRUE);
    gtk_label_set_selectable(GTK_LABEL(title), TRUE);
    gtk_widget_add_css_class(title, "title-2");
    gtk_box_append(GTK_BOX(root), title);
    GtkWidget *notice = gtk_label_new(
        "Preview only. Import never executes the command or reads @files.");
    gtk_label_set_xalign(GTK_LABEL(notice), 0.0F);
    gtk_label_set_wrap(GTK_LABEL(notice), TRUE);
    gtk_widget_add_css_class(notice, "dim-label");
    gtk_box_append(GTK_BOX(root), notice);
    GtkWidget *scroll = gtk_scrolled_window_new();
    gtk_widget_set_vexpand(scroll, TRUE);
    GtkWidget *sections = gtk_box_new(GTK_ORIENTATION_VERTICAL, 18);
    gtk_box_append(GTK_BOX(sections), capture_detail_section(
        "Headers", imported.headers));
    gtk_box_append(GTK_BOX(sections), capture_detail_section(
        "Body", imported.body[0] == '\0' ? "No body" : imported.body));
    gtk_scrolled_window_set_child(GTK_SCROLLED_WINDOW(scroll), sections);
    gtk_box_append(GTK_BOX(root), scroll);
    GtkWidget *close = gtk_button_new_with_label("Close");
    gtk_widget_set_halign(close, GTK_ALIGN_END);
    g_signal_connect(close, "clicked", G_CALLBACK(capture_detail_close), NULL);
    gtk_box_append(GTK_BOX(root), close);
    gtk_window_set_child(GTK_WINDOW(dialog), root);
    gtk_window_present(GTK_WINDOW(dialog));
    ch_gtk_curl_import_clear(&imported);
}

static void har_write_finished(GObject *source, GAsyncResult *result,
                               gpointer user_data) {
    HarWriteContext *context = user_data;
    g_autoptr(GError) error = NULL;
    if (!g_file_replace_contents_finish(
            G_FILE(source), result, NULL, &error)) {
        g_autofree char *message = g_strdup_printf(
            "HAR export failed: %s", error->message);
        set_error(context->app, message);
    } else {
        gtk_label_set_text(GTK_LABEL(context->app->capture_state_label),
                           "HAR export saved");
    }
    g_bytes_unref(context->contents);
    g_free(context);
}

static void save_har(ClambhookLinuxApp *app, GFile *destination,
                     GBytes *body) {
    if (destination == NULL) {
        set_error(app, "Choose a destination for the HAR export.");
        return;
    }
    HarWriteContext *context = g_new0(HarWriteContext, 1U);
    context->app = app;
    context->contents = g_bytes_ref(body);
    g_file_replace_contents_bytes_async(
        destination, context->contents, NULL, FALSE,
        G_FILE_CREATE_REPLACE_DESTINATION, NULL, har_write_finished, context);
}

static char *capture_entries_path(ClambhookLinuxApp *app) {
    return ch_gtk_capture_entries_path(
        gtk_editable_get_text(GTK_EDITABLE(app->capture_query)),
        gtk_editable_get_text(GTK_EDITABLE(app->capture_method)),
        gtk_check_button_get_active(
            GTK_CHECK_BUTTON(app->capture_error_only)),
        100U);
}

static void reconcile_failed_mutation(ClambhookLinuxApp *app,
                                      RequestKind kind) {
    switch (kind) {
        case REQUEST_CONNECT:
            send_request(app, REQUEST_STATUS, "GET", "/api/v1/status", NULL);
            break;
        case REQUEST_PROFILE:
            send_request(app, REQUEST_PROFILES, "GET", "/api/v1/profiles",
                         NULL);
            break;
        case REQUEST_POLICY_SELECT:
            send_request(app, REQUEST_POLICIES, "GET",
                         "/api/v1/policy-groups", NULL);
            break;
        case REQUEST_PROMPT_RESOLVE:
            send_request(app, REQUEST_PROMPTS, "GET",
                         "/api/v1/prompts/pending", NULL);
            break;
        case REQUEST_SILENT_PROMOTE:
            send_request(app, REQUEST_SILENT_DECISIONS, "GET",
                         "/api/v1/prompts/decisions", NULL);
            break;
        case REQUEST_CAPTURE_TOGGLE:
            send_request(app, REQUEST_CAPTURE_STATUS, "GET",
                         "/api/v1/developer/status", NULL);
            break;
        case REQUEST_CAPTURE_CLEAR:
            {
                g_autofree char *path = capture_entries_path(app);
                send_request(app, REQUEST_CAPTURES, "GET", path, NULL);
            }
            break;
        case REQUEST_CONDITIONER_UPDATE:
            send_request(app, REQUEST_CONDITIONER, "GET",
                         "/api/v1/conditioner", NULL);
            break;
        case REQUEST_DNS_UPDATE:
            send_request(app, REQUEST_DNS, "GET", "/api/v1/dns", NULL);
            break;
        default:
            break;
    }
}

static void request_finished(GObject *source, GAsyncResult *result,
                             gpointer user_data) {
    RequestContext *context = user_data;
    ClambhookLinuxApp *app = context->app;
    g_autoptr(GError) error = NULL;
    g_autoptr(GBytes) body = soup_session_send_and_read_finish(
        SOUP_SESSION(source), result, &error);
    set_request_activity(app, -1);
    if (error != NULL) {
        g_autofree char *message = g_strdup_printf(
            "API request failed: %s", error->message);
        set_error(app, message);
        gtk_label_set_text(GTK_LABEL(app->api_label), "API offline");
        reconcile_failed_mutation(app, context->kind);
    } else {
        guint status = soup_message_get_status(context->message);
        gsize length = 0U;
        const guint8 *data = g_bytes_get_data(body, &length);
        if (status >= 200U && status < 300U) {
            switch (context->kind) {
                case REQUEST_STATUS: apply_status(app, data, length); break;
                case REQUEST_PROFILES: apply_profiles(app, data, length); break;
                case REQUEST_TRAFFIC: apply_traffic(app, data, length); break;
                case REQUEST_CAPTURE_STATUS:
                    apply_capture_status(app, data, length); break;
                case REQUEST_CAPTURE_DETAIL:
                    show_capture_detail(app, data, length); break;
                case REQUEST_CAPTURE_CURL:
                    copy_curl_export(app, data, length); break;
                case REQUEST_CAPTURE_CURL_IMPORT:
                    show_curl_import(app, data, length); break;
                case REQUEST_CAPTURE_HAR:
                    save_har(app, context->destination, body); break;
                case REQUEST_CONNECT:
                    apply_status(app, data, length);
                    refresh_all(app);
                    break;
                case REQUEST_PROFILE:
                    apply_profiles(app, data, length);
                    refresh_all(app);
                    break;
                case REQUEST_POLICY_SELECT:
                case REQUEST_PROMPT_RESOLVE:
                case REQUEST_SILENT_PROMOTE:
                case REQUEST_CAPTURE_TOGGLE:
                case REQUEST_CAPTURE_CLEAR:
                    refresh_all(app);
                    break;
                default:
                    apply_page(app, context->kind, data, length);
                    break;
            }
        } else {
            g_autofree char *response = g_strndup(
                data == NULL ? "" : (const char *)data, length);
            g_autofree char *message = g_strdup_printf(
                "Daemon request failed (%u): %s", status, response);
            set_error(app, message);
            reconcile_failed_mutation(app, context->kind);
        }
    }
    g_object_unref(context->message);
    g_clear_object(&context->destination);
    g_free(context);
}

static void send_request_internal(ClambhookLinuxApp *app, RequestKind kind,
                                  const char *method, const char *path,
                                  const char *body, GFile *destination) {
    g_autofree char *url = join_url(app->api_url, path);
    SoupMessage *message = soup_message_new(method, url);
    if (message == NULL) {
        set_error(app, "The configured daemon URL is invalid.");
        return;
    }
    SoupMessageHeaders *headers = soup_message_get_request_headers(message);
    if (app->api_token[0] != '\0') {
        g_autofree char *authorization = g_strdup_printf(
            "Bearer %s", app->api_token);
        soup_message_headers_replace(headers, "Authorization", authorization);
    }
    soup_message_headers_replace(headers, "Accept", "application/json");
    if (strcmp(method, "POST") == 0 || strcmp(method, "PUT") == 0 ||
        strcmp(method, "DELETE") == 0) {
        const char *payload = body == NULL ? "{}" : body;
        g_autoptr(GBytes) request_body = g_bytes_new(
            payload, strlen(payload));
        soup_message_set_request_body_from_bytes(
            message, "application/json", request_body);
    }
    RequestContext *context = g_new0(RequestContext, 1U);
    context->app = app;
    context->message = message;
    context->kind = kind;
    context->destination = destination == NULL ? NULL :
        g_object_ref(destination);
    set_request_activity(app, 1);
    soup_session_send_and_read_async(
        app->session, message, G_PRIORITY_DEFAULT, NULL,
        request_finished, context);
}

static void send_request(ClambhookLinuxApp *app, RequestKind kind,
                         const char *method, const char *path,
                         const char *body) {
    send_request_internal(app, kind, method, path, body, NULL);
}

static void send_request_to_file(ClambhookLinuxApp *app, RequestKind kind,
                                 const char *method, const char *path,
                                 GFile *destination) {
    send_request_internal(app, kind, method, path, NULL, destination);
}

static void refresh_all(ClambhookLinuxApp *app) {
    set_error(app, "");
    send_request(app, REQUEST_STATUS, "GET", "/api/v1/status", NULL);
    send_request(app, REQUEST_PROFILES, "GET", "/api/v1/profiles", NULL);
    send_request(app, REQUEST_TRAFFIC, "GET",
                 "/api/v1/traffic?limit=200", NULL);
    send_request(app, REQUEST_SERVERS, "GET", "/api/v1/servers", NULL);
    send_request(app, REQUEST_POLICIES, "GET",
                 "/api/v1/policy-groups", NULL);
    send_request(app, REQUEST_PROMPTS, "GET",
                 "/api/v1/prompts/pending", NULL);
    send_request(app, REQUEST_SILENT_DECISIONS, "GET",
                 "/api/v1/prompts/decisions", NULL);
    send_request(app, REQUEST_DNS, "GET", "/api/v1/dns", NULL);
    send_request(app, REQUEST_CAPTURE_STATUS, "GET",
                 "/api/v1/developer/status", NULL);
    g_autofree char *captures = capture_entries_path(app);
    send_request(app, REQUEST_CAPTURES, "GET", captures, NULL);
    send_request(app, REQUEST_CONDITIONER, "GET",
                 "/api/v1/conditioner", NULL);
}

static void connect_clicked(GtkButton *button, gpointer user_data) {
    (void)button;
    ClambhookLinuxApp *app = user_data;
    send_request(app, REQUEST_CONNECT, "POST",
                 app->running ? "/api/v1/disconnect" : "/api/v1/connect",
                 "{}");
}

static void refresh_clicked(GtkButton *button, gpointer user_data) {
    (void)button;
    refresh_all(user_data);
}

static void policy_test_clicked(GtkButton *button, gpointer user_data) {
    (void)button;
    send_request(user_data, REQUEST_POLICY_TEST, "POST",
                 "/api/v1/policy-groups/test", "{}");
}

static void profile_selected(GObject *object, GParamSpec *parameter,
                             gpointer user_data) {
    (void)parameter;
    ClambhookLinuxApp *app = user_data;
    if (app->updating_profiles) return;
    guint selected = gtk_drop_down_get_selected(GTK_DROP_DOWN(object));
    if (selected == GTK_INVALID_LIST_POSITION) return;
    GListModel *model = gtk_drop_down_get_model(GTK_DROP_DOWN(object));
    g_autoptr(GtkStringObject) item = g_list_model_get_item(model, selected);
    if (item == NULL) return;
    const char *name = gtk_string_object_get_string(item);
    g_autofree char *body = ch_gtk_profile_body(name);
    send_request(app, REQUEST_PROFILE, "PUT",
                 "/api/v1/profiles/active", body);
}

static void policy_selection_context_free(gpointer data, GClosure *closure) {
    (void)closure;
    PolicySelectionContext *context = data;
    g_free(context->group);
    g_free(context->selected);
    g_free(context);
}

static void policy_selected(GObject *object, GParamSpec *parameter,
                            gpointer user_data) {
    (void)parameter;
    PolicySelectionContext *context = user_data;
    guint selected = gtk_drop_down_get_selected(GTK_DROP_DOWN(object));
    if (selected == GTK_INVALID_LIST_POSITION) return;
    GListModel *model = gtk_drop_down_get_model(GTK_DROP_DOWN(object));
    g_autoptr(GtkStringObject) item = g_list_model_get_item(model, selected);
    if (item == NULL) return;
    const char *chain = gtk_string_object_get_string(item);
    if (strcmp(chain, context->selected) == 0) return;
    g_free(context->selected);
    context->selected = g_strdup(chain);
    g_autofree char *body = ch_gtk_policy_selection_body(
        context->group, chain);
    send_request(context->app, REQUEST_POLICY_SELECT, "PUT",
                 "/api/v1/policy-groups/selection", body);
}

static void prompt_action_context_free(gpointer data, GClosure *closure) {
    (void)closure;
    PromptActionContext *context = data;
    g_free(context->identifier);
    g_free(context->action);
    g_free(context->scope);
    g_free(context);
}

static void prompt_action_clicked(GtkButton *button, gpointer user_data) {
    (void)button;
    PromptActionContext *context = user_data;
    g_autofree char *path = ch_gtk_prompt_resolution_path(
        context->identifier);
    g_autofree char *body = json_prompt_body(
        context->app, context->action, context->scope);
    send_request(context->app, REQUEST_PROMPT_RESOLVE, "POST", path, body);
}

static GtkWidget *prompt_action_button(ClambhookLinuxApp *app,
                                       const char *identifier,
                                       const char *label,
                                       const char *action,
                                       const char *scope,
                                       gboolean destructive) {
    GtkWidget *button = gtk_button_new_with_label(label);
    if (destructive) gtk_widget_add_css_class(button, "destructive-action");
    PromptActionContext *context = g_new0(PromptActionContext, 1U);
    context->app = app;
    context->identifier = g_strdup(identifier);
    context->action = g_strdup(action);
    context->scope = g_strdup(scope);
    g_signal_connect_data(button, "clicked", G_CALLBACK(prompt_action_clicked),
                          context, prompt_action_context_free, 0);
    return button;
}

static void silent_action_context_free(gpointer data, GClosure *closure) {
    (void)closure;
    SilentActionContext *context = data;
    g_free(context->identifier);
    g_free(context->scope);
    g_free(context);
}

static void silent_promote_clicked(GtkButton *button, gpointer user_data) {
    (void)button;
    SilentActionContext *context = user_data;
    ClambhookLinuxApp *app = context->app;
    g_autofree char *path = ch_gtk_silent_promotion_path(
        context->identifier);
    g_autofree char *body = ch_gtk_silent_promotion_body(
        context->scope,
        gtk_check_button_get_active(
            GTK_CHECK_BUTTON(app->prompt_match_host)),
        gtk_check_button_get_active(
            GTK_CHECK_BUTTON(app->prompt_match_port)),
        gtk_check_button_get_active(
            GTK_CHECK_BUTTON(app->prompt_match_protocol)));
    send_request(app, REQUEST_SILENT_PROMOTE, "POST", path, body);
}

static GtkWidget *silent_action_button(ClambhookLinuxApp *app,
                                       const char *identifier,
                                       const char *label,
                                       const char *scope) {
    GtkWidget *button = gtk_button_new_with_label(label);
    SilentActionContext *context = g_new0(SilentActionContext, 1U);
    context->app = app;
    context->identifier = g_strdup(identifier);
    context->scope = g_strdup(scope);
    g_signal_connect_data(button, "clicked", G_CALLBACK(silent_promote_clicked),
                          context, silent_action_context_free, 0);
    return button;
}

static void populate_policy_rows(ClambhookLinuxApp *app, GPtrArray *rows) {
    GtkWidget *list = app->page_lists[PAGE_POLICIES];
    clear_list(list);
    if (rows == NULL || rows->len == 0U) {
        gtk_list_box_append(GTK_LIST_BOX(list),
                            list_row("No policy groups", ""));
        return;
    }
    for (guint index = 0U; index < rows->len; ++index) {
        ch_gtk_row *row = g_ptr_array_index(rows, index);
        GtkWidget *list_item = gtk_list_box_row_new();
        GtkWidget *content = row_box(row->title, row->detail);
        if (row->selectable && row->options != NULL &&
            row->options->len > 0U) {
            GtkStringList *model = gtk_string_list_new(NULL);
            guint selected = GTK_INVALID_LIST_POSITION;
            for (guint option = 0U; option < row->options->len; ++option) {
                const char *chain = g_ptr_array_index(row->options, option);
                gtk_string_list_append(model, chain);
                if (strcmp(chain, row->selected) == 0) selected = option;
            }
            GtkWidget *dropdown = gtk_drop_down_new(
                G_LIST_MODEL(model), NULL);
            g_object_unref(model);
            gtk_drop_down_set_selected(GTK_DROP_DOWN(dropdown), selected);
            gtk_widget_set_halign(dropdown, GTK_ALIGN_START);
            gtk_widget_set_tooltip_text(
                dropdown, "Select the active chain for this policy group");
            PolicySelectionContext *context = g_new0(
                PolicySelectionContext, 1U);
            context->app = app;
            context->group = g_strdup(row->identifier);
            context->selected = g_strdup(row->selected);
            g_signal_connect_data(
                dropdown, "notify::selected", G_CALLBACK(policy_selected),
                context, policy_selection_context_free, 0);
            gtk_box_append(GTK_BOX(content), dropdown);
        }
        gtk_list_box_row_set_child(GTK_LIST_BOX_ROW(list_item), content);
        gtk_list_box_append(GTK_LIST_BOX(list), list_item);
    }
}

static void populate_prompt_rows(ClambhookLinuxApp *app, GPtrArray *rows) {
    GtkWidget *list = app->page_lists[PAGE_PROMPTS];
    clear_list(list);
    if (rows == NULL || rows->len == 0U) {
        gtk_list_box_append(GTK_LIST_BOX(list),
                            list_row("No pending prompts", ""));
        return;
    }
    for (guint index = 0U; index < rows->len; ++index) {
        ch_gtk_row *row = g_ptr_array_index(rows, index);
        GtkWidget *list_item = gtk_list_box_row_new();
        GtkWidget *content = row_box(row->title, row->detail);
        GtkWidget *actions = gtk_flow_box_new();
        gtk_flow_box_set_selection_mode(GTK_FLOW_BOX(actions),
                                        GTK_SELECTION_NONE);
        gtk_flow_box_set_min_children_per_line(GTK_FLOW_BOX(actions), 1U);
        gtk_flow_box_set_max_children_per_line(GTK_FLOW_BOX(actions), 5U);
        gtk_flow_box_set_column_spacing(GTK_FLOW_BOX(actions), 6U);
        gtk_flow_box_set_row_spacing(GTK_FLOW_BOX(actions), 6U);
        gtk_flow_box_append(GTK_FLOW_BOX(actions), prompt_action_button(
            app, row->identifier, "Allow once", "allow", "once", FALSE));
        gtk_flow_box_append(GTK_FLOW_BOX(actions), prompt_action_button(
            app, row->identifier, "Allow session", "allow", "session",
            FALSE));
        gtk_flow_box_append(GTK_FLOW_BOX(actions), prompt_action_button(
            app, row->identifier, "Allow until quit", "allow", "until_quit",
            FALSE));
        gtk_flow_box_append(GTK_FLOW_BOX(actions), prompt_action_button(
            app, row->identifier, "Allow forever", "allow", "forever",
            FALSE));
        gtk_flow_box_append(GTK_FLOW_BOX(actions), prompt_action_button(
            app, row->identifier, "Block forever", "block", "forever",
            TRUE));
        gtk_box_append(GTK_BOX(content), actions);
        gtk_list_box_row_set_child(GTK_LIST_BOX_ROW(list_item), content);
        gtk_list_box_append(GTK_LIST_BOX(list), list_item);
    }
}

static void populate_silent_rows(ClambhookLinuxApp *app, GPtrArray *rows) {
    GtkWidget *list = app->page_lists[PAGE_SILENT_DECISIONS];
    clear_list(list);
    if (rows == NULL || rows->len == 0U) {
        gtk_list_box_append(GTK_LIST_BOX(list),
                            list_row("No Silent Mode decisions", ""));
        return;
    }
    for (guint index = 0U; index < rows->len; ++index) {
        ch_gtk_row *row = g_ptr_array_index(rows, index);
        GtkWidget *list_item = gtk_list_box_row_new();
        GtkWidget *content = row_box(row->title, row->detail);
        GtkWidget *actions = gtk_box_new(GTK_ORIENTATION_HORIZONTAL, 6);
        gtk_widget_set_halign(actions, GTK_ALIGN_START);
        gtk_box_append(GTK_BOX(actions), silent_action_button(
            app, row->identifier, "Remember session", "session"));
        gtk_box_append(GTK_BOX(actions), silent_action_button(
            app, row->identifier, "Until quit", "until_quit"));
        gtk_box_append(GTK_BOX(actions), silent_action_button(
            app, row->identifier, "Forever", "forever"));
        gtk_box_append(GTK_BOX(content), actions);
        gtk_list_box_row_set_child(GTK_LIST_BOX_ROW(list_item), content);
        gtk_list_box_append(GTK_LIST_BOX(list), list_item);
    }
}

static void populate_capture_rows(ClambhookLinuxApp *app, GPtrArray *rows) {
    GtkWidget *list = app->page_lists[PAGE_CAPTURES];
    clear_list(list);
    if (rows == NULL || rows->len == 0U) {
        gtk_list_box_append(GTK_LIST_BOX(list),
                            list_row("No captured transactions", ""));
        return;
    }
    for (guint index = 0U; index < rows->len; ++index) {
        ch_gtk_row *row = g_ptr_array_index(rows, index);
        GtkWidget *list_item = list_row(row->title, row->detail);
        gtk_list_box_row_set_activatable(GTK_LIST_BOX_ROW(list_item), TRUE);
        gtk_widget_set_tooltip_text(
            list_item, "Open redacted request and response details");
        g_object_set_data_full(G_OBJECT(list_item), "clambhook-capture-id",
                               g_strdup(row->identifier), g_free);
        gtk_list_box_append(GTK_LIST_BOX(list), list_item);
    }
}

static void capture_row_activated(GtkListBox *list, GtkListBoxRow *row,
                                  gpointer user_data) {
    (void)list;
    const char *identifier = g_object_get_data(
        G_OBJECT(row), "clambhook-capture-id");
    if (identifier == NULL || identifier[0] == '\0') return;
    g_autofree char *path = ch_gtk_capture_detail_path(identifier);
    send_request(user_data, REQUEST_CAPTURE_DETAIL, "GET", path, NULL);
}

static void capture_filter_clicked(GtkButton *button, gpointer user_data) {
    (void)button;
    ClambhookLinuxApp *app = user_data;
    set_error(app, "");
    g_autofree char *path = capture_entries_path(app);
    send_request(app, REQUEST_CAPTURES, "GET", path, NULL);
}

static void capture_toggle_clicked(GtkButton *button, gpointer user_data) {
    (void)button;
    ClambhookLinuxApp *app = user_data;
    g_autofree char *body = ch_gtk_capture_enabled_body(
        !app->capture_enabled);
    send_request(app, REQUEST_CAPTURE_TOGGLE, "PUT",
                 "/api/v1/developer/settings", body);
}

static void capture_clear_confirmed(GtkButton *button, gpointer user_data) {
    GtkRoot *root = gtk_widget_get_root(GTK_WIDGET(button));
    if (GTK_IS_WINDOW(root)) gtk_window_destroy(GTK_WINDOW(root));
    send_request(user_data, REQUEST_CAPTURE_CLEAR, "DELETE",
                 "/api/v1/developer/entries", "{}");
}

static void capture_clear_cancelled(GtkButton *button, gpointer user_data) {
    (void)user_data;
    GtkRoot *root = gtk_widget_get_root(GTK_WIDGET(button));
    if (GTK_IS_WINDOW(root)) gtk_window_destroy(GTK_WINDOW(root));
}

static void capture_clear_clicked(GtkButton *button, gpointer user_data) {
    (void)button;
    ClambhookLinuxApp *app = user_data;
    GtkWidget *dialog = gtk_window_new();
    gtk_window_set_title(GTK_WINDOW(dialog), "Clear captured transactions?");
    gtk_window_set_transient_for(GTK_WINDOW(dialog), GTK_WINDOW(app->window));
    gtk_window_set_modal(GTK_WINDOW(dialog), TRUE);
    gtk_window_set_resizable(GTK_WINDOW(dialog), FALSE);

    GtkWidget *content = gtk_box_new(GTK_ORIENTATION_VERTICAL, 14);
    gtk_widget_set_margin_top(content, 22);
    gtk_widget_set_margin_bottom(content, 22);
    gtk_widget_set_margin_start(content, 22);
    gtk_widget_set_margin_end(content, 22);
    GtkWidget *title = gtk_label_new("Delete every bounded capture entry?");
    gtk_label_set_xalign(GTK_LABEL(title), 0.0F);
    gtk_widget_add_css_class(title, "title-3");
    gtk_box_append(GTK_BOX(content), title);
    GtkWidget *detail = gtk_label_new(
        "This removes captured request and response data from the daemon. "
        "The action cannot be undone.");
    gtk_label_set_xalign(GTK_LABEL(detail), 0.0F);
    gtk_label_set_wrap(GTK_LABEL(detail), TRUE);
    gtk_box_append(GTK_BOX(content), detail);
    GtkWidget *actions = gtk_box_new(GTK_ORIENTATION_HORIZONTAL, 8);
    gtk_widget_set_halign(actions, GTK_ALIGN_END);
    GtkWidget *cancel = gtk_button_new_with_label("Cancel");
    g_signal_connect(cancel, "clicked",
                     G_CALLBACK(capture_clear_cancelled), NULL);
    gtk_box_append(GTK_BOX(actions), cancel);
    GtkWidget *clear = gtk_button_new_with_label("Clear all");
    gtk_widget_add_css_class(clear, "destructive-action");
    g_signal_connect(clear, "clicked",
                     G_CALLBACK(capture_clear_confirmed), app);
    gtk_box_append(GTK_BOX(actions), clear);
    gtk_box_append(GTK_BOX(content), actions);
    gtk_window_set_child(GTK_WINDOW(dialog), content);
    gtk_window_present(GTK_WINDOW(dialog));
}

static void capture_import_preview(GtkButton *button, gpointer user_data) {
    ClambhookLinuxApp *app = user_data;
    GtkTextBuffer *buffer = g_object_get_data(
        G_OBJECT(button), "clambhook-curl-buffer");
    if (buffer == NULL) return;
    GtkTextIter start;
    GtkTextIter end;
    gtk_text_buffer_get_bounds(buffer, &start, &end);
    g_autofree char *command = gtk_text_buffer_get_text(
        buffer, &start, &end, FALSE);
    if (command == NULL || command[0] == '\0') {
        set_error(app, "Paste a cURL command to import.");
        return;
    }
    g_autofree char *body = ch_gtk_curl_import_body(command);
    GtkRoot *root = gtk_widget_get_root(GTK_WIDGET(button));
    if (GTK_IS_WINDOW(root)) gtk_window_destroy(GTK_WINDOW(root));
    send_request(app, REQUEST_CAPTURE_CURL_IMPORT, "POST",
                 "/api/v1/developer/curl/import", body);
}

static void capture_import_clicked(GtkButton *button, gpointer user_data) {
    (void)button;
    ClambhookLinuxApp *app = user_data;
    GtkWidget *dialog = gtk_window_new();
    gtk_window_set_title(GTK_WINDOW(dialog), "Import cURL command");
    gtk_window_set_transient_for(GTK_WINDOW(dialog), GTK_WINDOW(app->window));
    gtk_window_set_modal(GTK_WINDOW(dialog), TRUE);
    gtk_window_set_default_size(GTK_WINDOW(dialog), 680, 360);
    GtkWidget *root = gtk_box_new(GTK_ORIENTATION_VERTICAL, 12);
    gtk_widget_set_margin_top(root, 22);
    gtk_widget_set_margin_bottom(root, 22);
    gtk_widget_set_margin_start(root, 22);
    gtk_widget_set_margin_end(root, 22);
    GtkWidget *description = gtk_label_new(
        "Paste a cURL command to parse it into an editable request preview. "
        "Nothing is executed, and @file arguments are rejected.");
    gtk_label_set_xalign(GTK_LABEL(description), 0.0F);
    gtk_label_set_wrap(GTK_LABEL(description), TRUE);
    gtk_box_append(GTK_BOX(root), description);
    GtkWidget *scroll = gtk_scrolled_window_new();
    gtk_widget_set_vexpand(scroll, TRUE);
    GtkWidget *view = gtk_text_view_new();
    gtk_text_view_set_wrap_mode(GTK_TEXT_VIEW(view), GTK_WRAP_WORD_CHAR);
    gtk_widget_add_css_class(view, "monospace");
    gtk_scrolled_window_set_child(GTK_SCROLLED_WINDOW(scroll), view);
    gtk_box_append(GTK_BOX(root), scroll);
    GtkWidget *actions = gtk_box_new(GTK_ORIENTATION_HORIZONTAL, 8);
    gtk_widget_set_halign(actions, GTK_ALIGN_END);
    GtkWidget *cancel = gtk_button_new_with_label("Cancel");
    g_signal_connect(cancel, "clicked", G_CALLBACK(capture_detail_close), NULL);
    gtk_box_append(GTK_BOX(actions), cancel);
    GtkWidget *preview = gtk_button_new_with_label("Preview import");
    gtk_widget_add_css_class(preview, "suggested-action");
    g_object_set_data(G_OBJECT(preview), "clambhook-curl-buffer",
                      gtk_text_view_get_buffer(GTK_TEXT_VIEW(view)));
    g_signal_connect(preview, "clicked",
                     G_CALLBACK(capture_import_preview), app);
    gtk_box_append(GTK_BOX(actions), preview);
    gtk_box_append(GTK_BOX(root), actions);
    gtk_window_set_child(GTK_WINDOW(dialog), root);
    gtk_window_present(GTK_WINDOW(dialog));
    gtk_widget_grab_focus(view);
}

G_GNUC_BEGIN_IGNORE_DEPRECATIONS
static void capture_har_destination(GtkNativeDialog *dialog, int response,
                                    gpointer user_data) {
    ClambhookLinuxApp *app = user_data;
    if (response == GTK_RESPONSE_ACCEPT) {
        g_autoptr(GFile) file = gtk_file_chooser_get_file(
            GTK_FILE_CHOOSER(dialog));
        if (file != NULL) {
            send_request_to_file(app, REQUEST_CAPTURE_HAR, "GET",
                                 "/api/v1/developer/har", file);
        }
    }
    gtk_native_dialog_destroy(dialog);
    g_object_unref(dialog);
}

static void capture_har_clicked(GtkButton *button, gpointer user_data) {
    (void)button;
    ClambhookLinuxApp *app = user_data;
    GtkFileChooserNative *chooser = gtk_file_chooser_native_new(
        "Export ClambHook HAR", GTK_WINDOW(app->window),
        GTK_FILE_CHOOSER_ACTION_SAVE, "Export", "Cancel");
    gtk_file_chooser_set_current_name(GTK_FILE_CHOOSER(chooser),
                                      "clambhook.har");
    g_signal_connect(chooser, "response",
                     G_CALLBACK(capture_har_destination), app);
    gtk_native_dialog_show(GTK_NATIVE_DIALOG(chooser));
}
G_GNUC_END_IGNORE_DEPRECATIONS

static void conditioner_save_clicked(GtkButton *button, gpointer user_data) {
    (void)button;
    ClambhookLinuxApp *app = user_data;
    g_autoptr(GError) error = NULL;
    g_autofree char *body = ch_gtk_conditioner_body(
        app->conditioner_profile,
        gtk_check_button_get_active(
            GTK_CHECK_BUTTON(app->conditioner_enabled)),
        gtk_editable_get_text(GTK_EDITABLE(app->conditioner_download)),
        gtk_editable_get_text(GTK_EDITABLE(app->conditioner_upload)),
        gtk_editable_get_text(GTK_EDITABLE(app->conditioner_latency)),
        gtk_editable_get_text(GTK_EDITABLE(app->conditioner_jitter)),
        gtk_editable_get_text(GTK_EDITABLE(app->conditioner_loss)), &error);
    if (body == NULL) {
        set_error(app, error == NULL ? "Invalid conditioner values." :
                                      error->message);
        return;
    }
    set_error(app, "");
    send_request(app, REQUEST_CONDITIONER_UPDATE, "PUT",
                 "/api/v1/conditioner", body);
}

static void dns_save_clicked(GtkButton *button, gpointer user_data) {
    (void)button;
    ClambhookLinuxApp *app = user_data;
    GtkTextIter start;
    GtkTextIter end;
    gtk_text_buffer_get_bounds(app->dns_upstreams, &start, &end);
    g_autofree char *upstreams = gtk_text_buffer_get_text(
        app->dns_upstreams, &start, &end, FALSE);
    g_autoptr(GError) error = NULL;
    g_autofree char *body = ch_gtk_dns_body(
        app->dns_profile,
        gtk_check_button_get_active(GTK_CHECK_BUTTON(app->dns_enabled)),
        gtk_editable_get_text(GTK_EDITABLE(app->dns_timeout)), upstreams,
        &error);
    if (body == NULL) {
        set_error(app, error == NULL ? "Invalid DNS settings." :
                                      error->message);
        return;
    }
    set_error(app, "");
    send_request(app, REQUEST_DNS_UPDATE, "PUT", "/api/v1/dns", body);
}

static GtkWidget *detail_row(const char *title, GtkWidget **value_out) {
    GtkWidget *row = gtk_box_new(GTK_ORIENTATION_HORIZONTAL, 12);
    GtkWidget *title_label = gtk_label_new(title);
    GtkWidget *value = gtk_label_new("—");
    gtk_widget_set_hexpand(value, TRUE);
    gtk_label_set_xalign(GTK_LABEL(title_label), 0.0F);
    gtk_label_set_xalign(GTK_LABEL(value), 1.0F);
    gtk_label_set_selectable(GTK_LABEL(value), TRUE);
    gtk_widget_add_css_class(title_label, "dim-label");
    gtk_box_append(GTK_BOX(row), title_label);
    gtk_box_append(GTK_BOX(row), value);
    *value_out = value;
    return row;
}

static GtkWidget *page_container(const char *title, const char *description) {
    GtkWidget *page = gtk_box_new(GTK_ORIENTATION_VERTICAL, 12);
    gtk_widget_set_margin_top(page, 24);
    gtk_widget_set_margin_bottom(page, 24);
    gtk_widget_set_margin_start(page, 28);
    gtk_widget_set_margin_end(page, 28);
    GtkWidget *title_label = gtk_label_new(title);
    gtk_label_set_xalign(GTK_LABEL(title_label), 0.0F);
    gtk_widget_add_css_class(title_label, "title-1");
    gtk_box_append(GTK_BOX(page), title_label);
    GtkWidget *description_label = gtk_label_new(description);
    gtk_label_set_xalign(GTK_LABEL(description_label), 0.0F);
    gtk_label_set_wrap(GTK_LABEL(description_label), TRUE);
    gtk_widget_add_css_class(description_label, "dim-label");
    gtk_box_append(GTK_BOX(page), description_label);
    return page;
}

static GtkWidget *scrolled_list(GtkWidget **list_out) {
    GtkWidget *scroll = gtk_scrolled_window_new();
    gtk_widget_set_vexpand(scroll, TRUE);
    gtk_scrolled_window_set_policy(GTK_SCROLLED_WINDOW(scroll),
                                   GTK_POLICY_NEVER, GTK_POLICY_AUTOMATIC);
    GtkWidget *list = gtk_list_box_new();
    gtk_list_box_set_selection_mode(GTK_LIST_BOX(list), GTK_SELECTION_NONE);
    gtk_widget_add_css_class(list, "boxed-list");
    gtk_scrolled_window_set_child(GTK_SCROLLED_WINDOW(scroll), list);
    *list_out = list;
    return scroll;
}

static GtkWidget *create_now_page(ClambhookLinuxApp *app) {
    GtkWidget *page = page_container(
        "Network status",
        "ClambHook routing state and current active-profile traffic.");
    app->status_label = gtk_label_new("Loading…");
    gtk_label_set_xalign(GTK_LABEL(app->status_label), 0.0F);
    gtk_widget_add_css_class(app->status_label, "title-2");
    gtk_box_append(GTK_BOX(page), app->status_label);
    GtkWidget *details = gtk_box_new(GTK_ORIENTATION_VERTICAL, 10);
    gtk_widget_add_css_class(details, "card");
    gtk_box_append(GTK_BOX(details), detail_row("Active profile",
                                                &app->profile_label));
    gtk_box_append(GTK_BOX(details), detail_row("Routing mode",
                                                &app->mode_label));
    gtk_box_append(GTK_BOX(details), detail_row("Daemon API",
                                                &app->api_label));
    gtk_box_append(GTK_BOX(details), detail_row("Connections",
                                                &app->connections_label));
    gtk_box_append(GTK_BOX(details), detail_row("Bandwidth",
                                                &app->bandwidth_label));
    gtk_box_append(GTK_BOX(details), detail_row("Traffic totals",
                                                &app->totals_label));
    gtk_box_append(GTK_BOX(page), details);
    app->connect_button = gtk_button_new_with_label("Connect");
    gtk_widget_add_css_class(app->connect_button, "suggested-action");
    gtk_widget_set_halign(app->connect_button, GTK_ALIGN_START);
    g_signal_connect(app->connect_button, "clicked",
                     G_CALLBACK(connect_clicked), app);
    gtk_box_append(GTK_BOX(page), app->connect_button);
    return page;
}

static GtkWidget *create_data_page(ClambhookLinuxApp *app, PageSlot slot,
                                   const char *title, const char *description,
                                   gboolean policy_action) {
    GtkWidget *page = page_container(title, description);
    GtkWidget *summary = gtk_label_new("Loading…");
    gtk_label_set_xalign(GTK_LABEL(summary), 0.0F);
    gtk_widget_add_css_class(summary, "dim-label");
    app->page_summaries[slot] = summary;
    if (policy_action) {
        GtkWidget *bar = gtk_box_new(GTK_ORIENTATION_HORIZONTAL, 8);
        gtk_widget_set_hexpand(summary, TRUE);
        gtk_box_append(GTK_BOX(bar), summary);
        app->policy_test_button = gtk_button_new_with_label("Latency test");
        g_signal_connect(app->policy_test_button, "clicked",
                         G_CALLBACK(policy_test_clicked), app);
        gtk_box_append(GTK_BOX(bar), app->policy_test_button);
        gtk_box_append(GTK_BOX(page), bar);
    } else {
        gtk_box_append(GTK_BOX(page), summary);
    }
    gtk_box_append(GTK_BOX(page), scrolled_list(&app->page_lists[slot]));
    return page;
}

static GtkWidget *create_activity_page(ClambhookLinuxApp *app) {
    GtkWidget *page = page_container(
        "Traffic Monitor",
        "Recent routed connections, decisions, chains, and byte totals.");
    gtk_box_append(GTK_BOX(page), scrolled_list(&app->activity_list));
    return page;
}

static GtkWidget *create_prompt_page(ClambhookLinuxApp *app) {
    GtkWidget *page = page_container(
        "Connection prompts",
        "Resolve pending connections and promote recent Silent Mode decisions "
        "into remembered native rules.");
    GtkWidget *pending_title = gtk_label_new("Pending prompts");
    gtk_label_set_xalign(GTK_LABEL(pending_title), 0.0F);
    gtk_widget_add_css_class(pending_title, "title-3");
    gtk_box_append(GTK_BOX(page), pending_title);
    GtkWidget *summary = gtk_label_new("Loading…");
    gtk_label_set_xalign(GTK_LABEL(summary), 0.0F);
    gtk_widget_add_css_class(summary, "dim-label");
    app->page_summaries[PAGE_PROMPTS] = summary;
    gtk_box_append(GTK_BOX(page), summary);

    GtkWidget *match = gtk_box_new(GTK_ORIENTATION_HORIZONTAL, 10);
    GtkWidget *match_label = gtk_label_new("Persisted-rule matching:");
    gtk_widget_add_css_class(match_label, "dim-label");
    gtk_box_append(GTK_BOX(match), match_label);
    app->prompt_match_host = gtk_check_button_new_with_label("This host");
    app->prompt_match_port = gtk_check_button_new_with_label("This port");
    app->prompt_match_protocol = gtk_check_button_new_with_label(
        "This protocol");
    gtk_box_append(GTK_BOX(match), app->prompt_match_host);
    gtk_box_append(GTK_BOX(match), app->prompt_match_port);
    gtk_box_append(GTK_BOX(match), app->prompt_match_protocol);
    gtk_box_append(GTK_BOX(page), match);
    GtkWidget *pending = scrolled_list(&app->page_lists[PAGE_PROMPTS]);
    gtk_widget_set_size_request(pending, -1, 180);
    gtk_box_append(GTK_BOX(page), pending);

    GtkWidget *silent_title = gtk_label_new("Silent Mode review");
    gtk_label_set_xalign(GTK_LABEL(silent_title), 0.0F);
    gtk_widget_add_css_class(silent_title, "title-3");
    gtk_box_append(GTK_BOX(page), silent_title);
    GtkWidget *silent_summary = gtk_label_new("Loading…");
    gtk_label_set_xalign(GTK_LABEL(silent_summary), 0.0F);
    gtk_widget_add_css_class(silent_summary, "dim-label");
    app->page_summaries[PAGE_SILENT_DECISIONS] = silent_summary;
    gtk_box_append(GTK_BOX(page), silent_summary);
    GtkWidget *silent = scrolled_list(
        &app->page_lists[PAGE_SILENT_DECISIONS]);
    gtk_widget_set_size_request(silent, -1, 180);
    gtk_box_append(GTK_BOX(page), silent);
    return page;
}

static GtkWidget *create_library_page(ClambhookLinuxApp *app) {
    GtkWidget *page = page_container(
        "Library", "Active listeners and configured proxy-chain servers.");
    GtkWidget *listeners_title = gtk_label_new("Listeners");
    gtk_label_set_xalign(GTK_LABEL(listeners_title), 0.0F);
    gtk_widget_add_css_class(listeners_title, "title-3");
    gtk_box_append(GTK_BOX(page), listeners_title);
    GtkWidget *listeners_scroll = scrolled_list(&app->listener_list);
    gtk_widget_set_size_request(listeners_scroll, -1, 180);
    gtk_widget_set_vexpand(listeners_scroll, FALSE);
    gtk_box_append(GTK_BOX(page), listeners_scroll);
    GtkWidget *servers_title = gtk_label_new("Servers");
    gtk_label_set_xalign(GTK_LABEL(servers_title), 0.0F);
    gtk_widget_add_css_class(servers_title, "title-3");
    gtk_box_append(GTK_BOX(page), servers_title);
    gtk_box_append(GTK_BOX(page), scrolled_list(
        &app->page_lists[PAGE_SERVERS]));
    app->page_summaries[PAGE_SERVERS] = gtk_label_new("");
    return page;
}

static GtkWidget *create_capture_page(ClambhookLinuxApp *app) {
    GtkWidget *page = create_data_page(
        app, PAGE_CAPTURES, "HTTP capture",
        "Opt-in bounded request and response inspection. Sensitive headers "
        "and configured query parameters are redacted by the daemon.", FALSE);
    GtkWidget *bar = gtk_box_new(GTK_ORIENTATION_HORIZONTAL, 8);
    app->capture_state_label = gtk_label_new("Capture status loading…");
    gtk_label_set_xalign(GTK_LABEL(app->capture_state_label), 0.0F);
    gtk_widget_set_hexpand(app->capture_state_label, TRUE);
    gtk_box_append(GTK_BOX(bar), app->capture_state_label);
    app->capture_clear_button = gtk_button_new_with_label("Clear");
    gtk_widget_set_tooltip_text(
        app->capture_clear_button, "Delete all bounded capture entries");
    g_signal_connect(app->capture_clear_button, "clicked",
                     G_CALLBACK(capture_clear_clicked), app);
    gtk_widget_set_sensitive(app->capture_clear_button, FALSE);
    gtk_box_append(GTK_BOX(bar), app->capture_clear_button);
    app->capture_import_button = gtk_button_new_with_label("Import cURL");
    gtk_widget_set_tooltip_text(
        app->capture_import_button,
        "Parse a cURL command without executing it or reading files");
    g_signal_connect(app->capture_import_button, "clicked",
                     G_CALLBACK(capture_import_clicked), app);
    gtk_box_append(GTK_BOX(bar), app->capture_import_button);
    app->capture_har_button = gtk_button_new_with_label("Export HAR");
    gtk_widget_set_tooltip_text(
        app->capture_har_button,
        "Save the bounded, redacted capture archive as HAR 1.2 JSON");
    g_signal_connect(app->capture_har_button, "clicked",
                     G_CALLBACK(capture_har_clicked), app);
    gtk_box_append(GTK_BOX(bar), app->capture_har_button);
    app->capture_toggle_button = gtk_button_new_with_label("Enable capture");
    g_signal_connect(app->capture_toggle_button, "clicked",
                     G_CALLBACK(capture_toggle_clicked), app);
    gtk_box_append(GTK_BOX(bar), app->capture_toggle_button);
    GtkWidget *title = gtk_widget_get_first_child(page);
    GtkWidget *description = gtk_widget_get_next_sibling(title);
    gtk_box_insert_child_after(GTK_BOX(page), bar, description);

    GtkWidget *filters = gtk_box_new(GTK_ORIENTATION_HORIZONTAL, 8);
    app->capture_query = gtk_search_entry_new();
    gtk_editable_set_text(GTK_EDITABLE(app->capture_query), "");
    gtk_widget_set_hexpand(app->capture_query, TRUE);
    gtk_widget_set_tooltip_text(
        app->capture_query, "Search method, URL, host, headers, and previews");
    gtk_box_append(GTK_BOX(filters), app->capture_query);
    app->capture_method = gtk_entry_new();
    gtk_entry_set_placeholder_text(GTK_ENTRY(app->capture_method),
                                   "GET,POST");
    gtk_widget_set_size_request(app->capture_method, 110, -1);
    gtk_widget_set_tooltip_text(
        app->capture_method, "Comma-separated HTTP methods");
    gtk_box_append(GTK_BOX(filters), app->capture_method);
    app->capture_error_only = gtk_check_button_new_with_label("Errors only");
    gtk_box_append(GTK_BOX(filters), app->capture_error_only);
    app->capture_filter_button = gtk_button_new_with_label("Filter");
    g_signal_connect(app->capture_filter_button, "clicked",
                     G_CALLBACK(capture_filter_clicked), app);
    gtk_box_append(GTK_BOX(filters), app->capture_filter_button);
    gtk_box_insert_child_after(GTK_BOX(page), filters, bar);
    g_signal_connect(app->page_lists[PAGE_CAPTURES], "row-activated",
                     G_CALLBACK(capture_row_activated), app);
    return page;
}

static void conditioner_grid_entry(GtkGrid *grid, int row,
                                   const char *label_text,
                                   const char *placeholder,
                                   GtkInputPurpose purpose,
                                   GtkWidget **entry_out) {
    GtkWidget *label = gtk_label_new_with_mnemonic(label_text);
    gtk_label_set_xalign(GTK_LABEL(label), 0.0F);
    GtkWidget *entry = gtk_entry_new();
    gtk_entry_set_placeholder_text(GTK_ENTRY(entry), placeholder);
    gtk_entry_set_input_purpose(GTK_ENTRY(entry), purpose);
    gtk_widget_set_hexpand(entry, TRUE);
    gtk_label_set_mnemonic_widget(GTK_LABEL(label), entry);
    gtk_grid_attach(grid, label, 0, row, 1, 1);
    gtk_grid_attach(grid, entry, 1, row, 1, 1);
    *entry_out = entry;
}

static GtkWidget *create_conditioner_page(ClambhookLinuxApp *app) {
    GtkWidget *page = create_data_page(
        app, PAGE_CONDITIONER, "Network conditioner",
        "Shape bandwidth, latency, jitter, and packet loss for the active "
        "profile. Changes are validated and persisted transactionally by "
        "the native C daemon.", FALSE);
    GtkWidget *editor = gtk_box_new(GTK_ORIENTATION_VERTICAL, 12);
    app->conditioner_editor = editor;
    gtk_widget_add_css_class(editor, "card");
    gtk_widget_set_margin_top(editor, 4);
    gtk_widget_set_margin_bottom(editor, 4);
    gtk_widget_set_margin_start(editor, 2);
    gtk_widget_set_margin_end(editor, 2);

    GtkWidget *header = gtk_box_new(GTK_ORIENTATION_HORIZONTAL, 10);
    app->conditioner_profile_label = gtk_label_new("Profile: loading…");
    gtk_label_set_xalign(GTK_LABEL(app->conditioner_profile_label), 0.0F);
    gtk_widget_set_hexpand(app->conditioner_profile_label, TRUE);
    gtk_widget_add_css_class(app->conditioner_profile_label, "heading");
    gtk_box_append(GTK_BOX(header), app->conditioner_profile_label);
    app->conditioner_enabled = gtk_check_button_new_with_label("Enabled");
    gtk_widget_set_tooltip_text(
        app->conditioner_enabled,
        "Apply the configured network conditions to eligible traffic");
    gtk_box_append(GTK_BOX(header), app->conditioner_enabled);
    gtk_box_append(GTK_BOX(editor), header);

    GtkWidget *grid_widget = gtk_grid_new();
    GtkGrid *grid = GTK_GRID(grid_widget);
    gtk_grid_set_row_spacing(grid, 8);
    gtk_grid_set_column_spacing(grid, 12);
    conditioner_grid_entry(grid, 0, "_Download (Kbps)", "Unlimited",
                           GTK_INPUT_PURPOSE_DIGITS,
                           &app->conditioner_download);
    conditioner_grid_entry(grid, 1, "_Upload (Kbps)", "Unlimited",
                           GTK_INPUT_PURPOSE_DIGITS,
                           &app->conditioner_upload);
    conditioner_grid_entry(grid, 2, "_Latency", "40ms",
                           GTK_INPUT_PURPOSE_FREE_FORM,
                           &app->conditioner_latency);
    conditioner_grid_entry(grid, 3, "_Jitter", "5ms",
                           GTK_INPUT_PURPOSE_FREE_FORM,
                           &app->conditioner_jitter);
    conditioner_grid_entry(grid, 4, "_Loss (%)", "0–100",
                           GTK_INPUT_PURPOSE_NUMBER,
                           &app->conditioner_loss);
    gtk_box_append(GTK_BOX(editor), grid_widget);

    GtkWidget *hint = gtk_label_new(
        "Leave bandwidth and loss empty for zero. Leave latency or jitter "
        "empty to remove that delay.");
    gtk_label_set_xalign(GTK_LABEL(hint), 0.0F);
    gtk_label_set_wrap(GTK_LABEL(hint), TRUE);
    gtk_widget_add_css_class(hint, "dim-label");
    gtk_box_append(GTK_BOX(editor), hint);
    app->conditioner_save_button = gtk_button_new_with_label(
        "Save conditioner");
    gtk_widget_add_css_class(app->conditioner_save_button,
                             "suggested-action");
    gtk_widget_set_halign(app->conditioner_save_button, GTK_ALIGN_END);
    g_signal_connect(app->conditioner_save_button, "clicked",
                     G_CALLBACK(conditioner_save_clicked), app);
    gtk_box_append(GTK_BOX(editor), app->conditioner_save_button);

    GtkWidget *title = gtk_widget_get_first_child(page);
    GtkWidget *description = gtk_widget_get_next_sibling(title);
    gtk_box_insert_child_after(GTK_BOX(page), editor, description);
    return page;
}

static GtkWidget *create_dns_page(ClambhookLinuxApp *app) {
    GtkWidget *page = create_data_page(
        app, PAGE_DNS, "Encrypted DNS",
        "Configure ordered route-aware encrypted DNS upstreams for the "
        "active profile. The native C daemon validates and persists changes "
        "before rebuilding the live DNS proxy.", FALSE);
    GtkWidget *editor = gtk_box_new(GTK_ORIENTATION_VERTICAL, 10);
    app->dns_editor = editor;
    gtk_widget_add_css_class(editor, "card");
    gtk_widget_set_margin_top(editor, 4);
    gtk_widget_set_margin_bottom(editor, 4);
    gtk_widget_set_margin_start(editor, 2);
    gtk_widget_set_margin_end(editor, 2);

    GtkWidget *header = gtk_box_new(GTK_ORIENTATION_HORIZONTAL, 10);
    app->dns_profile_label = gtk_label_new("Profile: loading…");
    gtk_label_set_xalign(GTK_LABEL(app->dns_profile_label), 0.0F);
    gtk_widget_set_hexpand(app->dns_profile_label, TRUE);
    gtk_widget_add_css_class(app->dns_profile_label, "heading");
    gtk_box_append(GTK_BOX(header), app->dns_profile_label);
    app->dns_enabled = gtk_check_button_new_with_label("Encrypted DNS");
    gtk_widget_set_tooltip_text(
        app->dns_enabled,
        "Intercept DNS traffic and use the configured encrypted upstreams");
    gtk_box_append(GTK_BOX(header), app->dns_enabled);
    gtk_box_append(GTK_BOX(editor), header);

    GtkWidget *timeout_row = gtk_box_new(GTK_ORIENTATION_HORIZONTAL, 12);
    GtkWidget *timeout_label = gtk_label_new_with_mnemonic("_Timeout");
    gtk_label_set_xalign(GTK_LABEL(timeout_label), 0.0F);
    app->dns_timeout = gtk_entry_new();
    gtk_entry_set_placeholder_text(GTK_ENTRY(app->dns_timeout), "5s");
    gtk_widget_set_hexpand(app->dns_timeout, TRUE);
    gtk_label_set_mnemonic_widget(GTK_LABEL(timeout_label), app->dns_timeout);
    gtk_box_append(GTK_BOX(timeout_row), timeout_label);
    gtk_box_append(GTK_BOX(timeout_row), app->dns_timeout);
    gtk_box_append(GTK_BOX(editor), timeout_row);

    GtkWidget *upstreams_label = gtk_label_new_with_mnemonic(
        "_Ordered upstreams (JSON array)");
    gtk_label_set_xalign(GTK_LABEL(upstreams_label), 0.0F);
    gtk_box_append(GTK_BOX(editor), upstreams_label);
    GtkWidget *upstreams_scroll = gtk_scrolled_window_new();
    gtk_scrolled_window_set_policy(GTK_SCROLLED_WINDOW(upstreams_scroll),
                                   GTK_POLICY_AUTOMATIC,
                                   GTK_POLICY_AUTOMATIC);
    gtk_widget_set_size_request(upstreams_scroll, -1, 180);
    GtkWidget *upstreams_view = gtk_text_view_new();
    gtk_text_view_set_wrap_mode(GTK_TEXT_VIEW(upstreams_view),
                                GTK_WRAP_NONE);
    gtk_widget_add_css_class(upstreams_view, "monospace");
    app->dns_upstreams = gtk_text_view_get_buffer(
        GTK_TEXT_VIEW(upstreams_view));
    gtk_label_set_mnemonic_widget(GTK_LABEL(upstreams_label),
                                  upstreams_view);
    gtk_scrolled_window_set_child(GTK_SCROLLED_WINDOW(upstreams_scroll),
                                  upstreams_view);
    gtk_box_append(GTK_BOX(editor), upstreams_scroll);
    GtkWidget *hint = gtk_label_new(
        "Each object may define name, protocol, URL or address, TLS server "
        "name, bootstrap addresses, and chain. Keep [] when encrypted DNS "
        "is disabled.");
    gtk_label_set_xalign(GTK_LABEL(hint), 0.0F);
    gtk_label_set_wrap(GTK_LABEL(hint), TRUE);
    gtk_widget_add_css_class(hint, "dim-label");
    gtk_box_append(GTK_BOX(editor), hint);
    app->dns_save_button = gtk_button_new_with_label("Save DNS settings");
    gtk_widget_add_css_class(app->dns_save_button, "suggested-action");
    gtk_widget_set_halign(app->dns_save_button, GTK_ALIGN_END);
    g_signal_connect(app->dns_save_button, "clicked",
                     G_CALLBACK(dns_save_clicked), app);
    gtk_box_append(GTK_BOX(editor), app->dns_save_button);

    GtkWidget *title = gtk_widget_get_first_child(page);
    GtkWidget *description = gtk_widget_get_next_sibling(title);
    gtk_box_insert_child_after(GTK_BOX(page), editor, description);
    return page;
}

static GtkWidget *create_information_page(const char *title,
                                          const char *description,
                                          const char *body) {
    GtkWidget *page = page_container(title, description);
    GtkWidget *label = gtk_label_new(body);
    gtk_label_set_xalign(GTK_LABEL(label), 0.0F);
    gtk_label_set_yalign(GTK_LABEL(label), 0.0F);
    gtk_label_set_wrap(GTK_LABEL(label), TRUE);
    gtk_label_set_selectable(GTK_LABEL(label), TRUE);
    gtk_box_append(GTK_BOX(page), label);
    return page;
}

static void add_stack_page(GtkStack *stack, GtkWidget *page,
                           const char *name, const char *title) {
    gtk_stack_add_titled(stack, page, name, title);
}

static void activate(GtkApplication *application, gpointer user_data) {
    ClambhookLinuxApp *app = user_data;
    if (app->window != NULL) {
        gtk_window_present(GTK_WINDOW(app->window));
        return;
    }
    app->window = gtk_application_window_new(application);
    gtk_window_set_title(GTK_WINDOW(app->window), "ClambHook");
    gtk_window_set_default_size(GTK_WINDOW(app->window), 1080, 760);

    GtkWidget *header = gtk_header_bar_new();
    GtkWidget *profile_label = gtk_label_new("Profile");
    gtk_header_bar_pack_start(GTK_HEADER_BAR(header), profile_label);
    app->profile_dropdown = gtk_drop_down_new(NULL, NULL);
    gtk_widget_set_tooltip_text(app->profile_dropdown,
                                "Switch active ClambHook profile");
    g_signal_connect(app->profile_dropdown, "notify::selected",
                     G_CALLBACK(profile_selected), app);
    gtk_header_bar_pack_start(GTK_HEADER_BAR(header), app->profile_dropdown);
    app->refresh_button = gtk_button_new_from_icon_name(
        "view-refresh-symbolic");
    gtk_widget_set_tooltip_text(app->refresh_button,
                                "Refresh all dashboard pages");
    g_signal_connect(app->refresh_button, "clicked",
                     G_CALLBACK(refresh_clicked), app);
    gtk_header_bar_pack_end(GTK_HEADER_BAR(header), app->refresh_button);
    app->spinner = gtk_spinner_new();
    gtk_widget_set_visible(app->spinner, FALSE);
    gtk_header_bar_pack_end(GTK_HEADER_BAR(header), app->spinner);
    gtk_window_set_titlebar(GTK_WINDOW(app->window), header);

    GtkWidget *root = gtk_box_new(GTK_ORIENTATION_VERTICAL, 0);
    GtkWidget *workspace = gtk_box_new(GTK_ORIENTATION_HORIZONTAL, 0);
    gtk_widget_set_vexpand(workspace, TRUE);
    GtkWidget *stack_widget = gtk_stack_new();
    GtkStack *stack = GTK_STACK(stack_widget);
    gtk_stack_set_transition_type(stack, GTK_STACK_TRANSITION_TYPE_CROSSFADE);
    gtk_widget_set_hexpand(stack_widget, TRUE);
    GtkWidget *sidebar = gtk_stack_sidebar_new();
    gtk_stack_sidebar_set_stack(GTK_STACK_SIDEBAR(sidebar), stack);
    gtk_widget_set_size_request(sidebar, 180, -1);
    gtk_widget_add_css_class(sidebar, "navigation-sidebar");
    gtk_box_append(GTK_BOX(workspace), sidebar);
    gtk_box_append(GTK_BOX(workspace), stack_widget);
    gtk_box_append(GTK_BOX(root), workspace);

    add_stack_page(stack, create_now_page(app), "now", "Now");
    add_stack_page(stack, create_activity_page(app), "activity", "Activity");
    add_stack_page(stack, create_data_page(
        app, PAGE_POLICIES, "Policy groups",
        "Manual selection and latency-tested automatic policy groups.", TRUE),
        "policies", "Policies");
    add_stack_page(stack, create_prompt_page(app), "firewall", "Firewall");
    add_stack_page(stack, create_dns_page(app), "dns", "DNS");
    add_stack_page(stack, create_capture_page(app), "capture", "Capture");
    add_stack_page(stack, create_conditioner_page(app),
                   "conditioner", "Conditioner");
    add_stack_page(stack, create_library_page(app), "library", "Library");
    add_stack_page(stack, create_information_page(
        "License", "ClambHook licensing and trial state.",
        "License activation remains managed by the native clambhook-license-c "
        "helper during this additive GTK migration. No license key is placed "
        "in command-line arguments or dashboard logs."),
        "license", "License");
    g_autofree char *settings_text = g_strdup_printf(
        "Daemon API: %s\nAuthentication token: %s\n\nSet CLAMBHOOK_API_URL "
        "and CLAMBHOOK_API_TOKEN before launch. The token is sent only as a "
        "Bearer header to the configured loopback API.",
        app->api_url, app->api_token[0] == '\0' ? "not configured" :
                                                  "configured");
    add_stack_page(stack, create_information_page(
        "Settings", "Native GTK connection settings.", settings_text),
        "settings", "Settings");

    app->error_label = gtk_label_new("");
    gtk_label_set_xalign(GTK_LABEL(app->error_label), 0.0F);
    gtk_label_set_wrap(GTK_LABEL(app->error_label), TRUE);
    gtk_widget_set_margin_top(app->error_label, 8);
    gtk_widget_set_margin_bottom(app->error_label, 8);
    gtk_widget_set_margin_start(app->error_label, 12);
    gtk_widget_set_margin_end(app->error_label, 12);
    gtk_widget_add_css_class(app->error_label, "error");
    gtk_widget_set_visible(app->error_label, FALSE);
    gtk_box_append(GTK_BOX(root), app->error_label);
    gtk_window_set_child(GTK_WINDOW(app->window), root);
    gtk_window_present(GTK_WINDOW(app->window));
    refresh_all(app);
    start_event_stream(app);
}

static void shutdown_application(GApplication *application,
                                 gpointer user_data) {
    (void)application;
    ClambhookLinuxApp *app = user_data;
    app->shutting_down = TRUE;
    if (app->event_refresh_source != 0U) {
        g_source_remove(app->event_refresh_source);
        app->event_refresh_source = 0U;
    }
    if (app->event_reconnect_source != 0U) {
        g_source_remove(app->event_reconnect_source);
        app->event_reconnect_source = 0U;
    }
    if (app->event_connection != NULL) {
        soup_websocket_connection_close(
            app->event_connection, SOUP_WEBSOCKET_CLOSE_NORMAL,
            "application closing");
        g_clear_object(&app->event_connection);
    }
    soup_session_abort(app->session);
}

int main(int argc, char **argv) {
    if (argc == 2 && strcmp(argv[1], "--version") == 0) {
        g_print("clambhook-linux %s\n", CLAMBHOOK_VERSION);
        return EXIT_SUCCESS;
    }
    const char *configured_url = g_getenv("CLAMBHOOK_API_URL");
    const char *configured_token = g_getenv("CLAMBHOOK_API_TOKEN");
    ClambhookLinuxApp app = {
        .api_url = g_strdup(configured_url == NULL ||
                            configured_url[0] == '\0' ?
            "http://127.0.0.1:9090" : configured_url),
        .api_token = g_strdup(configured_token == NULL ? "" :
                                                       configured_token),
        .session = soup_session_new()
    };
    app.application = gtk_application_new(
        "com.clambhook.Clambhook", G_APPLICATION_DEFAULT_FLAGS);
    g_signal_connect(app.application, "activate", G_CALLBACK(activate), &app);
    g_signal_connect(app.application, "shutdown",
                     G_CALLBACK(shutdown_application), &app);
    int status = g_application_run(
        G_APPLICATION(app.application), argc, argv);
    g_clear_object(&app.session);
    g_clear_object(&app.application);
    g_free(app.api_url);
    g_free(app.api_token);
    g_free(app.conditioner_profile);
    g_free(app.dns_profile);
    return status;
}
