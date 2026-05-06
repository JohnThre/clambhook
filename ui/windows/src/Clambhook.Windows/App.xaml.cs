using Clambhook.Windows.Core;
using Microsoft.UI.Xaml;

namespace Clambhook.Windows;

public partial class App : Application
{
    private MainWindow? _window;

    public App()
    {
        InitializeComponent();
    }

    protected override void OnLaunched(LaunchActivatedEventArgs args)
    {
        _window = new MainWindow(
            new MainViewModel(
                new FileSettingsStore(),
                new DpapiTokenVault(),
                new DaemonSupervisor()));
        _window.Activate();
    }
}
