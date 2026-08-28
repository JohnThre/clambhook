// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

#include "model.h"

#include <json-glib/json-glib.h>

#include <errno.h>
#include <math.h>
#include <string.h>

static GQuark ch_gtk_model_error_quark(void) {
    return g_quark_from_static_string("clambhook-linux-gtk-model");
}

static const char *object_string(JsonObject *object, const char *name,
                                 const char *fallback) {
    if (object == NULL || !json_object_has_member(object, name)) {
        return fallback;
    }
    JsonNode *node = json_object_get_member(object, name);
    return node != NULL && JSON_NODE_HOLDS_VALUE(node) &&
        json_node_get_value_type(node) == G_TYPE_STRING ?
        json_node_get_string(node) : fallback;
}

static gboolean object_boolean(JsonObject *object, const char *name,
                               gboolean fallback) {
    if (object == NULL || !json_object_has_member(object, name)) {
        return fallback;
    }
    JsonNode *node = json_object_get_member(object, name);
    return node != NULL && JSON_NODE_HOLDS_VALUE(node) &&
        json_node_get_value_type(node) == G_TYPE_BOOLEAN ?
        json_node_get_boolean(node) : fallback;
}

static double object_number(JsonObject *object, const char *name,
                            double fallback) {
    if (object == NULL || !json_object_has_member(object, name)) {
        return fallback;
    }
    JsonNode *node = json_object_get_member(object, name);
    if (node == NULL || !JSON_NODE_HOLDS_VALUE(node)) return fallback;
    GType type = json_node_get_value_type(node);
    return type == G_TYPE_DOUBLE || type == G_TYPE_FLOAT ||
        type == G_TYPE_INT64 || type == G_TYPE_UINT64 ||
        type == G_TYPE_INT || type == G_TYPE_UINT ?
        json_node_get_double(node) : fallback;
}

static guint64 object_uint64(JsonObject *object, const char *name) {
    double value = object_number(object, name, 0.0);
    if (value <= 0.0 || !isfinite(value)) return 0U;
    return value >= (double)G_MAXUINT64 ? G_MAXUINT64 : (guint64)value;
}

static JsonArray *object_array(JsonObject *object, const char *name) {
    if (object == NULL || !json_object_has_member(object, name)) return NULL;
    JsonNode *node = json_object_get_member(object, name);
    return node != NULL && JSON_NODE_HOLDS_ARRAY(node) ?
        json_node_get_array(node) : NULL;
}

static JsonObject *array_object(JsonArray *array, guint index) {
    JsonNode *node = array == NULL ? NULL :
        json_array_get_element(array, index);
    return node != NULL && JSON_NODE_HOLDS_OBJECT(node) ?
        json_node_get_object(node) : NULL;
}

static JsonObject *object_object(JsonObject *object, const char *name) {
    if (object == NULL || !json_object_has_member(object, name)) return NULL;
    JsonNode *node = json_object_get_member(object, name);
    return node != NULL && JSON_NODE_HOLDS_OBJECT(node) ?
        json_node_get_object(node) : NULL;
}

static guint array_length(JsonArray *array) {
    return array == NULL ? 0U : json_array_get_length(array);
}

static JsonObject *parse_root(const guint8 *data, gsize length,
                              JsonParser **out_parser, GError **error) {
    JsonParser *parser = json_parser_new();
    if (!json_parser_load_from_data(parser, (const char *)data,
                                    (gssize)length, error)) {
        g_object_unref(parser);
        return NULL;
    }
    JsonNode *root = json_parser_get_root(parser);
    if (root == NULL || !JSON_NODE_HOLDS_OBJECT(root)) {
        g_set_error_literal(error, ch_gtk_model_error_quark(), 1,
                            "daemon payload must be a JSON object");
        g_object_unref(parser);
        return NULL;
    }
    *out_parser = parser;
    return json_node_get_object(root);
}

void ch_gtk_row_free(ch_gtk_row *row) {
    if (row == NULL) return;
    g_free(row->title);
    g_free(row->detail);
    g_free(row->identifier);
    g_free(row->selected);
    g_clear_pointer(&row->options, g_ptr_array_unref);
    g_free(row);
}

void ch_gtk_status_model_clear(ch_gtk_status_model *model) {
    if (model == NULL) return;
    g_free(model->profile);
    g_free(model->mode);
    g_clear_pointer(&model->listeners, g_ptr_array_unref);
    memset(model, 0, sizeof(*model));
}

void ch_gtk_profiles_model_clear(ch_gtk_profiles_model *model) {
    if (model == NULL) return;
    g_free(model->active);
    g_clear_pointer(&model->names, g_ptr_array_unref);
    memset(model, 0, sizeof(*model));
}

void ch_gtk_traffic_model_clear(ch_gtk_traffic_model *model) {
    if (model == NULL) return;
    g_clear_pointer(&model->rows, g_ptr_array_unref);
    memset(model, 0, sizeof(*model));
}

void ch_gtk_capture_detail_clear(ch_gtk_capture_detail *detail) {
    if (detail == NULL) return;
    g_free(detail->identifier);
    g_free(detail->method);
    g_free(detail->url);
    g_free(detail->host);
    g_free(detail->profile);
    g_free(detail->chain);
    g_free(detail->started_at);
    g_free(detail->finished_at);
    g_free(detail->error_message);
    g_free(detail->request_headers);
    g_free(detail->request_body);
    g_free(detail->response_headers);
    g_free(detail->response_body);
    memset(detail, 0, sizeof(*detail));
}

void ch_gtk_conditioner_model_clear(ch_gtk_conditioner_model *model) {
    if (model == NULL) return;
    g_free(model->profile);
    g_free(model->latency);
    g_free(model->jitter);
    memset(model, 0, sizeof(*model));
}

void ch_gtk_dns_model_clear(ch_gtk_dns_model *model) {
    if (model == NULL) return;
    g_free(model->profile);
    g_free(model->timeout);
    g_free(model->upstreams_json);
    memset(model, 0, sizeof(*model));
}

void ch_gtk_curl_import_clear(ch_gtk_curl_import *model) {
    if (model == NULL) return;
    g_free(model->method);
    g_free(model->url);
    g_free(model->headers);
    g_free(model->body);
    memset(model, 0, sizeof(*model));
}

void ch_gtk_license_state_clear(ch_gtk_license_state *state) {
    if (state == NULL) return;
    g_free(state->install_id);
    g_free(state->email);
    g_free(state->snapshot_json);
    g_free(state->grant_json);
    g_free(state->device_state_json);
    memset(state, 0, sizeof(*state));
}

void ch_gtk_license_view_clear(ch_gtk_license_view *view) {
    if (view == NULL) return;
    g_free(view->title);
    g_free(view->detail);
    g_free(view->current_device_id);
    g_clear_pointer(&view->devices, g_ptr_array_unref);
    memset(view, 0, sizeof(*view));
}

