using System.ComponentModel;
using System.Runtime.CompilerServices;

namespace Clambhook.Windows.Core;

public sealed record DashboardState
{
    public StatusPayload Status { get; init; } = new();
    public ProfilesPayload Profiles { get; init; } = new();
    public ServersPayload Servers { get; init; } = new();
    public TrafficSnapshotPayload Traffic { get; init; } = new();
    public IReadOnlyList<BandwidthSample> BandwidthSamples { get; init; } = [];
    public IReadOnlyList<string> Logs { get; init; } = [];
    public bool ApiOnline { get; init; }
    public string ErrorText { get; init; } = "";
    public string ActiveProfile => string.IsNullOrWhiteSpace(Profiles.Active) ? Status.Profile : Profiles.Active;
    public BandwidthSample CurrentBandwidth => BandwidthSamples.Count == 0 ? new BandwidthSample() : BandwidthSamples[^1];
    public int ActiveConnections => Status.Listeners.Sum(listener => listener.ActiveConnections);
}

public sealed class DashboardStore : INotifyPropertyChanged
{
    public const int BandwidthSampleLimit = 60;
    public const int MaxLogLines = 200;
    private readonly IClambhookApi _api;
    private DashboardState _state = new();

    public DashboardStore(IClambhookApi api)
    {
        _api = api;
    }

    public event PropertyChangedEventHandler? PropertyChanged;

    public DashboardState State
    {
        get => _state;
        private set
        {
            _state = value;
            OnPropertyChanged();
        }
    }

    public async Task RefreshDashboardAsync(CancellationToken cancellationToken = default)
    {
        try
        {
            var status = await _api.GetStatusAsync(cancellationToken);
            var profiles = await _api.GetProfilesAsync(cancellationToken);
            var servers = await _api.GetServersAsync(cancellationToken);
            var traffic = await _api.GetTrafficAsync(cancellationToken);
            State = State with
            {
                Status = status,
                Profiles = profiles,
                Servers = servers,
                Traffic = traffic,
                ApiOnline = true,
                ErrorText = ""
            };
        }
        catch (Exception error)
        {
            MarkOffline(error);
        }
    }

    public async Task RefreshStatusAsync(CancellationToken cancellationToken = default)
    {
        try
        {
            var status = await _api.GetStatusAsync(cancellationToken);
            var traffic = await _api.GetTrafficAsync(cancellationToken);
            State = State with { Status = status, Traffic = traffic, ApiOnline = true, ErrorText = "" };
        }
        catch (Exception error)
        {
            MarkOffline(error);
        }
    }

    public async Task ConnectAsync(CancellationToken cancellationToken = default)
    {
        await PerformActionAsync(() => _api.ConnectAsync(cancellationToken), cancellationToken);
    }

    public async Task DisconnectAsync(CancellationToken cancellationToken = default)
    {
        await PerformActionAsync(() => _api.DisconnectAsync(cancellationToken), cancellationToken);
    }

    public async Task SetActiveProfileAsync(string name, CancellationToken cancellationToken = default)
    {
        await PerformActionAsync(() => _api.SetActiveProfileAsync(name, cancellationToken), cancellationToken);
    }

    public void ApplyEvent(DaemonEvent daemonEvent)
    {
        switch (daemonEvent.Type)
        {
            case "connection.bytes":
                ApplyConnectionBytes(daemonEvent);
                break;
            case "log.line":
                ApplyLogLine(daemonEvent);
                break;
        }
    }

    private async Task PerformActionAsync(Func<Task> action, CancellationToken cancellationToken)
    {
        try
        {
            await action();
            await RefreshDashboardAsync(cancellationToken);
        }
        catch (Exception error)
        {
            MarkOffline(error);
        }
    }

    private void MarkOffline(Exception error)
    {
        State = State with { ApiOnline = false, ErrorText = error.Message };
    }

    private void ApplyConnectionBytes(DaemonEvent daemonEvent)
    {
        if (!daemonEvent.Data.TryGetValue("rx_delta", out var rxElement) ||
            !daemonEvent.Data.TryGetValue("tx_delta", out var txElement) ||
            !daemonEvent.Data.TryGetValue("interval_ns", out var intervalElement))
        {
            return;
        }

        var rxDelta = rxElement.DoubleValueOrNull();
        var txDelta = txElement.DoubleValueOrNull();
        var intervalNs = intervalElement.DoubleValueOrNull();
        if (rxDelta is null || txDelta is null || intervalNs is null or <= 0)
        {
            return;
        }

        var seconds = intervalNs.Value / 1_000_000_000.0;
        var sample = new BandwidthSample(rxDelta.Value / seconds, txDelta.Value / seconds);
        State = State with { BandwidthSamples = State.BandwidthSamples.Concat([sample]).TakeLast(BandwidthSampleLimit).ToList() };
    }

    private void ApplyLogLine(DaemonEvent daemonEvent)
    {
        if (!daemonEvent.Data.TryGetValue("line", out var lineElement))
        {
            return;
        }

        var line = lineElement.StringValueOrNull();
        if (string.IsNullOrEmpty(line))
        {
            return;
        }

        State = State with { Logs = State.Logs.Concat([line]).TakeLast(MaxLogLines).ToList() };
    }

    private void OnPropertyChanged([CallerMemberName] string? propertyName = null)
    {
        PropertyChanged?.Invoke(this, new PropertyChangedEventArgs(propertyName));
    }
}
