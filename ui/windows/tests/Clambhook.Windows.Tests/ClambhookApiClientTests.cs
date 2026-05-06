using System.Net;
using System.Text;
using Clambhook.Windows.Core;

namespace Clambhook.Windows.Tests;

public sealed class ClambhookApiClientTests
{
    [Fact]
    public async Task StatusSendsBearerTokenAndDecodesResponse()
    {
        var handler = new CapturingHandler("""{"running":true,"profile":"A","listeners":[]}""");
        var client = new ClambhookApiClient(new Uri("http://127.0.0.1:9090/"), () => "secret-token", new HttpClient(handler));

        var status = await client.GetStatusAsync();

        Assert.Equal("/api/v1/status", handler.Requests.Single().RequestUri!.AbsolutePath);
        Assert.Equal("Bearer", handler.Requests.Single().Headers.Authorization!.Scheme);
        Assert.Equal("secret-token", handler.Requests.Single().Headers.Authorization!.Parameter);
        Assert.True(status.Running);
        Assert.Equal("A", status.Profile);
    }

    [Fact]
    public async Task SetActiveProfileSendsPutBody()
    {
        var handler = new CapturingHandler("", HttpStatusCode.NoContent);
        var client = new ClambhookApiClient(new Uri("http://localhost:9090"), () => "", new HttpClient(handler));

        await client.SetActiveProfileAsync("B");

        var request = handler.Requests.Single();
        Assert.Equal(HttpMethod.Put, request.Method);
        Assert.Equal("/api/v1/profiles/active", request.RequestUri!.AbsolutePath);
        Assert.Equal("application/json", request.Content!.Headers.ContentType!.MediaType);
        Assert.Equal("""{"name":"B"}""", await request.Content.ReadAsStringAsync());
    }

    [Fact]
    public async Task HttpErrorsPreserveStatusAndBody()
    {
        var handler = new CapturingHandler("unauthorized\n", HttpStatusCode.Unauthorized);
        var client = new ClambhookApiClient(new Uri("http://localhost:9090"), () => null, new HttpClient(handler));

        var error = await Assert.ThrowsAsync<ApiHttpException>(() => client.GetStatusAsync());

        Assert.Equal(401, error.StatusCode);
        Assert.Equal("unauthorized", error.Body);
    }

    [Fact]
    public void EventsUriConvertsSchemeAndKeepsBearerHeaderValue()
    {
        var httpClient = new ClambhookApiClient(new Uri("http://127.0.0.1:9090/"), () => "secret-token");
        var httpsClient = new ClambhookApiClient(new Uri("https://proxy.example.test"), () => "");

        Assert.Equal("ws://127.0.0.1:9090/api/v1/events?types=connection.*,log.*", httpClient.EventsUri.ToString());
        Assert.Equal("wss://proxy.example.test/api/v1/events?types=connection.*,log.*", httpsClient.EventsUri.ToString());
        Assert.Equal("secret-token", httpClient.BearerTokenForWebSocket);
        Assert.Null(httpsClient.BearerTokenForWebSocket);
    }
}

internal sealed class CapturingHandler : HttpMessageHandler
{
    private readonly string _body;
    private readonly HttpStatusCode _statusCode;

    public CapturingHandler(string body, HttpStatusCode statusCode = HttpStatusCode.OK)
    {
        _body = body;
        _statusCode = statusCode;
    }

    public List<HttpRequestMessage> Requests { get; } = [];

    protected override Task<HttpResponseMessage> SendAsync(HttpRequestMessage request, CancellationToken cancellationToken)
    {
        Requests.Add(request);
        return Task.FromResult(new HttpResponseMessage(_statusCode)
        {
            Content = new StringContent(_body, Encoding.UTF8, "application/json")
        });
    }
}
