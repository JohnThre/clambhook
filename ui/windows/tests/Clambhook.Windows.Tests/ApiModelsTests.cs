using System.Text.Json;
using Clambhook.Windows.Core;

namespace Clambhook.Windows.Tests;

public sealed class ApiModelsTests
{
    [Fact]
    public void DecodesStatusPayload()
    {
        var status = JsonSerializer.Deserialize<StatusPayload>(
            """
            {
              "running": true,
              "profile": "default",
              "listeners": [
                {"protocol": "socks5", "addr": "127.0.0.1:1080", "active_conns": 2}
              ]
            }
            """,
            ApiJson.Options);

        Assert.NotNull(status);
        Assert.True(status.Running);
        Assert.Equal("default", status.Profile);
        Assert.Equal("socks5", status.Listeners.Single().Protocol);
        Assert.Equal(2, status.Listeners.Single().ActiveConnections);
    }

    [Fact]
    public void DecodesServersPayload()
    {
        var servers = JsonSerializer.Deserialize<ServersPayload>(
            """
            {
              "profile": "default",
              "chains": [
                {
                  "name": "primary",
                  "servers": [
                    {
                      "name": "london",
                      "address": "uk.example:443",
                      "protocol": "vless",
                      "geo": {
                        "country": "United Kingdom",
                        "country_code": "GB",
                        "city": "London",
                        "latitude": 51.5072,
                        "longitude": -0.1276
                      }
                    }
                  ]
                }
              ]
            }
            """,
            ApiJson.Options);

        Assert.NotNull(servers);
        var server = servers.Chains.Single().Servers.Single();
        Assert.Equal("default", servers.Profile);
        Assert.Equal("primary", servers.Chains.Single().Name);
        Assert.Equal("london", server.Name);
        Assert.Equal("GB", server.Geo.CountryCode);
    }

    [Fact]
    public void DecodesDaemonEventPayload()
    {
        var daemonEvent = JsonSerializer.Deserialize<DaemonEvent>(
            """
            {
              "shard_id": 7,
              "lamport": 12,
              "ts_ns": 123456789,
              "type": "connection.bytes",
              "data": {
                "rx_delta": 2048,
                "tx_delta": 1024,
                "interval_ns": 1000000000
              }
            }
            """,
            ApiJson.Options);

        Assert.NotNull(daemonEvent);
        Assert.Equal(7UL, daemonEvent.ShardId);
        Assert.Equal(12UL, daemonEvent.Lamport);
        Assert.Equal("connection.bytes", daemonEvent.Type);
        Assert.Equal(2048, daemonEvent.Data["rx_delta"].GetDouble());
    }
}
