using Clambhook.Windows.Core;
using Drawing = System.Drawing;
using Forms = System.Windows.Forms;

namespace Clambhook.Windows;

public sealed class TrayIconService : IDisposable
{
    private readonly Forms.NotifyIcon _notifyIcon;
    private readonly Forms.ToolStripMenuItem _statusItem;
    private readonly Forms.ToolStripMenuItem _connectItem;
    private readonly Forms.ToolStripMenuItem _daemonItem;

    public TrayIconService(
        string iconPath,
        Action showWindow,
        Func<Task> connectOrDisconnect,
        Func<Task> startOrStopDaemon,
        Func<Task> refresh,
        Action quit)
    {
        _statusItem = new Forms.ToolStripMenuItem("clambhook");
        _statusItem.Enabled = false;
        _connectItem = new Forms.ToolStripMenuItem("Connect", null, async (_, _) => await connectOrDisconnect());
        _daemonItem = new Forms.ToolStripMenuItem("Start daemon", null, async (_, _) => await startOrStopDaemon());
        var menu = new Forms.ContextMenuStrip();
        menu.Items.Add(_statusItem);
        menu.Items.Add(new Forms.ToolStripSeparator());
        menu.Items.Add(new Forms.ToolStripMenuItem("Show clambhook", null, (_, _) => showWindow()));
        menu.Items.Add(_connectItem);
        menu.Items.Add(_daemonItem);
        menu.Items.Add(new Forms.ToolStripMenuItem("Refresh", null, async (_, _) => await refresh()));
        menu.Items.Add(new Forms.ToolStripSeparator());
        menu.Items.Add(new Forms.ToolStripMenuItem("Quit", null, (_, _) => quit()));

        _notifyIcon = new Forms.NotifyIcon
        {
            Icon = LoadIcon(iconPath),
            Text = "clambhook",
            ContextMenuStrip = menu,
            Visible = true
        };
        _notifyIcon.DoubleClick += (_, _) => showWindow();
    }

    public void Update(DashboardState state, DaemonSupervisor daemon)
    {
        _statusItem.Text = state.ApiOnline
            ? $"{(state.Status.Running ? "Running" : "Stopped")} / {state.ActiveProfile}"
            : "API offline";
        _connectItem.Text = state.Status.Running ? "Disconnect" : "Connect";
        _connectItem.Enabled = state.ApiOnline;
        _daemonItem.Text = daemon.IsRunning ? "Stop daemon" : "Start daemon";
        _daemonItem.Enabled = !daemon.IsBusy;
        _notifyIcon.Text = TrimNotifyText(state.ApiOnline
            ? $"clambhook / {(state.Status.Running ? "running" : "stopped")} / {state.ActiveProfile}"
            : "clambhook / API offline");
    }

    public void Dispose()
    {
        _notifyIcon.Visible = false;
        _notifyIcon.Dispose();
    }

    private static Drawing.Icon LoadIcon(string iconPath)
    {
        if (File.Exists(iconPath))
        {
            try
            {
                return new Drawing.Icon(iconPath);
            }
            catch
            {
            }
        }

        return Drawing.SystemIcons.Application;
    }

    private static string TrimNotifyText(string text)
    {
        return text.Length <= 63 ? text : text[..63];
    }
}
