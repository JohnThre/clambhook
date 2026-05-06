using System.Net;
using System.Net.Http.Headers;
using System.Net.WebSockets;
using System.Text;
using System.Text.Json;

namespace Clambhook.Windows.Core;

public interface IClambhookApi
{
    Task<StatusPayload> GetStatusAsync(CancellationToken cancellationToken = default);
    Task<ProfilesPayload> GetProfilesAsync(CancellationToken cancellationToken = default);
    Task<ServersPayload> GetServersAsync(CancellationToken cancellationToken = default);
    Task ConnectAsync(CancellationToken cancellationToken = default);
    Task DisconnectAsync(CancellationToken cancellationToken = default);
    Task SetActiveProfileAsync(string name, CancellationToken cancellationToken = default);
}

public sealed class ApiHttpException : HttpRequestException
{
    public ApiHttpException(int statusCode, string body)
        : base(string.IsNullOrWhiteSpace(body) ? statusCode.ToString() : $"{statusCode}: {body}")
    {
        StatusCode = statusCode;
        Body = body;
    }

    public int StatusCode { get; }
    public string Body { get; }
}

public sealed class ClambhookApiClient : IClambhookApi
{
    private readonly Uri _baseUri;
    private readonly Func<string?> _tokenProvider;
    private readonly HttpClient _httpClient;

    public ClambhookApiClient(Uri baseUri, Func<string?>? tokenProvider = null, HttpClient? httpClient = null)
    {
        _baseUri = NormalizeBaseUri(baseUri);
        _tokenProvider = tokenProvider ?? (() => null);
        _httpClient = httpClient ?? new HttpClient();
    }

    public Uri EventsUri
    {
        get
        {
            var builder = new UriBuilder(_baseUri)
            {
                Scheme = _baseUri.Scheme == Uri.UriSchemeHttps ? "wss" : "ws",
                Path = "/api/v1/events",
                Query = "types=connection.*,log.*"
            };
            return builder.Uri;
        }
    }

    public string? BearerTokenForWebSocket
    {
        get
        {
            var token = _tokenProvider()?.Trim();
            return string.IsNullOrEmpty(token) ? null : token;
        }
    }

    public Task<StatusPayload> GetStatusAsync(CancellationToken cancellationToken = default) =>
        GetJsonAsync<StatusPayload>("/api/v1/status", cancellationToken);

    public Task<ProfilesPayload> GetProfilesAsync(CancellationToken cancellationToken = default) =>
        GetJsonAsync<ProfilesPayload>("/api/v1/profiles", cancellationToken);

    public Task<ServersPayload> GetServersAsync(CancellationToken cancellationToken = default) =>
        GetJsonAsync<ServersPayload>("/api/v1/servers", cancellationToken);

    public async Task ConnectAsync(CancellationToken cancellationToken = default)
    {
        await SendAsync(HttpMethod.Post, "/api/v1/connect", null, cancellationToken);
    }

    public async Task DisconnectAsync(CancellationToken cancellationToken = default)
    {
        await SendAsync(HttpMethod.Post, "/api/v1/disconnect", null, cancellationToken);
    }

    public async Task SetActiveProfileAsync(string name, CancellationToken cancellationToken = default)
    {
        var body = JsonSerializer.Serialize(new Dictionary<string, string> { ["name"] = name }, ApiJson.Options);
        await SendAsync(HttpMethod.Put, "/api/v1/profiles/active", body, cancellationToken);
    }

    public async Task StreamEventsAsync(Func<DaemonEvent, Task> onEvent, Func<Exception, Task> onError, CancellationToken cancellationToken)
    {
        using var socket = new ClientWebSocket();
        if (BearerTokenForWebSocket is { } token)
        {
            socket.Options.SetRequestHeader("Authorization", $"Bearer {token}");
        }

        try
        {
            await socket.ConnectAsync(EventsUri, cancellationToken);
            var buffer = new byte[64 * 1024];
            while (!cancellationToken.IsCancellationRequested && socket.State == WebSocketState.Open)
            {
                var segment = new ArraySegment<byte>(buffer);
                using var message = new MemoryStream();
                WebSocketReceiveResult result;
                do
                {
                    result = await socket.ReceiveAsync(segment, cancellationToken);
                    if (result.MessageType == WebSocketMessageType.Close)
                    {
                        return;
                    }
                    message.Write(buffer, 0, result.Count);
                } while (!result.EndOfMessage);

                var json = Encoding.UTF8.GetString(message.ToArray());
                var daemonEvent = JsonSerializer.Deserialize<DaemonEvent>(json, ApiJson.Options);
                if (daemonEvent is not null)
                {
                    await onEvent(daemonEvent);
                }
            }
        }
        catch (OperationCanceledException) when (cancellationToken.IsCancellationRequested)
        {
        }
        catch (Exception error)
        {
            await onError(error);
        }
    }

    private async Task<T> GetJsonAsync<T>(string path, CancellationToken cancellationToken)
    {
        var body = await SendAsync(HttpMethod.Get, path, null, cancellationToken);
        return JsonSerializer.Deserialize<T>(body, ApiJson.Options)
            ?? throw new JsonException($"empty response for {path}");
    }

    private async Task<string> SendAsync(HttpMethod method, string path, string? body, CancellationToken cancellationToken)
    {
        var request = new HttpRequestMessage(method, new Uri(_baseUri, path));
        var token = _tokenProvider()?.Trim();
        if (!string.IsNullOrEmpty(token))
        {
            request.Headers.Authorization = new AuthenticationHeaderValue("Bearer", token);
        }

        if (body is not null)
        {
            request.Content = new StringContent(body, Encoding.UTF8, "application/json");
        }

        using var response = await _httpClient.SendAsync(request, cancellationToken);
        var responseBody = await response.Content.ReadAsStringAsync(cancellationToken);
        if (response.StatusCode is < HttpStatusCode.OK or >= HttpStatusCode.MultipleChoices)
        {
            var trimmed = responseBody.Trim();
            throw new ApiHttpException((int)response.StatusCode, trimmed[..Math.Min(trimmed.Length, 1024)]);
        }

        return responseBody;
    }

    private static Uri NormalizeBaseUri(Uri uri)
    {
        var text = uri.ToString().Trim().TrimEnd('/') + "/";
        return new Uri(text);
    }
}
