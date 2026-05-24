using System.Runtime.InteropServices;
using Clambhook.Windows.Core;
using Microsoft.UI;
using Microsoft.UI.Windowing;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Media;
using Windows.Graphics;
using WinRT.Interop;
using Forms = System.Windows.Forms;

namespace Clambhook.Windows;

public sealed partial class MainWindow : Window
{
    private const int ShowWindowHidden = 0;
    private const int ShowWindowNormal = 1;
    private readonly MainViewModel _model;
    private readonly TrayIconService _trayIcon;
    private readonly nint _windowHandle;
    private bool _updatingProfiles;
    private bool _allowClose;

    public MainWindow(MainViewModel model)
    {
        _model = model;
        InitializeComponent();
        _windowHandle = WindowNative.GetWindowHandle(this);
        ConfigureWindow();
        _model.StateChanged += QueueRefresh;
        _trayIcon = new TrayIconService(
            iconPath: AppIconPath(),
            showWindow: ShowFromTray,
            connectOrDisconnect: async () => await ConnectOrDisconnectAsync(),
            startOrStopDaemon: async () => await StartOrStopDaemonAsync(),
            refresh: async () => await _model.RefreshDashboardAsync(),
            quit: QuitFromTray);
        AppWindow.Closing += MainWindow_Closing;
        Closed += MainWindow_Closed;
        _ = InitializeAsync();
    }

    private async Task InitializeAsync()
    {
        await _model.InitializeAsync(AppContext.BaseDirectory);
        LoadSettingsFields();
        RefreshUi();
    }

    private void ConfigureWindow()
    {
        var iconPath = AppIconPath();
        if (File.Exists(iconPath))
        {
            AppWindow.SetIcon(iconPath);
        }

        AppWindow.Resize(new SizeInt32(1080, 760));
    }

    private void MainWindow_Closing(AppWindow sender, AppWindowClosingEventArgs args)
    {
        if (!_allowClose && _model.Settings.MinimizeToTray)
        {
            args.Cancel = true;
            HideToTray();
        }
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
        var view = DashboardViewState.From(
            _model.Store.State,
            _model.Daemon,
            _model.IsBusy,
            _model.BusyMessage,
            _model.DaemonMessage);
        StatusText.Text = view.StatusText;
        StatusDetailText.Text = view.StatusDetail;
        StatusBadgeText.Text = view.StatusBadge;
        ApiText.Text = view.ApiText;
        DaemonText.Text = view.DaemonText;
        DaemonMessageText.Text = view.DaemonMessage;
        DaemonMessageText.Visibility = string.IsNullOrWhiteSpace(view.DaemonMessage) ? Visibility.Collapsed : Visibility.Visible;
        ConnectionButtonText.Text = view.ConnectionActionText;
        ConnectionButtonIcon.Glyph = view.ConnectionActionGlyph;
        RefreshButton.IsEnabled = view.CanRefresh;
        ConnectionButton.IsEnabled = view.CanToggleConnection;
        StartDaemonButton.IsEnabled = view.CanStartDaemon;
        StopDaemonButton.IsEnabled = view.CanStopDaemon;
        ProfilesComboBox.IsEnabled = view.CanSwitchProfile;
        ErrorInfoBar.Message = view.ErrorText;
        ErrorInfoBar.IsOpen = !string.IsNullOrWhiteSpace(view.ErrorText);
        BusyText.Text = view.BusyMessage;
        BusyInfoBar.IsOpen = view.IsBusy;
        StatusBadgeBorder.Background = new SolidColorBrush(
            _model.Store.State.Status.Running ? ColorHelper.FromArgb(36, 32, 128, 80) : ColorHelper.FromArgb(36, 96, 96, 96));
        StatusBadgeText.Foreground = new SolidColorBrush(
            _model.Store.State.Status.Running ? ColorHelper.FromArgb(255, 20, 110, 62) : ColorHelper.FromArgb(255, 96, 96, 96));

        ApplyMetricTiles(view.Metrics);
        ApplyProfiles(view);
        ListenersList.ItemsSource = view.Listeners;
        ServersList.ItemsSource = view.Servers;
        TrafficList.ItemsSource = view.Traffic;
        LogsList.ItemsSource = view.Logs;
        ListenersEmptyText.Visibility = view.HasListeners ? Visibility.Collapsed : Visibility.Visible;
        ServersEmptyText.Visibility = view.HasServers ? Visibility.Collapsed : Visibility.Visible;
        TrafficEmptyText.Visibility = view.HasTraffic ? Visibility.Collapsed : Visibility.Visible;
        LogsEmptyText.Visibility = view.HasLogs ? Visibility.Collapsed : Visibility.Visible;
        _trayIcon.Update(_model.Store.State, _model.Daemon);
    }

    private void ApplyMetricTiles(IReadOnlyList<MetricTile> metrics)
    {
        if (metrics.Count < 4)
        {
            return;
        }

        ConnectionsMetricText.Text = metrics[0].Value;
        ConnectionsMetricDetailText.Text = metrics[0].Detail;
        DownMetricText.Text = metrics[1].Value;
        DownMetricDetailText.Text = metrics[1].Detail;
        UpMetricText.Text = metrics[2].Value;
        UpMetricDetailText.Text = metrics[2].Detail;
        ProfileMetricText.Text = metrics[3].Value;
        ProfileMetricDetailText.Text = metrics[3].Detail;
    }

    private void ApplyProfiles(DashboardViewState view)
    {
        _updatingProfiles = true;
        ProfilesComboBox.ItemsSource = view.Profiles;
        ProfilesComboBox.SelectedItem = _model.Store.State.ActiveProfile;
        _updatingProfiles = false;
    }

