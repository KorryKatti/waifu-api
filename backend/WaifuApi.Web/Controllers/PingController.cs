using Microsoft.AspNetCore.Http;
using Microsoft.AspNetCore.Mvc;

namespace WaifuApi.Web.Controllers;

/// <summary>
/// Liveness check for the API.
/// </summary>
/// <remarks>
/// Cheap, unauthenticated endpoint that touches no database, so it stays available
/// even while the rest of the API is degraded. Intended for uptime monitors and
/// for clients that just want to confirm the instance is reachable.
/// </remarks>
[ApiController]
[Route("ping")]
[Produces("text/plain")]
[Tags("Ping")]
public class PingController : ControllerBase
{
    /// <summary>
    /// Check that the API is up.
    /// </summary>
    /// <remarks>
    /// Returns the plain text body `pong` as soon as the request reaches the API.
    /// No authentication required.
    /// </remarks>
    /// <returns>The plain text body `pong`.</returns>
    /// <response code="200">Returns `pong`.</response>
    [HttpGet]
    [ProducesResponseType(StatusCodes.Status200OK)]
    public ContentResult GetPing()
    {
        return Content("pong", "text/plain");
    }
}