char *ch_gtk_format_bytes(guint64 value) {
    static const char *const units[] = {"B", "KiB", "MiB", "GiB", "TiB"};
    double scaled = (double)value;
    size_t unit = 0U;
    while (scaled >= 1024.0 && unit + 1U < G_N_ELEMENTS(units)) {
        scaled /= 1024.0;
        ++unit;
    }
    return unit == 0U ? g_strdup_printf("%" G_GUINT64_FORMAT " %s", value,
                                        units[unit]) :
        g_strdup_printf("%.1f %s", scaled, units[unit]);
}

char *ch_gtk_format_rate(double value) {
    if (!isfinite(value) || value < 0.0) value = 0.0;
    g_autofree char *bytes = ch_gtk_format_bytes((guint64)value);
    return g_strdup_printf("%s/s", bytes);
}

static char *builder_json(JsonBuilder *builder) {
    g_autoptr(JsonGenerator) generator = json_generator_new();
    g_autoptr(JsonNode) root = json_builder_get_root(builder);
    json_generator_set_root(generator, root);
    return json_generator_to_data(generator, NULL);
}

char *ch_gtk_profile_body(const char *name) {
    g_autoptr(JsonBuilder) builder = json_builder_new();
    json_builder_begin_object(builder);
    json_builder_set_member_name(builder, "name");
    json_builder_add_string_value(builder, name);
    json_builder_end_object(builder);
    return builder_json(builder);
}

char *ch_gtk_policy_selection_body(const char *group, const char *chain) {
    g_autoptr(JsonBuilder) builder = json_builder_new();
    json_builder_begin_object(builder);
    json_builder_set_member_name(builder, "group");
    json_builder_add_string_value(builder, group);
    json_builder_set_member_name(builder, "chain");
    json_builder_add_string_value(builder, chain);
    json_builder_end_object(builder);
    return builder_json(builder);
}

char *ch_gtk_prompt_resolution_body(const char *action, const char *scope,
                                    gboolean match_host,
                                    gboolean match_port,
                                    gboolean match_protocol) {
    g_autoptr(JsonBuilder) builder = json_builder_new();
    json_builder_begin_object(builder);
    json_builder_set_member_name(builder, "action");
    json_builder_add_string_value(builder, action);
    json_builder_set_member_name(builder, "scope");
    json_builder_add_string_value(builder, scope);
    json_builder_set_member_name(builder, "match_host");
    json_builder_add_boolean_value(builder, match_host);
    json_builder_set_member_name(builder, "match_port");
    json_builder_add_boolean_value(builder, match_port);
    json_builder_set_member_name(builder, "match_protocol");
    json_builder_add_boolean_value(builder, match_protocol);
    json_builder_end_object(builder);
    return builder_json(builder);
}

char *ch_gtk_silent_promotion_body(const char *scope,
                                   gboolean match_host,
                                   gboolean match_port,
                                   gboolean match_protocol) {
    g_autoptr(JsonBuilder) builder = json_builder_new();
    json_builder_begin_object(builder);
    json_builder_set_member_name(builder, "scope");
    json_builder_add_string_value(builder, scope);
    json_builder_set_member_name(builder, "match_host");
    json_builder_add_boolean_value(builder, match_host);
    json_builder_set_member_name(builder, "match_port");
    json_builder_add_boolean_value(builder, match_port);
    json_builder_set_member_name(builder, "match_protocol");
    json_builder_add_boolean_value(builder, match_protocol);
    json_builder_end_object(builder);
    return builder_json(builder);
}

char *ch_gtk_capture_enabled_body(gboolean enabled) {
    g_autoptr(JsonBuilder) builder = json_builder_new();
    json_builder_begin_object(builder);
    json_builder_set_member_name(builder, "enabled");
    json_builder_add_boolean_value(builder, enabled);
    json_builder_end_object(builder);
    return builder_json(builder);
}

static gboolean builder_add_csv_strings(JsonBuilder *builder,
                                        const char *name,
                                        const char *text) {
    json_builder_set_member_name(builder, name);
    json_builder_begin_array(builder);
    g_auto(GStrv) values = g_strsplit(text == NULL ? "" : text, ",", -1);
    for (gsize index = 0U; values[index] != NULL; ++index) {
        g_strstrip(values[index]);
        if (values[index][0] != '\0') {
            json_builder_add_string_value(builder, values[index]);
        }
    }
    json_builder_end_array(builder);
    return TRUE;
}

static gboolean builder_add_csv_ports(JsonBuilder *builder, const char *text,
                                      GError **error) {
    json_builder_set_member_name(builder, "ports");
    json_builder_begin_array(builder);
    g_auto(GStrv) values = g_strsplit(text == NULL ? "" : text, ",", -1);
    for (gsize index = 0U; values[index] != NULL; ++index) {
        g_strstrip(values[index]);
        if (values[index][0] == '\0') continue;
        errno = 0;
        char *end = NULL;
        guint64 port = g_ascii_strtoull(values[index], &end, 10);
        if (errno == ERANGE || end == values[index] || *end != '\0' ||
            port == 0U || port > 65535U || values[index][0] == '-') {
            g_set_error(error, ch_gtk_model_error_quark(), 2,
                        "port %s must be between 1 and 65535", values[index]);
            return FALSE;
        }
        json_builder_add_int_value(builder, (gint64)port);
    }
    json_builder_end_array(builder);
    return TRUE;
}

char *ch_gtk_rule_create_body(const char *name, const char *action,
                              const char *domains,
                              const char *domain_suffixes,
                              const char *domain_keywords,
                              const char *cidrs, const char *ports,
                              const char *networks, gboolean prepend,
                              GError **error) {
    g_autofree char *trimmed_name = g_strdup(name == NULL ? "" : name);
    g_autofree char *trimmed_action = g_strdup(action == NULL ? "" : action);
    g_strstrip(trimmed_name);
    g_strstrip(trimmed_action);
    if (trimmed_name[0] == '\0' || trimmed_action[0] == '\0') {
        g_set_error_literal(error, ch_gtk_model_error_quark(), 2,
                            "rule name and action are required");
        return NULL;
    }
    g_autoptr(JsonBuilder) builder = json_builder_new();
    json_builder_begin_object(builder);
    json_builder_set_member_name(builder, "rule");
    json_builder_begin_object(builder);
    json_builder_set_member_name(builder, "name");
    json_builder_add_string_value(builder, trimmed_name);
    json_builder_set_member_name(builder, "action");
    json_builder_add_string_value(builder, trimmed_action);
    builder_add_csv_strings(builder, "domains", domains);
    builder_add_csv_strings(builder, "domain_suffixes", domain_suffixes);
    builder_add_csv_strings(builder, "domain_keywords", domain_keywords);
    builder_add_csv_strings(builder, "cidrs", cidrs);
    if (!builder_add_csv_ports(builder, ports, error)) return NULL;
    builder_add_csv_strings(builder, "networks", networks);
    json_builder_end_object(builder);
    json_builder_set_member_name(builder, "position");
    json_builder_add_string_value(builder, prepend ? "prepend" : "append");
    json_builder_end_object(builder);
    return builder_json(builder);
}

