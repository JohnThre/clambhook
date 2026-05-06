using System.Text.Json;
using Clambhook.Windows.Core;

namespace Clambhook.Windows.Tests;

public sealed class DashboardStoreTests
{
    [Fact]
    public async Task RefreshDashboardLoadsStatusProfilesAndServers()
    {
        var api = new FakeApi
        {
            Status = new StatusPayload(true, "A", [new ListenerStatusPayload("socks5", "127.0.0.1:1080", 3)]),
            Profiles = new ProfilesPayload(["A", "B"], "A"),
            Servers = new ServersPayload("A", [new ChainPayload("default", [new ServerPayload("london", "uk.example:443", "vless")])])
        };
        var store = new DashboardStore(api);

        await store.RefreshDashboardAsync();

        Assert.True(store.State.ApiOnline);
        Assert.True(store.State.Status.Running);
        Assert.Equal("A", store.State.ActiveProfile);
        Assert.Equal(3, store.State.ActiveConnections);
        Assert.Equal("london", store.State.Servers.Chains.Single().Servers.Single().Name);
    }

    [Fact]
    public async Task RefreshDashboardStoresOfflineErrorOnFailure()
    {
        var store = new DashboardStore(new FakeApi { Error = new InvalidOperationException("boom") });

        await store.RefreshDashboardAsync();

        Assert.False(store.State.ApiOnline);
        Assert.Equal("boom", store.State.ErrorText);
    }

    [Fact]
    public async Task ActionsRefreshDashboardAfterSuccess()
    {
        var api = new FakeApi();
        var store = new DashboardStore(api);

        await store.ConnectAsync();
        await store.DisconnectAsync();
        await store.SetActiveProfileAsync("B");

        Assert.Equal(new[] { "connect", "disconnect", "profile:B" }, api.Actions);
        Assert.Equal(3, api.StatusCalls);
        Assert.Equal(3, api.ProfileCalls);
        Assert.Equal(3, api.ServerCalls);
    }

    [Fact]
    public void AppliesBandwidthAndLogEventsWithCaps()
    {
        var store = new DashboardStore(new FakeApi());

        store.ApplyEvent(new DaemonEvent(
            1,
            1,
            1,
            "connection.bytes",
            new Dictionary<string, JsonElement>
            {
                ["rx_delta"] = JsonSerializer.SerializeToElement(2048),
                ["tx_delta"] = JsonSerializer.SerializeToElement(1024),
                ["interval_ns"] = JsonSerializer.SerializeToElement(1_000_000_000)
            }));

        for (var i = 0; i < DashboardStore.MaxLogLines + 5; i++)
        {
            store.ApplyEvent(new DaemonEvent(
                0,
                (ulong)i,
                i,
                "log.line",
                new Dictionary<string, JsonElement> { ["line"] = JsonSerializer.SerializeToElement($"line-{i}") }));
        }

        Assert.Equal(2048, store.State.CurrentBandwidth.RxBps);
        Assert.Equal(1024, store.State.CurrentBandwidth.TxBps);
        Assert.Equal(DashboardStore.MaxLogLines, store.State.Logs.Count);
        Assert.Equal("line-5", store.State.Logs.First());
    }
}

internal sealed class FakeApi : IClambhookApi
{
    public StatusPayload Status { get; set; } = new();
    public ProfilesPayload Profiles { get; set; } = new(["A", "B"], "A");
    public ServersPayload Servers { get; set; } = new("A", []);
    public Exception? Error { get; set; }
    public List<string> Actions { get; } = [];
    public int StatusCalls { get; private set; }
    public int ProfileCalls { get; private set; }
    public int ServerCalls { get; private set; }

    public Task<StatusPayload> GetStatusAsync(CancellationToken cancellationToken = default)
    {
        StatusCalls++;
        if (Error is not null) throw Error;
        return Task.FromResult(Status);
    }

    public Task<ProfilesPayload> GetProfilesAsync(CancellationToken cancellationToken = default)
    {
        ProfileCalls++;
        if (Error is not null) throw Error;
        return Task.FromResult(Profiles);
    }

    public Task<ServersPayload> GetServersAsync(CancellationToken cancellationToken = default)
    {
        ServerCalls++;
        if (Error is not null) throw Error;
        return Task.FromResult(Servers);
    }

    public Task ConnectAsync(CancellationToken cancellationToken = default)
    {
        Actions.Add("connect");
        return Task.CompletedTask;
    }

    public Task DisconnectAsync(CancellationToken cancellationToken = default)
    {
        Actions.Add("disconnect");
        return Task.CompletedTask;
    }

    public Task SetActiveProfileAsync(string name, CancellationToken cancellationToken = default)
    {
        Actions.Add($"profile:{name}");
        return Task.CompletedTask;
    }
}
