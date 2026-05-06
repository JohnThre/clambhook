using System.Security.Cryptography;
using System.Text;

namespace Clambhook.Windows.Core;

public interface ITokenVault
{
    Task<string> ReadTokenAsync(CancellationToken cancellationToken = default);
    Task SaveTokenAsync(string token, CancellationToken cancellationToken = default);
}

public sealed class DpapiTokenVault : ITokenVault
{
    private readonly string _path;

    public DpapiTokenVault(string? path = null)
    {
        _path = path ?? DefaultPath();
    }

    public async Task<string> ReadTokenAsync(CancellationToken cancellationToken = default)
    {
        if (!File.Exists(_path))
        {
            return "";
        }

        var protectedText = await File.ReadAllTextAsync(_path, cancellationToken);
        if (string.IsNullOrWhiteSpace(protectedText))
        {
            return "";
        }

        var bytes = Convert.FromBase64String(protectedText);
        var unprotected = ProtectedData.Unprotect(bytes, null, DataProtectionScope.CurrentUser);
        return Encoding.UTF8.GetString(unprotected);
    }

    public async Task SaveTokenAsync(string token, CancellationToken cancellationToken = default)
    {
        Directory.CreateDirectory(Path.GetDirectoryName(_path)!);
        var protectedBytes = ProtectedData.Protect(Encoding.UTF8.GetBytes(token.Trim()), null, DataProtectionScope.CurrentUser);
        await File.WriteAllTextAsync(_path, Convert.ToBase64String(protectedBytes), cancellationToken);
    }

    private static string DefaultPath()
    {
        var root = Path.Combine(
            Environment.GetFolderPath(Environment.SpecialFolder.ApplicationData),
            "clambhook");
        return Path.Combine(root, "windows-token.dat");
    }
}

public sealed class InMemoryTokenVault : ITokenVault
{
    private string _token = "";

    public Task<string> ReadTokenAsync(CancellationToken cancellationToken = default)
    {
        return Task.FromResult(_token);
    }

    public Task SaveTokenAsync(string token, CancellationToken cancellationToken = default)
    {
        _token = token.Trim();
        return Task.CompletedTask;
    }
}
