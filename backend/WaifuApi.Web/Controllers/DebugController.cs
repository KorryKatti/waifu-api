using Microsoft.EntityFrameworkCore;
using WaifuApi.Infrastructure.Persistence;

namespace WaifuApi.Web.Controllers;

/// <summary>
/// TEMPORARY diagnostic endpoint. Reports whether the Supabase connection string
/// is present and well-formed without ever revealing the password.
/// DELETE THIS FILE before going anywhere near real users.
/// </summary>
[ApiController]
[Route("debug")]
[Produces("application/json")]
[Tags["Debug"]]
public class DebugController : ControllerBase
{
    private readonly IConfiguration _configuration;
    private readonly IWaifuDbContext _context;

    public DebugController(IConfiguration configuration, IWaifuDbContext context)
    {
        _configuration = configuration;
        _context = context;
    }

    [HttpGet("conn")]
    public async Task<IActionResult> GetConnection()
    {
        var raw = _configuration.GetConnectionString("DefaultConnection");

        if (string.IsNullOrEmpty(raw))
        {
            return Ok(new
            {
                present = false,
                note = "DefaultConnection is null. Check the env var name is exactly " +
                       "ConnectionStrings__DefaultConnection"
            });
        }

        // Describe the value without exposing the secret.
        var firstChar = raw.Length > 0 ? raw[0] : '\0';
        var report = new Dictionary<string, object?>
        {
            ["present"] = true,
            ["length"] = raw.Length,
            ["firstCharCode"] = (int)firstChar,
            ["startsWithPostgres"] = raw.StartsWith("postgresql://", StringComparison.Ordinal),
            ["startsWithHttp"] = raw.StartsWith("http", StringComparison.OrdinalIgnoreCase),
            ["hasLeadingSpace"] = char.IsWhiteSpace(firstChar),
            ["hasTrailingSpace"] = char.IsWhiteSpace(raw[^1]),
            ["semicolonCount"] = raw.Count(c => c == ';'),
            ["atCount"] = raw.Count(c => c == '@'),
            ["keyCount"] = _configuration.AsEnumerable()
                .Count(kv => kv.Key.Contains("DefaultConnection", StringComparison.OrdinalIgnoreCase)),
            ["matchingKeys"] = _configuration.AsEnumerable()
                .Where(kv => kv.Key.Contains("Connection", StringComparison.OrdinalIgnoreCase))
                .Select(kv => kv.Key).ToArray(),
        };

        // Try an actual round-trip.
        try
        {
            var count = await _context.Images.CountAsync();
            report["dbReachable"] = true;
            report["imageCount"] = count;
        }
        catch (Exception ex)
        {
            report["dbReachable"] = false;
            report["errorType"] = ex.GetType().Name;
            report["errorMessage"] = ex.Message.Length > 400
                ? ex.Message[..400]
                : ex.Message;
        }

        return Ok(report);
    }
}