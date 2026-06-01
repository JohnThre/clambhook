int main(string[] args) {
    Environment.set_variable("GDK_BACKEND", "x11", true);
    Adw.init();
    var app = new Clambhook.ClambhookApplication();
    return app.run(args);
}
