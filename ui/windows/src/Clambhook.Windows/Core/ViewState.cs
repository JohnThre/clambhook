namespace Clambhook.Windows.Core;

public sealed record MetricTile(string Glyph, string Title, string Value, string Detail);

public sealed record DetailRow(string Glyph, string Primary, string Secondary, string Meta = "", string Badge = "")
{
    public double BadgeOpacity => string.IsNullOrWhiteSpace(Badge) ? 0 : 1;
}

public sealed record DashboardViewState
{
    public string StatusText { get; init; } = "Stopped";
    public string StatusDetail { get; init; } = "API offline";
    public string StatusBadge { get; init; } = "Stopped";
    public string ApiText { get; init; } = "API offline";
    public string ActiveProfile { get; init; } = "No profile";
    public string DaemonText { get; init; } = "Daemon stopped";
    public string DaemonMessage { get; init; } = "";
    public string ErrorText { get; init; } = "";
    public string BusyMessage { get; init; } = "";
    public bool IsBusy { get; init; }
    public string ConnectionActionText { get; init; } = "Connect";
    public string ConnectionActionGlyph { get; init; } = "\uE768";
    public bool CanRefresh { get; init; }
    public bool CanToggleConnection { get; init; }
    public bool CanStartDaemon { get; init; }
    public bool CanStopDaemon { get; init; }
    public bool CanSwitchProfile { get; init; }
    public IReadOnlyList<string> Profiles { get; init; } = [];
    public IReadOnlyList<MetricTile> Metrics { get; init; } = [];
    public IReadOnlyList<DetailRow> Listeners { get; init; } = [];
    public IReadOnlyList<DetailRow> Servers { get; init; } = [];
    public IReadOnlyList<DetailRow> Traffic { get; init; } = [];
    public IReadOnlyList<DetailRow> Logs { get; init; } = [];
    public bool HasListeners => Listeners.Count > 0;
    public bool HasServers => Servers.Count > 0;
    public bool HasTraffic => Traffic.Count > 0;
    public bool HasLogs => Logs.Count > 0;

    public static DashboardViewState From(
        DashboardState state,
        DaemonSupervisor daemon,
        bool isBusy,
        string busyMessage,
        string appMessage = "")
    {
        var running = state.Status.Running;
        var profile = string.IsNullOrWhiteSpace(state.ActiveProfile) ? "No profile" : state.ActiveProfile;
        var traffic = state.Traffic.Summary;
        var apiText = state.ApiOnline ? "API online" : "API offline";
        var statusDetail = state.ApiOnline ? $"{apiText} / {profile}" : apiText;
        var connectionActionText = running ? "Disconnect" : "Connect";

        return new DashboardViewState
        {
            StatusText = running ? "Running" : "Stopped",
            StatusBadge = running ? "Running" : "Stopped",
            StatusDetail = statusDetail,
            ApiText = apiText,
            ActiveProfile = profile,
            DaemonText = daemon.StateLabel,
            DaemonMessage = string.IsNullOrWhiteSpace(daemon.Message) ? appMessage : daemon.Message,
            ErrorText = state.ErrorText,
            IsBusy = isBusy,
            BusyMessage = busyMessage,
            ConnectionActionText = connectionActionText,
            ConnectionActionGlyph = running ? "\uE769" : "\uE768",
            CanRefresh = !isBusy,
            CanToggleConnection = state.ApiOnline && !isBusy,
            CanStartDaemon = !daemon.IsRunning && !daemon.IsBusy && !isBusy,
            CanStopDaemon = daemon.IsRunning && !daemon.IsBusy && !isBusy,
            CanSwitchProfile = state.ApiOnline && state.Profiles.Profiles.Count > 0 && !isBusy,
            Profiles = state.Profiles.Profiles,
            Metrics =
            [
                new MetricTile("\uE81E", "Connections", state.ActiveConnections.ToString(), $"{traffic.ActiveConnections} active in traffic history"),
                new MetricTile("\uE896", "Down", Formatters.FormatRate(traffic.RxBps), $"{Formatters.FormatBytes(traffic.RxTotal)} total"),
                new MetricTile("\uE898", "Up", Formatters.FormatRate(traffic.TxBps), $"{Formatters.FormatBytes(traffic.TxTotal)} total"),
                new MetricTile("\uE8A7", "Profile", profile, running ? "Connected profile" : "Selected profile")
            ],
            Listeners = ListenerRows(state.Status.Listeners),
            Servers = ServerRows(state.Servers),
            Traffic = TrafficRows(state.Traffic.Connections),
            Logs = LogRows(state.Logs)
        };
    }

    private static IReadOnlyList<DetailRow> ListenerRows(IReadOnlyList<ListenerStatusPayload> listeners)
    {
        return listeners
            .Select(listener => new DetailRow(
                "\uE968",
                listener.Protocol.ToUpperInvariant(),
                listener.Addr,
                $"{listener.ActiveConnections} active",
                listener.ActiveConnections > 0 ? "Active" : "Idle"))
            .ToList();
    }

    private static IReadOnlyList<DetailRow> ServerRows(ServersPayload servers)
    {
        return servers.Chains
            .SelectMany(chain => chain.Servers.Select(server => new DetailRow(
                "\uE968",
                server.Name,
                $"{chain.Name} / {server.Protocol} / {Formatters.ServerLocation(server)}",
                server.Address,
                server.Geo.CountryCode)))
            .ToList();
    }

    private static IReadOnlyList<DetailRow> TrafficRows(IReadOnlyList<TrafficConnectionPayload> connections)
    {
        return connections
            .Take(16)
            .Select(connection => new DetailRow(
                "\uE9D9",
                EmptyDash(connection.Target),
                TrafficLabelFor(connection),
                $"{connection.State} / {Formatters.FormatBytes(connection.RxTotal)} down / {Formatters.FormatBytes(connection.TxTotal)} up / {Formatters.FormatDurationNs(connection.DurationNs)}",
                connection.Network))
            .ToList();
    }

    private static IReadOnlyList<DetailRow> LogRows(IReadOnlyList<string> logs)
    {
        return logs
            .TakeLast(50)
            .Reverse()
            .Select(line => new DetailRow("\uE8D2", line, ""))
            .ToList();
    }

    private static string TrafficLabelFor(TrafficConnectionPayload connection)
    {
        var label = string.Join(" / ", new[] { connection.Application, connection.Network, connection.ChainName }
            .Where(part => !string.IsNullOrWhiteSpace(part)));
        return string.IsNullOrWhiteSpace(label) ? connection.Listener.Protocol : label;
    }

    private static string EmptyDash(string value) => string.IsNullOrWhiteSpace(value) ? "--" : value;
}