static gboolean parse_nonnegative_integer(const char *text,
                                           const char *field,
                                           gint64 *out,
                                           GError **error) {
    g_autofree char *trimmed = g_strdup(text == NULL ? "" : text);
    g_strstrip(trimmed);
    if (trimmed[0] == '\0') {
        *out = 0;
        return TRUE;
    }
    errno = 0;
    char *end = NULL;
    guint64 value = g_ascii_strtoull(trimmed, &end, 10);
    if (errno == ERANGE || end == trimmed || *end != '\0' ||
        value > (guint64)G_MAXINT64 || trimmed[0] == '-') {
        g_set_error(error, ch_gtk_model_error_quark(), 2,
                    "%s must be a non-negative integer", field);
        return FALSE;
    }
    *out = (gint64)value;
    return TRUE;
}

static gboolean parse_loss_percent(const char *text, double *out,
                                   GError **error) {
    g_autofree char *trimmed = g_strdup(text == NULL ? "" : text);
    g_strstrip(trimmed);
    if (trimmed[0] == '\0') {
        *out = 0.0;
        return TRUE;
    }
    errno = 0;
    char *end = NULL;
    double value = g_ascii_strtod(trimmed, &end);
    if (errno == ERANGE || end == trimmed || *end != '\0' ||
        !isfinite(value) || value < 0.0 || value > 100.0) {
        g_set_error_literal(error, ch_gtk_model_error_quark(), 2,
                            "loss percent must be between 0 and 100");
        return FALSE;
    }
    *out = value;
    return TRUE;
}

char *ch_gtk_conditioner_body(const char *profile, gboolean enabled,
                              const char *download_kbps,
                              const char *upload_kbps,
                              const char *latency, const char *jitter,
                              const char *loss_percent, GError **error) {
    gint64 download = 0;
    gint64 upload = 0;
    double loss = 0.0;
    if (!parse_nonnegative_integer(download_kbps, "download Kbps",
                                   &download, error) ||
        !parse_nonnegative_integer(upload_kbps, "upload Kbps", &upload,
                                   error) ||
        !parse_loss_percent(loss_percent, &loss, error)) {
        return NULL;
    }
    g_autofree char *latency_value = g_strdup(latency == NULL ? "" : latency);
    g_autofree char *jitter_value = g_strdup(jitter == NULL ? "" : jitter);
    g_strstrip(latency_value);
    g_strstrip(jitter_value);
    g_autoptr(JsonBuilder) builder = json_builder_new();
    json_builder_begin_object(builder);
    if (profile != NULL && profile[0] != '\0') {
        json_builder_set_member_name(builder, "profile");
        json_builder_add_string_value(builder, profile);
    }
    json_builder_set_member_name(builder, "enabled");
    json_builder_add_boolean_value(builder, enabled);
    json_builder_set_member_name(builder, "download_kbps");
    json_builder_add_int_value(builder, download);
    json_builder_set_member_name(builder, "upload_kbps");
    json_builder_add_int_value(builder, upload);
    json_builder_set_member_name(builder, "latency");
    json_builder_add_string_value(builder, latency_value);
    json_builder_set_member_name(builder, "jitter");
    json_builder_add_string_value(builder, jitter_value);
    json_builder_set_member_name(builder, "loss_percent");
    json_builder_add_double_value(builder, loss);
    json_builder_end_object(builder);
    return builder_json(builder);
}

char *ch_gtk_dns_body(const char *profile, gboolean enabled,
                      const char *timeout, const char *upstreams_json,
                      GError **error) {
    g_autofree char *timeout_value = g_strdup(timeout == NULL ? "" : timeout);
    g_strstrip(timeout_value);
    g_autoptr(JsonParser) parser = json_parser_new();
    const char *upstreams = upstreams_json == NULL ||
        upstreams_json[0] == '\0' ? "[]" : upstreams_json;
    if (!json_parser_load_from_data(parser, upstreams, -1, error)) return NULL;
    JsonNode *upstream_root = json_parser_get_root(parser);
    if (upstream_root == NULL || !JSON_NODE_HOLDS_ARRAY(upstream_root)) {
        g_set_error_literal(error, ch_gtk_model_error_quark(), 2,
                            "DNS upstreams must be a JSON array");
        return NULL;
    }
    g_autoptr(JsonBuilder) builder = json_builder_new();
    json_builder_begin_object(builder);
    if (profile != NULL && profile[0] != '\0') {
        json_builder_set_member_name(builder, "profile");
        json_builder_add_string_value(builder, profile);
    }
    json_builder_set_member_name(builder, "enabled");
    json_builder_add_boolean_value(builder, enabled);
    json_builder_set_member_name(builder, "timeout");
    json_builder_add_string_value(builder, timeout_value);
    json_builder_set_member_name(builder, "upstreams");
    json_builder_add_value(builder, json_node_copy(upstream_root));
    json_builder_end_object(builder);
    return builder_json(builder);
}

char *ch_gtk_curl_import_body(const char *command) {
    g_autoptr(JsonBuilder) builder = json_builder_new();
    json_builder_begin_object(builder);
    json_builder_set_member_name(builder, "curl");
    json_builder_add_string_value(builder, command == NULL ? "" : command);
    json_builder_end_object(builder);
    return builder_json(builder);
}

char *ch_gtk_prompt_resolution_path(const char *identifier) {
    g_autofree char *escaped = g_uri_escape_string(identifier, NULL, FALSE);
    return g_strdup_printf("/api/v1/prompts/%s/resolve", escaped);
}

char *ch_gtk_silent_promotion_path(const char *identifier) {
    g_autofree char *escaped = g_uri_escape_string(identifier, NULL, FALSE);
    return g_strdup_printf(
        "/api/v1/prompts/decisions/%s/promote", escaped);
}

char *ch_gtk_capture_entries_path(const char *query, const char *method,
                                  gboolean error_only, guint limit) {
    g_autofree char *escaped_query = g_uri_escape_string(
        query == NULL ? "" : query, NULL, FALSE);
    g_autofree char *escaped_method = g_uri_escape_string(
        method == NULL ? "" : method, NULL, FALSE);
    GString *path = g_string_new("/api/v1/developer/entries?limit=");
    g_string_append_printf(path, "%u", limit);
    if (escaped_method[0] != '\0') {
        g_string_append_printf(path, "&method=%s", escaped_method);
    }
    if (escaped_query[0] != '\0') {
        g_string_append_printf(path, "&q=%s", escaped_query);
    }
    if (error_only) g_string_append(path, "&error_only=1");
    return g_string_free(path, FALSE);
}

