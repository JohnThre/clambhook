using System.Diagnostics;

namespace Clambhook.Windows.Core;

public enum DaemonState
{
    Stopped,
    Starting,
    Running,
    Stopping,
    Failed
}

public sealed class DaemonSupervisor
{
    private Process? _process;

    public event Action? StateChanged;

    public DaemonState State { get; private set; } = DaemonState.Stopped;
    public string Message { get; private set; } = "";
    public bool IsRunning => _process is { HasExited: false };
    public bool IsBusy => State is DaemonState.Starting or DaemonState.Stopping;
    public string StateLabel => State switch
    {
        DaemonState.Stopped => "Daemon stopped",
        DaemonState.Starting => "Daemon starting",
        DaemonState.Running => "Daemon running",
        DaemonState.Stopping => "Daemon stopping",
        DaemonState.Failed => "Daemon failed",
        _ => "Daemon"
    };

    public Task StartAsync(AppSettings settings, string token, string appBaseDirectory, CancellationToken cancellationToken = default)
    {
        if (IsRunning)
        {
            SetState(DaemonState.Running, "daemon already running");
            return Task.CompletedTask;
        }

        SetState(DaemonState.Starting, "");
        var executable = ResolveExecutablePath(settings, appBaseDirectory);
        if (executable is null)
        {
            SetState(DaemonState.Failed, "clambhook daemon executable was not found");
            throw new FileNotFoundException("clambhook daemon executable was not found");
        }

        var startInfo = new ProcessStartInfo
        {
            FileName = executable,
            UseShellExecute = false,
            CreateNoWindow = true,
            WorkingDirectory = Path.GetDirectoryName(executable) ?? appBaseDirectory
        };
        foreach (var argument in BuildArgumentList(settings, token))
        {
            startInfo.ArgumentList.Add(argument);
        }

        try
        {
            _process = Process.Start(startInfo);
            if (_process is null)
            {
                throw new InvalidOperationException("failed to launch clambhook daemon");
            }

            _process.EnableRaisingEvents = true;
            _process.Exited += Process_Exited;
            SetState(DaemonState.Running, "daemon launched");
        }
        catch (Exception error)
        {
            _process = null;
            SetState(DaemonState.Failed, error.Message);
            throw;
        }

        return Task.CompletedTask;
    }

    public Task StopAsync()
    {
        if (_process is { HasExited: false } process)
        {
            SetState(DaemonState.Stopping, "");
            process.Exited -= Process_Exited;
            process.Kill(entireProcessTree: true);
            process.WaitForExit(3000);
            process.Dispose();
        }

        _process = null;
        SetState(DaemonState.Stopped, "daemon stopped");
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
        return string.Join(" ", BuildArgumentList(settings, token).Select(argument =>
            argument.StartsWith('-') ? argument : Quote(argument)));
    }

    public static IReadOnlyList<string> BuildArgumentList(AppSettings settings, string token)
    {
        var normalized = settings.Normalized();
        var args = new List<string> { "-api", ApiListenAddress(normalized.ApiEndpoint) };
        if (!string.IsNullOrWhiteSpace(token))
        {
            args.Add("-api-token");
            args.Add(token.Trim());
        }

        if (!string.IsNullOrWhiteSpace(normalized.ConfigPath))
        {
            args.Add("-config");
            args.Add(normalized.ConfigPath);
        }

        return args;
    }

    public static string ApiListenAddress(string endpoint)
    {
        var normalized = AppSettings.NormalizeEndpoint(endpoint);
        if (Uri.TryCreate(normalized, UriKind.Absolute, out var uri) && !string.IsNullOrWhiteSpace(uri.Host))
        {
            var host = uri.Host;
            if (host.Contains(':') && !host.StartsWith('['))
            {
                host = $"[{host}]";
            }

            return uri.IsDefaultPort ? host : $"{host}:{uri.Port}";
        }

        return normalized;
    }

    private static string Quote(string value)
    {
        var escaped = value.Replace("\"", "\\\"");
        return $"\"{escaped}\"";
    }

    private void Process_Exited(object? sender, EventArgs e)
    {
        if (sender is not Process exited || !ReferenceEquals(exited, _process))
        {
            return;
        }

        var exitCode = 0;
        try
        {
            exitCode = exited.ExitCode;
        }
        catch
        {
        }

        exited.Dispose();
        _process = null;
        if (State != DaemonState.Failed)
        {
            SetState(
                exitCode == 0 ? DaemonState.Stopped : DaemonState.Failed,
                exitCode == 0 ? "daemon exited" : $"daemon exited with code {exitCode}");
        }
    }

    private void SetState(DaemonState state, string message)
    {
        State = state;
        Message = message;
        StateChanged?.Invoke();
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
