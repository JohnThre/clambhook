int main(string[] args) {
    // Prefer the native Wayland backend on Wayland-first distros (Fedora,
    // Bazzite, PureOS, Ubuntu, Debian) and fall back to X11 automatically.
    // Respect an explicit GDK_BACKEND from the user or the environment; only
    // set a preference order when none is provided.
    if (Environment.get_variable("GDK_BACKEND") == null) {
        Environment.set_variable("GDK_BACKEND", "wayland,x11", true);
    }
    Adw.init();
    var app = new Clambhook.ClambhookApplication();
    return app.run(args);
}