char *ch_gtk_capture_detail_path(const char *identifier) {
    g_autofree char *escaped = g_uri_escape_string(identifier, NULL, FALSE);
    return g_strdup_printf("/api/v1/developer/entries/%s", escaped);
}

char *ch_gtk_capture_curl_path(const char *identifier) {
    g_autofree char *escaped = g_uri_escape_string(identifier, NULL, FALSE);
    return g_strdup_printf("/api/v1/developer/entries/%s/curl", escaped);
}

static ch_gtk_row *row_new(const char *title, const char *detail,
                           const char *identifier) {
    ch_gtk_row *row = g_new0(ch_gtk_row, 1U);
    row->title = g_strdup(title == NULL || title[0] == '\0' ? "—" : title);
    row->detail = g_strdup(detail == NULL ? "" : detail);
    row->identifier = g_strdup(identifier == NULL ? "" : identifier);
    row->selected = g_strdup("");
    return row;
}

gboolean ch_gtk_parse_status(const guint8 *data, gsize length,
                             ch_gtk_status_model *out, GError **error) {
    memset(out, 0, sizeof(*out));
    JsonParser *parser = NULL;
    JsonObject *root = parse_root(data, length, &parser, error);
    if (root == NULL) return FALSE;
    out->running = object_boolean(root, "running", FALSE);
    out->profile = g_strdup(object_string(root, "profile", ""));
    out->mode = g_strdup(object_string(
        root, "tunnel_mode", out->running ? "proxy" : ""));
    out->listeners = g_ptr_array_new_with_free_func(g_free);
    JsonArray *listeners = object_array(root, "listeners");
    for (guint index = 0U; index < array_length(listeners); ++index) {
        JsonObject *listener = array_object(listeners, index);
        const char *protocol = object_string(listener, "protocol", "proxy");
        const char *address = object_string(listener, "addr", "");
        guint64 active = object_uint64(listener, "active_conns");
        g_ptr_array_add(out->listeners, g_strdup_printf(
            "%s · %s · %" G_GUINT64_FORMAT " active",
            protocol, address, active));
    }
    g_object_unref(parser);
    return TRUE;
}

gboolean ch_gtk_parse_profiles(const guint8 *data, gsize length,
                               ch_gtk_profiles_model *out, GError **error) {
    memset(out, 0, sizeof(*out));
    JsonParser *parser = NULL;
    JsonObject *root = parse_root(data, length, &parser, error);
    if (root == NULL) return FALSE;
    out->active = g_strdup(object_string(root, "active", ""));
    out->names = g_ptr_array_new_with_free_func(g_free);
    JsonArray *profiles = object_array(root, "profiles");
    for (guint index = 0U; index < array_length(profiles); ++index) {
        JsonNode *node = json_array_get_element(profiles, index);
        if (node != NULL && JSON_NODE_HOLDS_VALUE(node) &&
            json_node_get_value_type(node) == G_TYPE_STRING) {
            g_ptr_array_add(out->names,
                            g_strdup(json_node_get_string(node)));
        }
    }
    g_object_unref(parser);
    return TRUE;
}

gboolean ch_gtk_parse_traffic(const guint8 *data, gsize length,
                              ch_gtk_traffic_model *out, GError **error) {
    memset(out, 0, sizeof(*out));
    JsonParser *parser = NULL;
    JsonObject *root = parse_root(data, length, &parser, error);
    if (root == NULL) return FALSE;
    JsonObject *summary = NULL;
    JsonNode *summary_node = json_object_get_member(root, "summary");
    if (summary_node != NULL && JSON_NODE_HOLDS_OBJECT(summary_node)) {
        summary = json_node_get_object(summary_node);
    }
    out->active_connections = object_uint64(summary, "active_connections");
    out->rx_bps = object_number(summary, "rx_bps", 0.0);
    out->tx_bps = object_number(summary, "tx_bps", 0.0);
    out->rx_total = object_uint64(summary, "rx_total");
    out->tx_total = object_uint64(summary, "tx_total");
    out->rows = g_ptr_array_new_with_free_func((GDestroyNotify)ch_gtk_row_free);
    JsonArray *connections = object_array(root, "connections");
    for (guint index = 0U; index < array_length(connections); ++index) {
        JsonObject *connection = array_object(connections, index);
        const char *action = object_string(connection, "rule_action", "proxy");
        const char *target = object_string(connection, "target", "");
        const char *state = object_string(connection, "state", "");
        const char *network = object_string(connection, "network", "tcp");
        const char *profile = object_string(connection, "profile", "");
        const char *chain = object_string(connection, "chain_name", "");
        const char *rule = object_string(connection, "rule_name", "");
        g_autofree char *rx = ch_gtk_format_bytes(
            object_uint64(connection, "rx_total"));
        g_autofree char *tx = ch_gtk_format_bytes(
            object_uint64(connection, "tx_total"));
        g_autofree char *action_upper = g_ascii_strup(action, -1);
        g_autofree char *title = g_strdup_printf(
            "%s  %s", action_upper, target);
        g_autofree char *detail = g_strdup_printf(
            "%s · %s · %s · %s · %s / %s",
            state, network, profile, chain[0] == '\0' ? "direct" : chain,
            rx, tx);
        if (rule[0] != '\0') {
            g_autofree char *with_rule = g_strdup_printf("%s · %s", detail,
                                                         rule);
            g_free(g_steal_pointer(&detail));
            detail = g_steal_pointer(&with_rule);
        }
        g_ptr_array_add(out->rows, row_new(
            title, detail, object_string(connection, "conn_id", "")));
    }
    g_object_unref(parser);
    return TRUE;
}

static void append_server_rows(JsonObject *root, GPtrArray *rows) {
    JsonArray *chains = object_array(root, "chains");
    for (guint chain_index = 0U;
         chain_index < array_length(chains); ++chain_index) {
        JsonObject *chain = array_object(chains, chain_index);
        const char *chain_name = object_string(chain, "name", "");
        JsonArray *servers = object_array(chain, "servers");
        for (guint index = 0U; index < array_length(servers); ++index) {
            JsonObject *server = array_object(servers, index);
            const char *name = object_string(server, "name", "Unnamed server");
            const char *protocol = object_string(server, "protocol", "");
            const char *address = object_string(server, "address", "");
            g_autofree char *detail = g_strdup_printf(
                "%s · %s · %s", chain_name, protocol,
                address[0] == '\0' ? "configured endpoint" : address);
            g_ptr_array_add(rows, row_new(name, detail, ""));
        }
    }
}

