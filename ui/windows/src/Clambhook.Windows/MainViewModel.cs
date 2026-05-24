using Clambhook.Windows.Core;

namespace Clambhook.Windows;

public sealed class MainViewModel
{
    private readonly ISettingsStore _settingsStore;
    private readonly ITokenVault _tokenVault;
    private CancellationTokenSource? _pollingCts;
    private CancellationTokenSource? _eventsCts;
    private string _appBaseDirectory = AppContext.BaseDirectory;
    private ClambhookApiClient _apiClient;
    private int _busyDepth;

    public MainViewModel(ISettingsStore settingsStore, ITokenVault tokenVault, DaemonSupervisor daemon)
    {
        _settingsStore = settingsStore;
        _tokenVault = tokenVault;
        Daemon = daemon;
        Settings = new AppSettings();
        _apiClient = NewClient();
        Store = new DashboardStore(_apiClient);
        Store.PropertyChanged += Store_PropertyChanged;
        Daemon.StateChanged += NotifyChanged;
    }

    public event Action? StateChanged;

    public AppSettings Settings { get; private set; }
    public string ApiToken { get; private set; } = "";
    public DashboardStore Store { get; private set; }
    public DaemonSupervisor Daemon { get; }
    public string DaemonMessage { get; private set; } = "";
    public bool IsBusy => _busyDepth > 0;
    public string BusyMessage { get; private set; } = "";

    public async Task InitializeAsync(string appBaseDirectory)
    {
        _appBaseDirectory = appBaseDirectory;
        Settings = await _settingsStore.LoadAsync();
        ApiToken = await _tokenVault.ReadTokenAsync();
        ReloadClient();
        if (Settings.LaunchDaemonOnStart)
        {
            await StartDaemonAsync();
        }

        StartBackgroundWork();
        await Store.RefreshDashboardAsync();
        NotifyChanged();
    }

    public async Task SaveSettingsAsync(AppSettings settings, string token)
    {
        await RunUserActionAsync("Saving settings", async () =>
        {
            Settings = settings.Normalized();
            ApiToken = token.Trim();
            await _settingsStore.SaveAsync(Settings);
            await _tokenVault.SaveTokenAsync(ApiToken);
            ReloadClient();
            StartBackgroundWork();
            await Store.RefreshDashboardAsync();
        });
    }

    public async Task RefreshDashboardAsync()
    {
        await RunUserActionAsync("Refreshing", () => Store.RefreshDashboardAsync());
    }

    public async Task ConnectAsync()
    {
        await RunUserActionAsync("Connecting", () => Store.ConnectAsync());
    }

    public async Task DisconnectAsync()
    {
        await RunUserActionAsync("Disconnecting", () => Store.DisconnectAsync());
    }

    public async Task SetActiveProfileAsync(string profile)
    {
        await RunUserActionAsync("Switching profile", () => Store.SetActiveProfileAsync(profile));
    }

    public async Task StartDaemonAsync()
    {
        await RunUserActionAsync("Starting daemon", async () =>
        {
            await Daemon.StartAsync(Settings, ApiToken, _appBaseDirectory);
            DaemonMessage = Daemon.Message;
        });
    }

    public async Task StopDaemonAsync()
    {
        await RunUserActionAsync("Stopping daemon", async () =>
        {
            await Daemon.StopAsync();
            DaemonMessage = Daemon.Message;
        });
    }

    public async Task ShutdownAsync()
    {
        _pollingCts?.Cancel();
        _eventsCts?.Cancel();
        if (Settings.StopDaemonOnExit)
        {
            await Daemon.StopAsync();
        }
    }

    private void ReloadClient()
    {
        _apiClient = NewClient();
        Store.PropertyChanged -= Store_PropertyChanged;
        Store = new DashboardStore(_apiClient);
        Store.PropertyChanged += Store_PropertyChanged;
    }

    private ClambhookApiClient NewClient()
    {
        return new ClambhookApiClient(new Uri(Settings.Normalized().ApiEndpoint), () => ApiToken);
    }

    private void StartBackgroundWork()
    {
        _pollingCts?.Cancel();
        _eventsCts?.Cancel();
        _pollingCts = new CancellationTokenSource();
        _eventsCts = new CancellationTokenSource();
        _ = RunPollingAsync(_pollingCts.Token);
        if (Settings.EventStreamEnabled)
        {
            _ = _apiClient.StreamEventsAsync(
                daemonEvent =>
                {
                    Store.ApplyEvent(daemonEvent);
                    NotifyChanged();
                    return Task.CompletedTask;
                },
                error =>
                {
                    DaemonMessage = $"events: {error.Message}";
                    NotifyChanged();
                    return Task.CompletedTask;
                },
                _eventsCts.Token);
        }
    }

    private async Task RunPollingAsync(CancellationToken cancellationToken)
    {
        try
        {
            while (!cancellationToken.IsCancellationRequested)
            {
                await Store.RefreshStatusAsync(cancellationToken);
                NotifyChanged();
                await Task.Delay(TimeSpan.FromSeconds(Settings.Normalized().RefreshIntervalSeconds), cancellationToken);
            }
        }
        catch (OperationCanceledException) when (cancellationToken.IsCancellationRequested)
        {
        }
    }

    private async Task RunUserActionAsync(string busyMessage, Func<Task> action)
    {
        _busyDepth++;
        BusyMessage = busyMessage;
        NotifyChanged();
        try
        {
            await action();
        }
        catch (Exception error)
        {
            DaemonMessage = error.Message;
        }
        finally
        {
            _busyDepth = Math.Max(0, _busyDepth - 1);
            if (_busyDepth == 0)
            {
                BusyMessage = "";
            }
            NotifyChanged();
        }
    }

    private void Store_PropertyChanged(object? sender, System.ComponentModel.PropertyChangedEventArgs e)
    {
        NotifyChanged();
    }

    private void NotifyChanged()
    {
        StateChanged?.Invoke();
    }
}