    private void LoadSettingsFields()
    {
        var settings = _model.Settings;
        EndpointBox.Text = settings.ApiEndpoint;
        TokenBox.Password = _model.ApiToken;
        DaemonPathBox.Text = settings.DaemonPath;
        ConfigPathBox.Text = settings.ConfigPath;
        RefreshSecondsBox.Value = settings.RefreshIntervalSeconds;
        LaunchDaemonBox.IsOn = settings.LaunchDaemonOnStart;
        StopDaemonBox.IsOn = settings.StopDaemonOnExit;
        EventsBox.IsOn = settings.EventStreamEnabled;
        TrayBox.IsOn = settings.MinimizeToTray;
        ValidateSettings();
    }

    private async void RefreshButton_Click(object sender, RoutedEventArgs e)
    {
        await _model.RefreshDashboardAsync();
    }

    private async void ConnectionButton_Click(object sender, RoutedEventArgs e)
    {
        await ConnectOrDisconnectAsync();
    }

    private async void ProfilesComboBox_SelectionChanged(object sender, SelectionChangedEventArgs e)
    {
        if (_updatingProfiles || ProfilesComboBox.SelectedItem is not string profile)
        {
            return;
        }

        await _model.SetActiveProfileAsync(profile);
    }

    private async void StartDaemonButton_Click(object sender, RoutedEventArgs e)
    {
        await _model.StartDaemonAsync();
    }

    private async void StopDaemonButton_Click(object sender, RoutedEventArgs e)
    {
        await _model.StopDaemonAsync();
    }

    private async void SettingsButton_Click(object sender, RoutedEventArgs e)
    {
        LoadSettingsFields();
        SettingsDialog.XamlRoot = RootGrid.XamlRoot;
        await SettingsDialog.ShowAsync();
    }

    private async void SettingsDialog_PrimaryButtonClick(ContentDialog sender, ContentDialogButtonClickEventArgs args)
    {
        if (!ValidateSettings())
        {
            args.Cancel = true;
            return;
        }

        var deferral = args.GetDeferral();
        try
        {
            await _model.SaveSettingsAsync(ReadSettingsFields(), TokenBox.Password);
            LoadSettingsFields();
        }
        finally
        {
            deferral.Complete();
        }
    }

    private void EndpointBox_TextChanged(object sender, TextChangedEventArgs e)
    {
        ValidateSettings();
    }

    private void BrowseDaemonButton_Click(object sender, RoutedEventArgs e)
    {
        BrowseForFile(DaemonPathBox, "Choose clambhook daemon", "Applications (*.exe)|*.exe|All files (*.*)|*.*");
    }

    private void BrowseConfigButton_Click(object sender, RoutedEventArgs e)
    {
        BrowseForFile(ConfigPathBox, "Choose clambhook config", "TOML files (*.toml)|*.toml|All files (*.*)|*.*");
    }

    private AppSettings ReadSettingsFields()
    {
        return new AppSettings
        {
            ApiEndpoint = EndpointBox.Text,
            DaemonPath = DaemonPathBox.Text,
            ConfigPath = ConfigPathBox.Text,
            RefreshIntervalSeconds = double.IsNaN(RefreshSecondsBox.Value) ? 5 : (int)RefreshSecondsBox.Value,
            LaunchDaemonOnStart = LaunchDaemonBox.IsOn,
            StopDaemonOnExit = StopDaemonBox.IsOn,
            EventStreamEnabled = EventsBox.IsOn,
            MinimizeToTray = TrayBox.IsOn
        };
    }

    private bool ValidateSettings()
    {
        if (SettingsValidationText is null)
        {
            return true;
        }

        if (AppSettings.IsSupportedApiEndpoint(EndpointBox.Text))
        {
            SettingsValidationText.Text = "";
            SettingsValidationText.Visibility = Visibility.Collapsed;
            return true;
        }

        SettingsValidationText.Text = "Use an http:// or https:// endpoint with a host.";
        SettingsValidationText.Visibility = Visibility.Visible;
        return false;
    }

    private static void BrowseForFile(TextBox target, string title, string filter)
    {
        using var dialog = new Forms.OpenFileDialog
        {
            Title = title,
            Filter = filter,
            CheckFileExists = true
        };
        if (dialog.ShowDialog() == Forms.DialogResult.OK)
        {
            target.Text = dialog.FileName;
        }
    }

    private void ShowFromTray()
    {
        DispatcherQueue.TryEnqueue(() =>
        {
            ShowWindow(_windowHandle, ShowWindowNormal);
            Activate();
            SetForegroundWindow(_windowHandle);
        });
    }

    private void HideToTray()
    {
        ShowWindow(_windowHandle, ShowWindowHidden);
    }

    private void QuitFromTray()
    {
        DispatcherQueue.TryEnqueue(() =>
        {
            _allowClose = true;
            Close();
        });
    }

    private async Task ConnectOrDisconnectAsync()
    {
        if (_model.Store.State.Status.Running)
        {
            await _model.DisconnectAsync();
        }
        else
        {
            await _model.ConnectAsync();
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

    private static string AppIconPath()
    {
        return Path.Combine(AppContext.BaseDirectory, "Assets", "clambhook.ico");
    }

    [DllImport("user32.dll")]
    private static extern bool ShowWindow(nint hWnd, int nCmdShow);

    [DllImport("user32.dll")]
    private static extern bool SetForegroundWindow(nint hWnd);
}