static void append_rule_rows(JsonObject *root, GPtrArray *rows) {
    JsonArray *rules = object_array(root, "rules");
    for (guint index = 0U; index < array_length(rules); ++index) {
        JsonObject *rule = array_object(rules, index);
        const char *name = object_string(rule, "name", "Unnamed rule");
        const char *action = object_string(rule, "action", "block");
        guint domains = array_length(object_array(rule, "domains"));
        guint suffixes = array_length(object_array(rule, "domain_suffixes"));
        guint keywords = array_length(object_array(rule, "domain_keywords"));
        guint cidrs = array_length(object_array(rule, "cidrs"));
        guint ports = array_length(object_array(rule, "ports"));
        guint networks = array_length(object_array(rule, "networks"));
        g_autofree char *action_upper = g_ascii_strup(action, -1);
        g_autofree char *title = g_strdup_printf("%s  %s", action_upper, name);
        g_autofree char *detail = g_strdup_printf(
            "%u domains · %u suffixes · %u keywords · %u CIDRs · "
            "%u ports · %u networks", domains, suffixes, keywords, cidrs,
            ports, networks);
        g_ptr_array_add(rows, row_new(title, detail, name));
    }
}

static void append_policy_rows(JsonObject *root, GPtrArray *rows) {
    JsonArray *groups = object_array(root, "groups");
    for (guint index = 0U; index < array_length(groups); ++index) {
        JsonObject *group = array_object(groups, index);
        if (object_boolean(group, "hidden", FALSE)) continue;
        const char *name = object_string(group, "name", "Unnamed group");
        const char *type = object_string(group, "type", "select");
        const char *selected = object_string(group, "selected", "");
        if (selected[0] == '\0') {
            selected = object_string(group, "active_chain", "none");
        }
        JsonArray *chains = object_array(group, "chains");
        g_autofree char *detail = g_strdup_printf(
            "%s · active %s · %u chains", type,
            selected[0] == '\0' ? "none" : selected,
            array_length(chains));
        ch_gtk_row *row = row_new(
            name, detail, object_string(group, "name", ""));
        g_free(row->selected);
        row->selected = g_strdup(selected);
        row->selectable = strcmp(type, "select") == 0;
        row->options = g_ptr_array_new_with_free_func(g_free);
        for (guint chain_index = 0U;
             chain_index < array_length(chains); ++chain_index) {
            JsonNode *chain = json_array_get_element(chains, chain_index);
            if (chain != NULL && JSON_NODE_HOLDS_VALUE(chain) &&
                json_node_get_value_type(chain) == G_TYPE_STRING) {
                g_ptr_array_add(row->options,
                                g_strdup(json_node_get_string(chain)));
            }
        }
        g_ptr_array_add(rows, row);
    }
}

static void append_prompt_rows(JsonObject *root, GPtrArray *rows) {
    JsonArray *prompts = object_array(root, "prompts");
    for (guint index = 0U; index < array_length(prompts); ++index) {
        JsonObject *prompt = array_object(prompts, index);
        const char *process = object_string(prompt, "process_name",
                                            "Unknown process");
        const char *target = object_string(prompt, "target", "");
        const char *network = object_string(prompt, "network", "tcp");
        const char *chain = object_string(prompt, "would_use_chain", "");
        g_autofree char *title = g_strdup_printf("%s → %s", process, target);
        g_autofree char *detail = g_strdup_printf(
            "%s%s%s", network, chain[0] == '\0' ? "" : " · via ", chain);
        g_ptr_array_add(rows, row_new(
            title, detail, object_string(prompt, "id", "")));
    }
}

static void append_silent_decision_rows(JsonObject *root, GPtrArray *rows) {
    JsonArray *decisions = object_array(root, "decisions");
    for (guint index = 0U; index < array_length(decisions); ++index) {
        JsonObject *decision = array_object(decisions, index);
        const char *process = object_string(decision, "process_name",
                                            "Unknown process");
        const char *target = object_string(decision, "target", "");
        const char *network = object_string(decision, "network", "tcp");
        const char *action = object_string(decision, "action", "deny");
        const char *profile = object_string(decision, "profile", "");
        g_autofree char *title = g_strdup_printf(
            "%s %s → %s", g_ascii_strcasecmp(action, "allow") == 0 ?
                "Allowed" : "Denied", process, target);
        g_autofree char *detail = g_strdup_printf(
            "%s%s%s", network, profile[0] == '\0' ? "" : " · profile ",
            profile);
        g_ptr_array_add(rows, row_new(
            title, detail, object_string(decision, "id", "")));
    }
}

static void append_dns_rows(JsonObject *root, GPtrArray *rows) {
    JsonArray *upstreams = object_array(root, "upstreams");
    for (guint index = 0U; index < array_length(upstreams); ++index) {
        JsonObject *upstream = array_object(upstreams, index);
        const char *protocol = object_string(upstream, "protocol", "dns");
        const char *name = object_string(upstream, "name", protocol);
        const char *url = object_string(upstream, "url", "");
        const char *address = object_string(upstream, "address", "");
        g_autofree char *detail = g_strdup_printf(
            "%s · %s", protocol, url[0] == '\0' ? address : url);
        g_ptr_array_add(rows, row_new(name, detail, ""));
    }
}

static void append_capture_rows(JsonObject *root, GPtrArray *rows) {
    JsonArray *entries = object_array(root, "entries");
    for (guint index = 0U; index < array_length(entries); ++index) {
        JsonObject *entry = array_object(entries, index);
        const char *method = object_string(entry, "method", "GET");
        const char *url = object_string(entry, "url", "");
        double status = object_number(entry, "status", 0.0);
        g_autofree char *title = g_strdup_printf("%s  %s", method, url);
        g_autofree char *detail = status > 0.0 ?
            g_strdup_printf("HTTP %.0f", status) : g_strdup("No response");
        g_ptr_array_add(rows, row_new(
            title, detail, object_string(entry, "id", "")));
    }
}

static void append_conditioner_rows(JsonObject *root, GPtrArray *rows) {
    gboolean enabled = object_boolean(root, "enabled", FALSE);
    const char *latency = object_string(root, "latency", "0ms");
    const char *jitter = object_string(root, "jitter", "0ms");
    double loss = object_number(root, "loss_percent", 0.0);
    double download = object_number(root, "download_kbps", 0.0);
    double upload = object_number(root, "upload_kbps", 0.0);
    g_autofree char *detail = g_strdup_printf(
        "latency %s · jitter %s · loss %.2f%% · %.0f Kbps down / "
        "%.0f Kbps up",
        latency, jitter, loss, download, upload);
    g_ptr_array_add(rows, row_new(enabled ? "Enabled" : "Disabled",
                                  detail, "conditioner"));
}

