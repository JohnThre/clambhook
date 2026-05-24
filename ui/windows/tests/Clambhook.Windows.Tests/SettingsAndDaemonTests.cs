using System.Xml.Linq;
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
    public void SettingsValidatesAndFallsBackUnsupportedApiEndpoint()
    {
        Assert.True(AppSettings.IsSupportedApiEndpoint("https://proxy.example.test:9443/"));
        Assert.True(AppSettings.IsSupportedApiEndpoint("http://[::1]:9091/"));
        Assert.False(AppSettings.IsSupportedApiEndpoint("ftp://proxy.example.test"));

        var settings = new AppSettings { ApiEndpoint = "ftp://proxy.example.test" }.Normalized();

        Assert.Equal(AppSettings.DefaultApiEndpoint, settings.ApiEndpoint);
    }

    [Fact]
    public async Task InMemoryTokenVaultTrimsToken()
    {
        var vault = new InMemoryTokenVault();

        await vault.SaveTokenAsync(" secret-token ");

        Assert.Equal("secret-token", await vault.ReadTokenAsync());
    }

    [Fact]
    public void DefaultSettingsLaunchBundledDaemon()
    {
        Assert.True(new AppSettings().LaunchDaemonOnStart);
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

        var expected = "-api \"127.0.0.1:9090\" -api-token \"secret-token\" -config \"C:\\config\\example.toml\"";
        Assert.Equal(expected, args);
    }

    [Fact]
    public void DaemonSupervisorConvertsApiEndpointToListenAddress()
    {
        Assert.Equal("127.0.0.1:9090", DaemonSupervisor.ApiListenAddress("http://127.0.0.1:9090"));
        Assert.Equal("[::1]:9091", DaemonSupervisor.ApiListenAddress("http://[::1]:9091/"));
        Assert.Equal("proxy.example.test", DaemonSupervisor.ApiListenAddress("https://proxy.example.test"));
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

    [Fact]
    public void WindowsAppProjectPublishesSelfContainedWithBundledDaemon()
    {
        var projectPath = FindFromCurrentDirectory(Path.Combine("ui", "windows", "src", "Clambhook.Windows", "Clambhook.Windows.csproj"));
        var project = XDocument.Load(projectPath);

        Assert.Equal("true", ProjectProperty(project, "WindowsAppSDKSelfContained"));
        Assert.Equal("true", ProjectProperty(project, "PublishSelfContained"));
        Assert.Equal(@"Assets\clambhook.ico", ProjectProperty(project, "ApplicationIcon"));
        Assert.Contains("win-x64", ProjectProperty(project, "RuntimeIdentifiers").Split(';'));
        Assert.Contains("win-arm64", ProjectProperty(project, "RuntimeIdentifiers").Split(';'));
        Assert.Contains(project.Descendants("Content"), content => content.Attribute("Include")?.Value == @"Assets\clambhook.ico");

        var targetNames = project.Descendants("Target")
            .Select(target => target.Attribute("Name")?.Value)
            .OfType<string>()
            .ToHashSet();

        Assert.Contains("RequireClambhookDaemonOnPublish", targetNames);
        Assert.Contains("CopyClambhookDaemonOnPublish", targetNames);
    }

    private static string ProjectProperty(XDocument project, string propertyName)
    {
        return project.Descendants(propertyName).Single().Value;
    }

    private static string FindFromCurrentDirectory(string relativePath)
    {
        var directory = new DirectoryInfo(Environment.CurrentDirectory);
        while (directory is not null)
        {
            var candidate = Path.Combine(directory.FullName, relativePath);
            if (File.Exists(candidate))
            {
                return candidate;
            }

            directory = directory.Parent;
        }

        throw new FileNotFoundException($"Could not find {relativePath} from {Environment.CurrentDirectory}");
    }
}
