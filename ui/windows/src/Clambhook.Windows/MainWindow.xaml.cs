using Clambhook.Windows.Core;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;

namespace Clambhook.Windows;

public sealed partial class MainWindow : Window
{
    private readonly MainViewModel _model;
    private readonly TrayIconService _trayIcon;
    private bool _updatingProfiles;

    public MainWindow(MainViewModel model)
    {
        _model = model;
        InitializeComponent();
        _model.StateChanged += QueueRefresh;
        _trayIcon = new TrayIconService(
            showWindow: ShowFromTray,
            connectOrDisconnect: async () => await ConnectOrDisconnectAsync(),
            startOrStopDaemon: async () => await StartOrStopDaemonAsync(),
            refresh: async () => await _model.RefreshDashboardAsync(),
            quit: Close);
        Closed += MainWindow_Closed;
        _ = InitializeAsync();
    }

    private async Task InitializeAsync()
    {
        await _model.InitializeAsync(AppContext.BaseDirectory);
        LoadSettingsFields();
        RefreshUi();
    }

    private void MainWindow_Closed(object sender, WindowEventArgs args)
    {
        _trayIcon.Dispose();
        _ = _model.ShutdownAsync();
    }

    private void QueueRefresh()
    {
        DispatcherQueue.TryEnqueue(RefreshUi);
    }

    private void RefreshUi()
    {
        var state = _model.Store.State;
        StatusText.Text = state.Status.Running ? "Running" : "Stopped";
        ApiText.Text = state.ApiOnline ? $"API online · {state.ActiveProfile}" : "API offline";
        ErrorText.Text = state.ErrorText;
        BandwidthText.Text = $"RX {Formatters.FormatRate(state.CurrentBandwidth.RxBps)}  TX {Formatters.FormatRate(state.CurrentBandwidth.TxBps)}";
        ConnectionsText.Text = $"Active connections {state.ActiveConnections}";
        TrafficSummaryText.Text =
            $"{state.Traffic.Summary.ActiveConnections} active · " +
            $"{Formatters.FormatRate(state.Traffic.Summary.RxBps)} down · " +
            $"{Formatters.FormatRate(state.Traffic.Summary.TxBps)} up · " +
            $"{Formatters.FormatBytes(state.Traffic.Summary.RxTotal)} down total · " +
            $"{Formatters.FormatBytes(state.Traffic.Summary.TxTotal)} up total";
        DaemonText.Text = _model.DaemonMessage;

        _updatingProfiles = true;
        ProfilesComboBox.ItemsSource = state.Profiles.Profiles;
        ProfilesComboBox.SelectedItem = state.ActiveProfile;
        _updatingProfiles = false;

        ListenersList.ItemsSource = state.Status.Listeners
            .Select(listener => $"{listener.Protocol} {listener.Addr} ({listener.ActiveConnections})")
            .ToList();
        ServersList.ItemsSource = state.Servers.Chains
            .SelectMany(chain => chain.Servers.Select(server => $"{chain.Name}: {server.Name} · {server.Protocol} · {Formatters.ServerLocation(server)}"))
            .DefaultIfEmpty("No servers in active profile")
            .ToList();
        TrafficList.ItemsSource = state.Traffic.Connections
            .Take(12)
            .Select(connection =>
            {
                var label = string.Join(" · ", new[] { connection.Application, connection.Network, connection.ChainName }
                    .Where(part => !string.IsNullOrWhiteSpace(part)));
                if (string.IsNullOrWhiteSpace(label))
                {
                    label = connection.Listener.Protocol;
                }

                return $"{connection.State}  {EmptyDash(connection.Target)}  {label}  " +
                    $"{Formatters.FormatBytes(connection.RxTotal)} down / {Formatters.FormatBytes(connection.TxTotal)} up  " +
                    $"{Formatters.FormatDurationNs(connection.DurationNs)}";
            })
            .DefaultIfEmpty("No traffic history")
            .ToList();
        LogsList.ItemsSource = state.Logs.TakeLast(50).Reverse().ToList();
        _trayIcon.Update(state, _model.Daemon.IsRunning);
    }

    private void LoadSettingsFields()
    {
        var settings = _model.Settings;
        EndpointBox.Text = settings.ApiEndpoint;
        TokenBox.Password = _model.ApiToken;
        DaemonPathBox.Text = settings.DaemonPath;
        ConfigPathBox.Text = settings.ConfigPath;
        RefreshSecondsBox.Text = settings.RefreshIntervalSeconds.ToString();
        LaunchDaemonBox.IsChecked = settings.LaunchDaemonOnStart;
        StopDaemonBox.IsChecked = settings.StopDaemonOnExit;
        EventsBox.IsChecked = settings.EventStreamEnabled;
        TrayBox.IsChecked = settings.MinimizeToTray;
    }

    private async void RefreshButton_Click(object sender, RoutedEventArgs e)
    {
        await _model.RefreshDashboardAsync();
    }

    private async void ConnectButton_Click(object sender, RoutedEventArgs e)
    {
        await _model.Store.ConnectAsync();
    }

    private async void DisconnectButton_Click(object sender, RoutedEventArgs e)
    {
        await _model.Store.DisconnectAsync();
    }

    private async void ProfilesComboBox_SelectionChanged(object sender, SelectionChangedEventArgs e)
    {
        if (_updatingProfiles || ProfilesComboBox.SelectedItem is not string profile)
        {
            return;
        }

        await _model.Store.SetActiveProfileAsync(profile);
    }

    private async void StartDaemonButton_Click(object sender, RoutedEventArgs e)
    {
        await _model.StartDaemonAsync();
    }

    private async void StopDaemonButton_Click(object sender, RoutedEventArgs e)
    {
        await _model.StopDaemonAsync();
    }

    private async void SaveSettingsButton_Click(object sender, RoutedEventArgs e)
    {
        var settings = new AppSettings
        {
            ApiEndpoint = EndpointBox.Text,
            DaemonPath = DaemonPathBox.Text,
            ConfigPath = ConfigPathBox.Text,
            RefreshIntervalSeconds = int.TryParse(RefreshSecondsBox.Text, out var refreshSeconds) ? refreshSeconds : 5,
            LaunchDaemonOnStart = LaunchDaemonBox.IsChecked == true,
            StopDaemonOnExit = StopDaemonBox.IsChecked == true,
            EventStreamEnabled = EventsBox.IsChecked == true,
            MinimizeToTray = TrayBox.IsChecked == true
        };
        await _model.SaveSettingsAsync(settings, TokenBox.Password);
        LoadSettingsFields();
    }

    private void ShowFromTray()
    {
        DispatcherQueue.TryEnqueue(Activate);
    }

    private static string EmptyDash(string value) => string.IsNullOrWhiteSpace(value) ? "--" : value;

    private async Task ConnectOrDisconnectAsync()
    {
        if (_model.Store.State.Status.Running)
        {
            await _model.Store.DisconnectAsync();
        }
        else
        {
            await _model.Store.ConnectAsync();
        }
    }

    private async Task StartOrStopDaemonAsync()
    {
        if (_model.Daemon.IsRunning)
        {
            await _model.StopDaemonAsync();
        }
        else
        {
            await _model.StartDaemonAsync();
        }
    }
}