static char *capture_headers_text(JsonObject *message) {
    JsonArray *headers = object_array(message, "headers");
    if (array_length(headers) == 0U) return g_strdup("No headers");
    GString *text = g_string_new("");
    for (guint index = 0U; index < array_length(headers); ++index) {
        JsonObject *header = array_object(headers, index);
        const char *name = object_string(header, "name", "");
        const char *value = object_string(header, "value", "");
        if (index > 0U) g_string_append_c(text, '\n');
        g_string_append_printf(text, "%s: %s", name, value);
        if (object_boolean(header, "truncated", FALSE)) {
            g_string_append(text, " [truncated]");
        }
    }
    return g_string_free(text, FALSE);
}

static char *capture_body_text(JsonObject *message) {
    JsonObject *body = object_object(message, "body");
    const char *preview = object_string(body, "preview", "");
    const char *base64 = object_string(body, "preview_base64", "");
    const char *content = preview[0] != '\0' ? preview : base64;
    guint64 size = object_uint64(body, "size");
    const char *mime = object_string(body, "mime_type", "");
    const char *encoding = object_string(body, "encoding", "");
    gboolean truncated = object_boolean(body, "truncated", FALSE);
    GString *text = g_string_new(
        content[0] == '\0' ? "No body preview" : content);
    g_string_append_printf(text, "\n\n%" G_GUINT64_FORMAT " bytes", size);
    if (mime[0] != '\0') g_string_append_printf(text, " · %s", mime);
    if (encoding[0] != '\0') {
        g_string_append_printf(text, " · %s", encoding);
    }
    if (truncated) g_string_append(text, " · preview truncated");
    return g_string_free(text, FALSE);
}

gboolean ch_gtk_parse_capture_detail(const guint8 *data, gsize length,
                                     ch_gtk_capture_detail *out,
                                     GError **error) {
    memset(out, 0, sizeof(*out));
    JsonParser *parser = NULL;
    JsonObject *root = parse_root(data, length, &parser, error);
    if (root == NULL) return FALSE;
    JsonObject *wrapped = object_object(root, "entry");
    if (wrapped != NULL) root = wrapped;
    out->identifier = g_strdup(object_string(root, "id", ""));
    out->method = g_strdup(object_string(root, "method", "GET"));
    out->url = g_strdup(object_string(root, "url", ""));
    out->host = g_strdup(object_string(root, "host", ""));
    out->profile = g_strdup(object_string(root, "profile", ""));
    out->chain = g_strdup(object_string(root, "chain_name", ""));
    out->started_at = g_strdup(object_string(root, "started_at", ""));
    out->finished_at = g_strdup(object_string(root, "finished_at", ""));
    out->error_message = g_strdup(object_string(root, "error", ""));
    double status = object_number(root, "status", 0.0);
    out->status = status <= 0.0 ? 0 :
        (status >= (double)G_MAXINT ? G_MAXINT : (gint)status);
    JsonObject *request = object_object(root, "request");
    JsonObject *response = object_object(root, "response");
    out->request_headers = capture_headers_text(request);
    out->request_body = capture_body_text(request);
    out->response_headers = capture_headers_text(response);
    out->response_body = capture_body_text(response);
    g_object_unref(parser);
    return TRUE;
}

char *ch_gtk_parse_curl_export(const guint8 *data, gsize length,
                               GError **error) {
    JsonParser *parser = NULL;
    JsonObject *root = parse_root(data, length, &parser, error);
    if (root == NULL) return NULL;
    char *result = g_strdup(object_string(root, "curl", ""));
    g_object_unref(parser);
    return result;
}

gboolean ch_gtk_parse_conditioner(const guint8 *data, gsize length,
                                  ch_gtk_conditioner_model *out,
                                  GError **error) {
    memset(out, 0, sizeof(*out));
    JsonParser *parser = NULL;
    JsonObject *root = parse_root(data, length, &parser, error);
    if (root == NULL) return FALSE;
    out->profile = g_strdup(object_string(root, "profile", ""));
    out->enabled = object_boolean(root, "enabled", FALSE);
    out->download_kbps = object_uint64(root, "download_kbps");
    out->upload_kbps = object_uint64(root, "upload_kbps");
    out->latency = g_strdup(object_string(root, "latency", ""));
    out->jitter = g_strdup(object_string(root, "jitter", ""));
    out->loss_percent = object_number(root, "loss_percent", 0.0);
    if (!isfinite(out->loss_percent) || out->loss_percent < 0.0 ||
        out->loss_percent > 100.0) {
        ch_gtk_conditioner_model_clear(out);
        g_set_error_literal(error, ch_gtk_model_error_quark(), 1,
                            "daemon conditioner loss must be between 0 and 100");
        g_object_unref(parser);
        return FALSE;
    }
    g_object_unref(parser);
    return TRUE;
}

gboolean ch_gtk_parse_dns(const guint8 *data, gsize length,
                          ch_gtk_dns_model *out, GError **error) {
    memset(out, 0, sizeof(*out));
    JsonParser *parser = NULL;
    JsonObject *root = parse_root(data, length, &parser, error);
    if (root == NULL) return FALSE;
    out->profile = g_strdup(object_string(root, "profile", ""));
    out->enabled = object_boolean(root, "enabled", FALSE);
    out->timeout = g_strdup(object_string(root, "timeout", ""));
    JsonArray *upstreams = object_array(root, "upstreams");
    g_autoptr(JsonGenerator) generator = json_generator_new();
    JsonNode *node = json_node_new(JSON_NODE_ARRAY);
    json_node_take_array(node, upstreams == NULL ? json_array_new() :
                                                  json_array_ref(upstreams));
    json_generator_set_root(generator, node);
    json_generator_set_pretty(generator, TRUE);
    json_generator_set_indent(generator, 2U);
    out->upstreams_json = json_generator_to_data(generator, NULL);
    json_node_free(node);
    g_object_unref(parser);
    return TRUE;
}

gboolean ch_gtk_parse_curl_import(const guint8 *data, gsize length,
                                  ch_gtk_curl_import *out,
                                  GError **error) {
    memset(out, 0, sizeof(*out));
    JsonParser *parser = NULL;
    JsonObject *root = parse_root(data, length, &parser, error);
    if (root == NULL) return FALSE;
    out->method = g_strdup(object_string(root, "method", "GET"));
    out->url = g_strdup(object_string(root, "url", ""));
    out->body = g_strdup(object_string(root, "body", ""));
    out->headers = array_length(object_array(root, "headers")) == 0U ?
        g_strdup("") : capture_headers_text(root);
    g_object_unref(parser);
    return TRUE;
}

static gboolean http_token_valid(const char *value) {
    static const char separators[] = "()<>@,;:\\\"/[]?={} \t";
    if (value == NULL || value[0] == '\0') return FALSE;
    for (const unsigned char *cursor = (const unsigned char *)value;
         *cursor != 0U; ++cursor) {
        if (*cursor <= 0x20U || *cursor >= 0x7fU ||
            strchr(separators, (int)*cursor) != NULL) return FALSE;
    }
    return TRUE;
}

