using System.Text.Json;
using System.Text.Json.Serialization;

namespace Clambhook.Windows.Core;

public sealed class StatusPayload
{
    public StatusPayload()
    {
    }

    public StatusPayload(bool running, string profile, IReadOnlyList<ListenerStatusPayload> listeners)
    {
        Running = running;
        Profile = profile;
        Listeners = listeners;
    }

    public bool Running { get; init; }
    public string Profile { get; init; } = "";
    public IReadOnlyList<ListenerStatusPayload> Listeners { get; init; } = [];
}

public sealed class ListenerStatusPayload
{
    public ListenerStatusPayload()
    {
    }

    public ListenerStatusPayload(string protocol, string addr, int activeConnections)
    {
        Protocol = protocol;
        Addr = addr;
        ActiveConnections = activeConnections;
    }

    public string Protocol { get; init; } = "";
    public string Addr { get; init; } = "";

    [JsonPropertyName("active_conns")]
    public int ActiveConnections { get; init; }
}

public sealed class ProfilesPayload
{
    public ProfilesPayload()
    {
    }

    public ProfilesPayload(IReadOnlyList<string> profiles, string active)
    {
        Profiles = profiles;
        Active = active;
    }

    public IReadOnlyList<string> Profiles { get; init; } = [];
    public string Active { get; init; } = "";
}

public sealed class ServersPayload
{
    public ServersPayload()
    {
    }

    public ServersPayload(string profile, IReadOnlyList<ChainPayload> chains)
    {
        Profile = profile;
        Chains = chains;
    }

    public string Profile { get; init; } = "";
    public IReadOnlyList<ChainPayload> Chains { get; init; } = [];
}

public sealed class ChainPayload
{
    public ChainPayload()
    {
    }

    public ChainPayload(string name, IReadOnlyList<ServerPayload> servers)
    {
        Name = name;
        Servers = servers;
    }

    public string Name { get; init; } = "";
    public IReadOnlyList<ServerPayload> Servers { get; init; } = [];
}

public sealed class ServerPayload
{
    public ServerPayload()
    {
    }

    public ServerPayload(string name, string address, string protocol)
    {
        Name = name;
        Address = address;
        Protocol = protocol;
    }

    public string Name { get; init; } = "";
    public string Address { get; init; } = "";
    public string Protocol { get; init; } = "";
    public LocationPayload Geo { get; init; } = new();

    [JsonPropertyName("geo_error")]
    public string? GeoError { get; init; }
}

public sealed class LocationPayload
{
    public string Country { get; init; } = "";

    [JsonPropertyName("country_code")]
    public string CountryCode { get; init; } = "";

    public string City { get; init; } = "";
    public double Latitude { get; init; }
    public double Longitude { get; init; }
}

public sealed class DaemonEvent
{
    public DaemonEvent()
    {
    }

    public DaemonEvent(ulong shardId, ulong lamport, long tsNs, string type, IReadOnlyDictionary<string, JsonElement> data)
    {
        ShardId = shardId;
        Lamport = lamport;
        TsNs = tsNs;
        Type = type;
        Data = data;
    }

    [JsonPropertyName("shard_id")]
    public ulong ShardId { get; init; }

    public ulong Lamport { get; init; }

    [JsonPropertyName("ts_ns")]
    public long TsNs { get; init; }

    public string Type { get; init; } = "";
    public IReadOnlyDictionary<string, JsonElement> Data { get; init; } = new Dictionary<string, JsonElement>();
}

public sealed record BandwidthSample(double RxBps = 0, double TxBps = 0);

public static class JsonElementExtensions
{
    public static double? DoubleValueOrNull(this JsonElement element)
    {
        return element.ValueKind switch
        {
            JsonValueKind.Number when element.TryGetDouble(out var value) => value,
            JsonValueKind.String when double.TryParse(element.GetString(), out var value) => value,
            JsonValueKind.True => 1,
            JsonValueKind.False => 0,
            _ => null
        };
    }

    public static string? StringValueOrNull(this JsonElement element)
    {
        return element.ValueKind == JsonValueKind.String ? element.GetString() : null;
    }
}
