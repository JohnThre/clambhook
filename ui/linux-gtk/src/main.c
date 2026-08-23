#include <gtk/gtk.h>
#include <json-glib/json-glib.h>
#include <libsoup/soup.h>

#include <stdlib.h>
#include <string.h>

typedef struct clambhook_linux_app {
    GtkApplication *application;
    GtkWidget *window;
    GtkWidget *status_label;
    GtkWidget *profile_label;
    GtkWidget *mode_label;
    GtkWidget *error_label;
    GtkWidget *connect_button;
    GtkWidget *spinner;
    SoupSession *session;
    char *api_url;
    char *api_token;
    gboolean running;
    gboolean request_active;
} ClambhookLinuxApp;

typedef struct request_context {
    ClambhookLinuxApp *app;
    SoupMessage *message;
} RequestContext;

static char *join_url(const char *base, const char *path) {
    gsize base_length = strlen(base);
    gboolean slash = base_length > 0U && base[base_length - 1U] == '/';
    return g_strdup_printf("%s%s%s", base, slash ? "" : "/", path[0] == '/' ? path + 1 : path);
}

static void set_request_active(ClambhookLinuxApp *app, gboolean active) {
    app->request_active = active;
    gtk_widget_set_sensitive(app->connect_button, !active);
    gtk_widget_set_visible(app->spinner, active);
    if (active) {
        gtk_spinner_start(GTK_SPINNER(app->spinner));
    } else {
        gtk_spinner_stop(GTK_SPINNER(app->spinner));
    }
}

static const char *json_string_member(JsonObject *object, const char *name, const char *fallback) {
    if (!json_object_has_member(object, name)) return fallback;
    JsonNode *node = json_object_get_member(object, name);
    return JSON_NODE_HOLDS_VALUE(node) && json_node_get_value_type(node) == G_TYPE_STRING
        ? json_node_get_string(node) : fallback;
}

static void apply_status(ClambhookLinuxApp *app, const guint8 *data, gsize length) {
    g_autoptr(JsonParser) parser = json_parser_new();
    g_autoptr(GError) error = NULL;
    if (!json_parser_load_from_data(parser, (const char *)data, (gssize)length, &error)) {
        gtk_label_set_text(GTK_LABEL(app->error_label), error->message);
        return;
    }
    JsonNode *root = json_parser_get_root(parser);
    if (root == NULL || !JSON_NODE_HOLDS_OBJECT(root)) {
        gtk_label_set_text(GTK_LABEL(app->error_label), "Daemon returned an invalid status payload.");
        return;
    }
    JsonObject *object = json_node_get_object(root);
    app->running = json_object_has_member(object, "running") &&
        json_object_get_boolean_member(object, "running");
    const char *profile = json_string_member(object, "profile", "—");
    const char *mode = json_string_member(object, "tunnel_mode", app->running ? "proxy" : "—");
    gtk_label_set_text(GTK_LABEL(app->status_label), app->running ? "Connected" : "Disconnected");
    gtk_label_set_text(GTK_LABEL(app->profile_label), profile);
    gtk_label_set_text(GTK_LABEL(app->mode_label), mode);
    gtk_button_set_label(GTK_BUTTON(app->connect_button), app->running ? "Disconnect" : "Connect");
    gtk_widget_remove_css_class(app->status_label, app->running ? "error" : "success");
    gtk_widget_add_css_class(app->status_label, app->running ? "success" : "error");
    gtk_label_set_text(GTK_LABEL(app->error_label), "");
}

static void request_finished(GObject *source, GAsyncResult *result, gpointer user_data) {
    RequestContext *context = user_data;
    ClambhookLinuxApp *app = context->app;
    g_autoptr(GError) error = NULL;
    g_autoptr(GBytes) body = soup_session_send_and_read_finish(SOUP_SESSION(source), result, &error);
    set_request_active(app, FALSE);
    if (error != NULL) {
        gtk_label_set_text(GTK_LABEL(app->error_label), error->message);
    } else {
        guint status = soup_message_get_status(context->message);
        gsize length = 0U;
        const guint8 *data = g_bytes_get_data(body, &length);
        if (status >= 200U && status < 300U) {
            apply_status(app, data, length);
        } else {
            g_autofree char *message = g_strndup((const char *)data, length);
            g_autofree char *display = g_strdup_printf("Daemon request failed (%u): %s", status, message);
            gtk_label_set_text(GTK_LABEL(app->error_label), display);
        }
    }
    g_object_unref(context->message);
    g_free(context);
}

static void send_request(ClambhookLinuxApp *app, const char *method, const char *path) {
    if (app->request_active) return;
    g_autofree char *url = join_url(app->api_url, path);
    SoupMessage *message = soup_message_new(method, url);
    if (message == NULL) {
        gtk_label_set_text(GTK_LABEL(app->error_label), "The configured daemon URL is invalid.");
        return;
    }
    if (app->api_token[0] != '\0') {
        g_autofree char *authorization = g_strdup_printf("Bearer %s", app->api_token);
        soup_message_headers_replace(
            soup_message_get_request_headers(message), "Authorization", authorization
        );
    }
    if (strcmp(method, "POST") == 0 || strcmp(method, "PUT") == 0) {
        g_autoptr(GBytes) request_body = g_bytes_new_static("{}", 2U);
        soup_message_set_request_body_from_bytes(message, "application/json", request_body);
    }
    RequestContext *context = g_new0(RequestContext, 1U);
    context->app = app;
    context->message = message;
    set_request_active(app, TRUE);
    soup_session_send_and_read_async(
        app->session, message, G_PRIORITY_DEFAULT, NULL, request_finished, context
    );
}