char *ch_gtk_composed_request_body(const char *method, const char *url,
                                   const char *headers, const char *body,
                                   GError **error) {
    g_autofree char *clean_method = g_strdup(method == NULL ? "" : method);
    g_autofree char *clean_url = g_strdup(url == NULL ? "" : url);
    g_strstrip(clean_method);
    g_strstrip(clean_url);
    if (clean_method[0] == '\0') {
        g_free(g_steal_pointer(&clean_method));
        clean_method = g_strdup("GET");
    }
    if (!http_token_valid(clean_method) || clean_url[0] == '\0' ||
        (!g_str_has_prefix(clean_url, "http://") &&
         !g_str_has_prefix(clean_url, "https://"))) {
        g_set_error_literal(error, ch_gtk_model_error_quark(), 1,
                            "Enter a valid HTTP method and HTTP(S) URL.");
        return NULL;
    }
    g_autoptr(JsonBuilder) builder = json_builder_new();
    json_builder_begin_object(builder);
    json_builder_set_member_name(builder, "method");
    json_builder_add_string_value(builder, clean_method);
    json_builder_set_member_name(builder, "url");
    json_builder_add_string_value(builder, clean_url);
    json_builder_set_member_name(builder, "headers");
    json_builder_begin_array(builder);
    g_auto(GStrv) lines = g_strsplit(headers == NULL ? "" : headers, "\n", -1);
    for (gsize index = 0U; lines[index] != NULL; ++index) {
        g_strchomp(lines[index]);
        g_strstrip(lines[index]);
        if (lines[index][0] == '\0') continue;
        char *colon = strchr(lines[index], ':');
        if (colon == NULL || colon == lines[index]) {
            g_set_error(error, ch_gtk_model_error_quark(), 1,
                        "Header line %zu must use Name: value.", index + 1U);
            return NULL;
        }
        *colon = '\0';
        char *name = g_strstrip(lines[index]);
        char *value = g_strstrip(colon + 1U);
        if (!http_token_valid(name) || strpbrk(value, "\r\n") != NULL) {
            g_set_error(error, ch_gtk_model_error_quark(), 1,
                        "Header line %zu is invalid.", index + 1U);
            return NULL;
        }
        json_builder_begin_object(builder);
        json_builder_set_member_name(builder, "name");
        json_builder_add_string_value(builder, name);
        json_builder_set_member_name(builder, "value");
        json_builder_add_string_value(builder, value);
        json_builder_end_object(builder);
    }
    json_builder_end_array(builder);
    json_builder_set_member_name(builder, "body");
    json_builder_add_string_value(builder, body == NULL ? "" : body);
    json_builder_end_object(builder);
    return builder_json(builder);
}

char *ch_gtk_repeat_request_body(const char *identifier) {
    g_autoptr(JsonBuilder) builder = json_builder_new();
    json_builder_begin_object(builder);
    json_builder_set_member_name(builder, "entry_id");
    json_builder_add_string_value(builder, identifier == NULL ? "" : identifier);
    json_builder_end_object(builder);
    return builder_json(builder);
}

static char *json_node_text(JsonNode *node) {
    if (node == NULL || JSON_NODE_HOLDS_NULL(node)) return g_strdup("");
    g_autoptr(JsonGenerator) generator = json_generator_new();
    json_generator_set_root(generator, node);
    return json_generator_to_data(generator, NULL);
}

gboolean ch_gtk_parse_license_state(const guint8 *data, gsize length,
                                    ch_gtk_license_state *out,
                                    GError **error) {
    memset(out, 0, sizeof(*out));
    if (data == NULL || length == 0U) {
        out->install_id = g_strdup("");
        out->email = g_strdup("");
        out->snapshot_json = g_strdup("");
        out->grant_json = g_strdup("");
        out->device_state_json = g_strdup("");
        return TRUE;
    }
    JsonParser *parser = NULL;
    JsonObject *root = parse_root(data, length, &parser, error);
    if (root == NULL) return FALSE;
    out->install_id = g_strdup(object_string(root, "installId", ""));
    out->email = g_strdup(object_string(root, "email", ""));
    out->snapshot_json = g_strdup(object_string(root, "snapshotJson", ""));
    out->grant_json = g_strdup(object_string(root, "grantJson", ""));
    out->device_state_json = g_strdup(
        object_string(root, "deviceStateJson", ""));
    g_object_unref(parser);
    return TRUE;
}

char *ch_gtk_license_state_json(const ch_gtk_license_state *state) {
    g_autoptr(JsonBuilder) builder = json_builder_new();
    json_builder_begin_object(builder);
    json_builder_set_member_name(builder, "installId");
    json_builder_add_string_value(builder, state->install_id == NULL ? "" :
                                                                    state->install_id);
    json_builder_set_member_name(builder, "email");
    json_builder_add_string_value(builder, state->email == NULL ? "" :
                                                               state->email);
    json_builder_set_member_name(builder, "snapshotJson");
    json_builder_add_string_value(builder, state->snapshot_json == NULL ? "" :
                                                                       state->snapshot_json);
    json_builder_set_member_name(builder, "grantJson");
    json_builder_add_string_value(builder, state->grant_json == NULL ? "" :
                                                                    state->grant_json);
    json_builder_set_member_name(builder, "deviceStateJson");
    json_builder_add_string_value(
        builder, state->device_state_json == NULL ? "" :
                                                   state->device_state_json);
    json_builder_end_object(builder);
    return builder_json(builder);
}

static void replace_node_json(JsonObject *root, const char *name,
                              char **destination) {
    JsonNode *node = json_object_get_member(root, name);
    if (node == NULL || JSON_NODE_HOLDS_NULL(node)) return;
    g_autofree char *encoded = json_node_text(node);
    g_free(*destination);
    *destination = g_steal_pointer(&encoded);
}

gboolean ch_gtk_license_state_apply(const guint8 *data, gsize length,
                                    ch_gtk_license_state *state,
                                    GError **error) {
    JsonParser *parser = NULL;
    JsonObject *root = parse_root(data, length, &parser, error);
    if (root == NULL) return FALSE;
    replace_node_json(root, "snapshot", &state->snapshot_json);
    replace_node_json(root, "grant", &state->grant_json);
    replace_node_json(root, "deviceState", &state->device_state_json);
    g_object_unref(parser);
    return TRUE;
}

