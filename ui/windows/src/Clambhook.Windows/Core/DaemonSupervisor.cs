using System.Diagnostics;
using System.Text;

namespace Clambhook.Windows.Core;

public sealed class DaemonSupervisor
{
    private Process? _process;

    public bool IsRunning => _process is { HasExited: false };

    public Task StartAsync(AppSettings settings, string token, string appBaseDirectory, CancellationToken cancellationToken = default)
    {
        if (IsRunning)
        {
            return Task.CompletedTask;
        }

        var executable = ResolveExecutablePath(settings, appBaseDirectory);
        if (executable is null)
        {
            throw new FileNotFoundException("clambhook daemon executable was not found");
        }

        _process = Process.Start(new ProcessStartInfo
        {
            FileName = executable,
            Arguments = BuildArguments(settings, token),
            UseShellExecute = false,
            CreateNoWindow = true,
            WorkingDirectory = Path.GetDirectoryName(executable) ?? appBaseDirectory
        });
        return Task.CompletedTask;
    }

    public Task StopAsync()
    {
        if (_process is { HasExited: false } process)
        {
            process.Kill(entireProcessTree: true);
            process.Dispose();
        }

        _process = null;
        return Task.CompletedTask;
    }

    public static string? ResolveExecutablePath(AppSettings settings, string appBaseDirectory)
    {
        var configured = settings.DaemonPath.Trim();
        if (!string.IsNullOrEmpty(configured) && File.Exists(configured))
        {
            return configured;
        }

        var bundled = Path.Combine(appBaseDirectory, "clambhook.exe");
        return File.Exists(bundled) ? bundled : null;
    }

    public static string BuildArguments(AppSettings settings, string token)
    {
        var args = new List<string> { "-api", Quote(settings.Normalized().ApiEndpoint) };
        if (!string.IsNullOrWhiteSpace(token))
        {
            args.Add("-api-token");
            args.Add(Quote(token.Trim()));
        }

        if (!string.IsNullOrWhiteSpace(settings.ConfigPath))
        {
            args.Add("-config");
            args.Add(Quote(settings.ConfigPath.Trim()));
        }

        return string.Join(" ", args);
    }

    private static string Quote(string value)
    {
        var escaped = value.Replace("\"", "\\\"");
        return $"\"{escaped}\"";
    }
}

public static class Formatters
{
    public static string FormatRate(double bytesPerSecond)
    {
        var units = new[] { "B/s", "KB/s", "MB/s", "GB/s" };
        var value = bytesPerSecond;
        var unit = 0;
        while (value >= 1024 && unit < units.Length - 1)
        {
            value /= 1024;
            unit++;
        }

        return unit == 0 ? $"{(int)value} {units[unit]}" : $"{value:0.0} {units[unit]}";
    }

    public static string FormatBytes(ulong bytes)
    {
        var units = new[] { "B", "KB", "MB", "GB" };
        var value = (double)bytes;
        var unit = 0;
        while (value >= 1024 && unit < units.Length - 1)
        {
            value /= 1024;
            unit++;
        }

        return unit == 0 ? $"{(ulong)value} {units[unit]}" : $"{value:0.0} {units[unit]}";
    }

    public static string FormatDurationNs(long ns)
    {
        if (ns <= 0)
        {
            return "--";
        }

        var seconds = ns / 1_000_000_000;
        if (seconds < 1)
        {
            return $"{ns / 1_000_000} ms";
        }

        return seconds < 60 ? $"{seconds} s" : $"{seconds / 60} min";
    }

    public static string ServerLocation(ServerPayload server)
    {
        var parts = new[] { server.Geo.City, server.Geo.Country }.Where(part => !string.IsNullOrWhiteSpace(part));
        var location = string.Join(", ", parts);
        return string.IsNullOrWhiteSpace(location) ? server.Address : location;
    }
}
