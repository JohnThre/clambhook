using System.Text.Json;

namespace Clambhook.Windows.Core;

public sealed class AppSettings
{
    public string ApiEndpoint { get; set; } = "http://127.0.0.1:9090";
    public string DaemonPath { get; set; } = "";
    public string ConfigPath { get; set; } = "";
    public bool LaunchDaemonOnStart { get; set; } = true;
    public bool StopDaemonOnExit { get; set; } = true;
    public bool EventStreamEnabled { get; set; } = true;
    public bool MinimizeToTray { get; set; } = true;
    public int RefreshIntervalSeconds { get; set; } = 5;

    public AppSettings Normalized()
    {
        return new AppSettings
        {
            ApiEndpoint = NormalizeEndpoint(ApiEndpoint),
            DaemonPath = DaemonPath.Trim(),
            ConfigPath = ConfigPath.Trim(),
            LaunchDaemonOnStart = LaunchDaemonOnStart,
            StopDaemonOnExit = StopDaemonOnExit,
            EventStreamEnabled = EventStreamEnabled,
            MinimizeToTray = MinimizeToTray,
            RefreshIntervalSeconds = Math.Clamp(RefreshIntervalSeconds, 2, 60)
        };
    }

    private static string NormalizeEndpoint(string value)
    {
        var trimmed = value.Trim().TrimEnd('/');
        return string.IsNullOrWhiteSpace(trimmed) ? "http://127.0.0.1:9090" : trimmed;
    }
}

public interface ISettingsStore
{
    Task<AppSettings> LoadAsync(CancellationToken cancellationToken = default);
    Task SaveAsync(AppSettings settings, CancellationToken cancellationToken = default);
}

public sealed class FileSettingsStore : ISettingsStore
{
    private readonly string _path;

    public FileSettingsStore(string? path = null)
    {
        _path = path ?? DefaultPath();
    }

    public async Task<AppSettings> LoadAsync(CancellationToken cancellationToken = default)
    {
        if (!File.Exists(_path))
        {
            return new AppSettings();
        }

        await using var stream = File.OpenRead(_path);
        var settings = await JsonSerializer.DeserializeAsync<AppSettings>(stream, ApiJson.Options, cancellationToken);
        return (settings ?? new AppSettings()).Normalized();
    }

    public async Task SaveAsync(AppSettings settings, CancellationToken cancellationToken = default)
    {
        var normalized = settings.Normalized();
        Directory.CreateDirectory(Path.GetDirectoryName(_path)!);
        await using var stream = File.Create(_path);
        await JsonSerializer.SerializeAsync(stream, normalized, ApiJson.Options, cancellationToken);
    }

    private static string DefaultPath()
    {
        var root = Path.Combine(
            Environment.GetFolderPath(Environment.SpecialFolder.ApplicationData),
            "clambhook");
        return Path.Combine(root, "windows-settings.json");
    }
}