static char *license_detail(JsonObject *decision, const char *reason) {
    if (strcmp(reason, "trial") == 0) {
        guint64 days = object_uint64(decision, "trialDaysRemaining");
        const char *ends = object_string(decision, "trialEndsAt", "");
        return ends[0] == '\0' ?
            g_strdup_printf("%" G_GUINT64_FORMAT
                            " days left in the one-month trial", days) :
            g_strdup_printf("%" G_GUINT64_FORMAT
                            " days left · trial ends %.10s", days, ends);
    }
    if (strcmp(reason, "lifetime") == 0) {
        const char *cutoff = object_string(decision, "updateCutoffDate", "");
        return cutoff[0] == '\0' ?
            g_strdup("Lifetime license active") :
            g_strdup_printf("Lifetime license · updates included through %.10s",
                            cutoff);
    }
    if (strcmp(reason, "offlineGrace") == 0) {
        const char *ends = object_string(decision, "offlineGraceEndsAt", "");
        return ends[0] == '\0' ?
            g_strdup("Using a cached license while verification is unavailable") :
            g_strdup_printf("Cached license · offline grace through %.10s", ends);
    }
    return g_strdup("Activate a license key to continue using ClambHook.");
}

gboolean ch_gtk_parse_license_view(const char *status_json,
                                   const char *device_state_json,
                                   ch_gtk_license_view *out,
                                   GError **error) {
    memset(out, 0, sizeof(*out));
    JsonParser *status_parser = NULL;
    const char *status_text = status_json == NULL || status_json[0] == '\0' ?
        "{}" : status_json;
    JsonObject *status = parse_root(
        (const guint8 *)status_text, strlen(status_text), &status_parser, error);
    if (status == NULL) return FALSE;
    JsonObject *decision = object_object(status, "decision");
    const char *reason = object_string(decision, "reason", "locked");
    out->can_use_app = strcmp(reason, "locked") != 0;
    if (strcmp(reason, "trial") == 0) out->title = g_strdup("Trial active");
    else if (strcmp(reason, "lifetime") == 0) {
        out->title = g_strdup("Licensed");
    } else if (strcmp(reason, "offlineGrace") == 0) {
        out->title = g_strdup("Licensed (offline grace)");
    } else out->title = g_strdup("License required");
    out->detail = license_detail(decision, reason);

    JsonParser *device_parser = NULL;
    const char *device_text = device_state_json == NULL ||
        device_state_json[0] == '\0' ? "{}" : device_state_json;
    JsonObject *device_state = parse_root(
        (const guint8 *)device_text, strlen(device_text), &device_parser,
        error);
    if (device_state == NULL) {
        g_object_unref(status_parser);
        ch_gtk_license_view_clear(out);
        return FALSE;
    }
    out->current_device_id = g_strdup(
        object_string(device_state, "current_device_id", ""));
    guint64 maximum = object_uint64(device_state, "max_active_devices");
    out->max_active_devices = maximum == 0U ? 3U :
        (maximum > G_MAXUINT ? G_MAXUINT : (guint)maximum);
    out->devices = g_ptr_array_new_with_free_func(
        (GDestroyNotify)ch_gtk_row_free);
    JsonArray *devices = object_array(device_state, "devices");
    for (guint index = 0U; index < array_length(devices); ++index) {
        JsonObject *device = array_object(devices, index);
        const char *identifier = object_string(device, "device_id", "");
        const char *name = object_string(device, "display_name", "Device");
        const char *platform = object_string(device, "platform", "unknown");
        const char *architecture = object_string(
            device, "architecture", "unknown");
        const char *deactivated = object_string(
            device, "deactivated_at", "");
        gboolean active = deactivated[0] == '\0';
        if (active) ++out->active_devices;
        if (active && strcmp(identifier, out->current_device_id) == 0) {
            out->current_device_active = TRUE;
        }
        g_autofree char *detail = g_strdup_printf(
            "%s · %s · %s", platform, architecture,
            active ? "active" : "deactivated");
        g_ptr_array_add(out->devices, row_new(name, detail, identifier));
    }
    g_object_unref(device_parser);
    g_object_unref(status_parser);
    return TRUE;
}

char *ch_gtk_license_registration_body(const char *install_id,
                                       const char *display_name,
                                       const char *architecture,
                                       const char *app_version) {
    g_autoptr(JsonBuilder) builder = json_builder_new();
    json_builder_begin_object(builder);
    json_builder_set_member_name(builder, "install_id");
    json_builder_add_string_value(builder, install_id == NULL ? "" :
                                                               install_id);
    json_builder_set_member_name(builder, "display_name");
    json_builder_add_string_value(builder, display_name == NULL ?
                                           "GNU/Linux device" : display_name);
    json_builder_set_member_name(builder, "platform");
    json_builder_add_string_value(builder, "linux");
    json_builder_set_member_name(builder, "architecture");
    json_builder_add_string_value(builder, architecture == NULL ? "unknown" :
                                                                 architecture);
    json_builder_set_member_name(builder, "app_version");
    json_builder_add_string_value(builder, app_version == NULL ? "" :
                                                               app_version);
    json_builder_end_object(builder);
    return builder_json(builder);
}

gboolean ch_gtk_parse_page_rows(ch_gtk_page_model_kind kind,
                                const guint8 *data, gsize length,
                                GPtrArray **out_rows, char **out_summary,
                                GError **error) {
    *out_rows = NULL;
    *out_summary = NULL;
    JsonParser *parser = NULL;
    JsonObject *root = parse_root(data, length, &parser, error);
    if (root == NULL) return FALSE;
    GPtrArray *rows = g_ptr_array_new_with_free_func(
        (GDestroyNotify)ch_gtk_row_free);
    switch (kind) {
        case CH_GTK_PAGE_SERVERS:
            append_server_rows(root, rows);
            *out_summary = g_strdup("Configured chains and server endpoints");
            break;
        case CH_GTK_PAGE_RULES:
            append_rule_rows(root, rows);
            *out_summary = g_strdup("Ordered rules for the active profile");
            break;
        case CH_GTK_PAGE_POLICIES:
            append_policy_rows(root, rows);
            *out_summary = g_strdup("Policy groups for the active profile");
            break;
        case CH_GTK_PAGE_PROMPTS:
            append_prompt_rows(root, rows);
            *out_summary = g_strdup("Pending connection decisions");
            break;
        case CH_GTK_PAGE_SILENT_DECISIONS:
            append_silent_decision_rows(root, rows);
            *out_summary = g_strdup(
                "Recent Silent Mode decisions available for promotion");
            break;
        case CH_GTK_PAGE_DNS:
            append_dns_rows(root, rows);
            *out_summary = g_strdup("Encrypted DNS upstreams");
            break;
        case CH_GTK_PAGE_CAPTURES:
            append_capture_rows(root, rows);
            *out_summary = g_strdup("Newest captured HTTP transactions");
            break;
        case CH_GTK_PAGE_CONDITIONER:
            append_conditioner_rows(root, rows);
            *out_summary = g_strdup("Active-profile network conditioning");
            break;
    }
    *out_rows = rows;
    g_object_unref(parser);
    return TRUE;
}
