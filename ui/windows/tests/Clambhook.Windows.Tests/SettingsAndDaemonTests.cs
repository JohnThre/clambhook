using Clambhook.Windows.Core;

namespace Clambhook.Windows.Tests;

public sealed class SettingsAndDaemonTests
{
    [Fact]
    public async Task FileSettingsStorePersistsNormalizedSettings()
    {
        var path = Path.Combine(Path.GetTempPath(), Guid.NewGuid().ToString("N"), "settings.json");
        var store = new FileSettingsStore(path);

        await store.SaveAsync(new AppSettings
        {
            ApiEndpoint = " http://proxy.example:9090/ ",
            RefreshIntervalSeconds = 1,
            EventStreamEnabled = false,
            DaemonPath = " C:\\tools\\clambhook.exe ",
            ConfigPath = " C:\\tools\\config.toml "
        });

        var settings = await store.LoadAsync();
        Assert.Equal("http://proxy.example:9090", settings.ApiEndpoint);
        Assert.Equal(2, settings.RefreshIntervalSeconds);
        Assert.False(settings.EventStreamEnabled);
        Assert.Equal("C:\\tools\\clambhook.exe", settings.DaemonPath);
        Assert.Equal("C:\\tools\\config.toml", settings.ConfigPath);
    }

    [Fact]
    public async Task InMemoryTokenVaultTrimsToken()
    {
        var vault = new InMemoryTokenVault();

        await vault.SaveTokenAsync(" secret-token ");

        Assert.Equal("secret-token", await vault.ReadTokenAsync());
    }

    [Fact]
    public void DaemonSupervisorBuildsExpectedArguments()
    {
        var settings = new AppSettings
        {
            ApiEndpoint = "http://127.0.0.1:9090",
            ConfigPath = "C:\\config\\example.toml"
        };

        var args = DaemonSupervisor.BuildArguments(settings, "secret-token");

        var expected = "-api \"http://127.0.0.1:9090\" -api-token \"secret-token\" -config \"C:\\config\\example.toml\"";
        Assert.Equal(expected, args);
    }

    [Fact]
    public void DaemonSupervisorPrefersConfiguredPathThenBundledPath()
    {
        var root = Path.Combine(Path.GetTempPath(), Guid.NewGuid().ToString("N"));
        Directory.CreateDirectory(root);
        var configured = Path.Combine(root, "configured.exe");
        var bundled = Path.Combine(root, "clambhook.exe");
        File.WriteAllText(bundled, "");

        Assert.Equal(bundled, DaemonSupervisor.ResolveExecutablePath(new AppSettings(), root));

        File.WriteAllText(configured, "");
        Assert.Equal(configured, DaemonSupervisor.ResolveExecutablePath(new AppSettings { DaemonPath = configured }, root));
    }
}