static void connect_clicked(GtkButton *button, gpointer user_data) {
    (void)button;
    ClambhookLinuxApp *app = user_data;
    send_request(app, "POST", app->running ? "/api/v1/disconnect" : "/api/v1/connect");
}

static void refresh_clicked(GtkButton *button, gpointer user_data) {
    (void)button;
    send_request(user_data, "GET", "/api/v1/status");
}

static GtkWidget *detail_row(const char *title, GtkWidget **value_out) {
    GtkWidget *row = gtk_box_new(GTK_ORIENTATION_HORIZONTAL, 12);
    GtkWidget *title_label = gtk_label_new(title);
    GtkWidget *value = gtk_label_new("—");
    gtk_widget_set_hexpand(value, TRUE);
    gtk_label_set_xalign(GTK_LABEL(title_label), 0.0F);
    gtk_label_set_xalign(GTK_LABEL(value), 1.0F);
    gtk_widget_add_css_class(title_label, "dim-label");
    gtk_box_append(GTK_BOX(row), title_label);
    gtk_box_append(GTK_BOX(row), value);
    *value_out = value;
    return row;
}

static void activate(GtkApplication *application, gpointer user_data) {
    ClambhookLinuxApp *app = user_data;
    if (app->window != NULL) {
        gtk_window_present(GTK_WINDOW(app->window));
        return;
    }
    app->window = gtk_application_window_new(application);
    gtk_window_set_title(GTK_WINDOW(app->window), "Clambhook");
    gtk_window_set_default_size(GTK_WINDOW(app->window), 620, 460);

    GtkWidget *header = gtk_header_bar_new();
    GtkWidget *refresh = gtk_button_new_from_icon_name("view-refresh-symbolic");
    gtk_widget_set_tooltip_text(refresh, "Refresh daemon status");
    g_signal_connect(refresh, "clicked", G_CALLBACK(refresh_clicked), app);
    gtk_header_bar_pack_end(GTK_HEADER_BAR(header), refresh);
    gtk_window_set_titlebar(GTK_WINDOW(app->window), header);

    GtkWidget *content = gtk_box_new(GTK_ORIENTATION_VERTICAL, 20);
    gtk_widget_set_margin_top(content, 28);
    gtk_widget_set_margin_bottom(content, 28);
    gtk_widget_set_margin_start(content, 32);
    gtk_widget_set_margin_end(content, 32);

    GtkWidget *title = gtk_label_new("Network status");
    gtk_label_set_xalign(GTK_LABEL(title), 0.0F);
    gtk_widget_add_css_class(title, "title-1");
    gtk_box_append(GTK_BOX(content), title);

    app->status_label = gtk_label_new("Loading…");
    gtk_label_set_xalign(GTK_LABEL(app->status_label), 0.0F);
    gtk_widget_add_css_class(app->status_label, "title-2");
    gtk_box_append(GTK_BOX(content), app->status_label);

    GtkWidget *details = gtk_box_new(GTK_ORIENTATION_VERTICAL, 10);
    gtk_widget_add_css_class(details, "card");
    gtk_box_append(GTK_BOX(details), detail_row("Active profile", &app->profile_label));
    gtk_box_append(GTK_BOX(details), detail_row("Routing mode", &app->mode_label));
    gtk_box_append(GTK_BOX(content), details);

    GtkWidget *actions = gtk_box_new(GTK_ORIENTATION_HORIZONTAL, 10);
    app->connect_button = gtk_button_new_with_label("Connect");
    gtk_widget_add_css_class(app->connect_button, "suggested-action");
    gtk_widget_set_hexpand(app->connect_button, TRUE);
    g_signal_connect(app->connect_button, "clicked", G_CALLBACK(connect_clicked), app);
    app->spinner = gtk_spinner_new();
    gtk_widget_set_visible(app->spinner, FALSE);
    gtk_box_append(GTK_BOX(actions), app->connect_button);
    gtk_box_append(GTK_BOX(actions), app->spinner);
    gtk_box_append(GTK_BOX(content), actions);

    app->error_label = gtk_label_new("");
    gtk_label_set_wrap(GTK_LABEL(app->error_label), TRUE);
    gtk_label_set_xalign(GTK_LABEL(app->error_label), 0.0F);
    gtk_widget_add_css_class(app->error_label, "error");
    gtk_box_append(GTK_BOX(content), app->error_label);
    gtk_window_set_child(GTK_WINDOW(app->window), content);
    gtk_window_present(GTK_WINDOW(app->window));
    send_request(app, "GET", "/api/v1/status");
}

int main(int argc, char **argv) {
    const char *configured_url = g_getenv("CLAMBHOOK_API_URL");
    const char *configured_token = g_getenv("CLAMBHOOK_API_TOKEN");
    ClambhookLinuxApp app = {
        .api_url = g_strdup(configured_url == NULL || configured_url[0] == '\0'
            ? "http://127.0.0.1:9090" : configured_url),
        .api_token = g_strdup(configured_token == NULL ? "" : configured_token),
        .session = soup_session_new()
    };
    app.application = gtk_application_new("com.clambhook.Clambhook", G_APPLICATION_DEFAULT_FLAGS);
    g_signal_connect(app.application, "activate", G_CALLBACK(activate), &app);
    int status = g_application_run(G_APPLICATION(app.application), argc, argv);
    g_clear_object(&app.session);
    g_clear_object(&app.application);
    g_free(app.api_url);
    g_free(app.api_token);
    return status;
}
