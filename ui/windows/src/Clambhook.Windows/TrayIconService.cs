using Clambhook.Windows.Core;
using Drawing = System.Drawing;
using Forms = System.Windows.Forms;

namespace Clambhook.Windows;

public sealed class TrayIconService : IDisposable
{
    private readonly Forms.NotifyIcon _notifyIcon;
    private readonly Forms.ToolStripMenuItem _connectItem;
    private readonly Forms.ToolStripMenuItem _daemonItem;

    public TrayIconService(
        Action showWindow,
        Func<Task> connectOrDisconnect,
        Func<Task> startOrStopDaemon,
        Func<Task> refresh,
        Action quit)
    {
        _connectItem = new Forms.ToolStripMenuItem("Connect", null, async (_, _) => await connectOrDisconnect());
        _daemonItem = new Forms.ToolStripMenuItem("Start daemon", null, async (_, _) => await startOrStopDaemon());
        var menu = new Forms.ContextMenuStrip();
        menu.Items.Add(new Forms.ToolStripMenuItem("Show clambhook", null, (_, _) => showWindow()));
        menu.Items.Add(_connectItem);
        menu.Items.Add(_daemonItem);
        menu.Items.Add(new Forms.ToolStripMenuItem("Refresh", null, async (_, _) => await refresh()));
        menu.Items.Add(new Forms.ToolStripSeparator());
        menu.Items.Add(new Forms.ToolStripMenuItem("Quit", null, (_, _) => quit()));

        _notifyIcon = new Forms.NotifyIcon
        {
            Icon = Drawing.SystemIcons.Application,
            Text = "clambhook",
            ContextMenuStrip = menu,
            Visible = true
        };
        _notifyIcon.DoubleClick += (_, _) => showWindow();
    }

    public void Update(DashboardState state, bool daemonRunning)
    {
        _connectItem.Text = state.Status.Running ? "Disconnect" : "Connect";
        _daemonItem.Text = daemonRunning ? "Stop daemon" : "Start daemon";
        _notifyIcon.Text = state.ApiOnline
            ? $"clambhook · {(state.Status.Running ? "running" : "stopped")}"
            : "clambhook · API offline";
    }

    public void Dispose()
    {
        _notifyIcon.Visible = false;
        _notifyIcon.Dispose();
    }
}
