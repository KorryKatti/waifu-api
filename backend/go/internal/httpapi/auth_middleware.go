package httpapi

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"waifuapi/internal/auth"
	"waifuapi/internal/config"
)

// principal returns the authenticated caller for this request, or nil when the
// request is anonymous.
//
// The value is placed on the request context by the authentication middleware.
// Handlers read it through this method so they never touch the context directly.
func (s *Server) principal(r *http.Request) *principal {
	value, _ := r.Context().Value(principalKey{}).(*principal)
	return value
}

// principalKey is the context key type for the authenticated caller.
type principalKey struct{}

// logError records a handler failure.
//
// Every internal error is logged with the request path so failures can be traced
// back to a route, but the response body stays generic.
func (s *Server) logError(r *http.Request, operation string, err error) {
	if s.log == nil {
		return
	}
	s.log.Error("request failed",
		"operation", operation,
		"method", r.Method,
		"path", r.URL.Path,
		"error", err.Error(),
	)
}

// withAuthentication resolves credentials and stores the caller on the context.
//
// # An invalid credential is not a challenge
//
// A credential that cannot be resolved leaves the request anonymous. It does not
// produce 401 here, and that distinction is the whole contract.
//
// ASP.NET separates *authentication* (working out who you are) from
// *authorization* (deciding whether you may have this resource). A handler
// without [Authorize] never challenges: ApiKeyAuthenticationHandler returns
// AuthenticateResult.Fail("Invalid API Key") for an unknown key, UseAuthentication
// records the failure, the action runs as an anonymous caller, and the response
// is 200. Only a handler carrying [Authorize] calls HandleChallengeAsync, and
// that is what produces 401.
//
// Challenging in the authentication step instead inverts that: one caller with a
// revoked key 401s every endpoint they touch, including the ones C# serves them
// happily. That is not hypothetical — GET /images has no [Authorize], and a
// stranger's stale X-Api-Key was returning 401 for the public gallery.
//
// So the failure is logged and the request continues anonymous. requireAuth
// then produces the 401 on the routes that genuinely need a principal, which is
// where C# would have produced it too.
func withAuthentication(authenticator *auth.Authenticator, lookup auth.UserLookup, log Logger) middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			authorization := r.Header.Get("Authorization")
			apiKey := r.Header.Get("X-Api-Key")

			resolved, err := authenticator.Authenticate(r.Context(), authorization, apiKey, lookup)
			if err == nil {
				next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), principalKey{}, toPrincipalView(resolved))))
				return
			}

			if errorsIsNoCredentials(err) {
				// Anonymous is a valid state; public endpoints continue.
				next.ServeHTTP(w, r)
				return
			}

			if log != nil {
				log.Error("authentication failed", "path", r.URL.Path, "error", err.Error())
			}

			// A blacklisted account is refused outright rather than made
			// anonymous. C# does this in BlacklistMiddleware, which runs after
			// authorization and throws ForbiddenException regardless of which
			// endpoint was called, so it is a 403 on every route including the
			// public ones — and never a 401, which is what an authentication
			// failure means here.
			if errors.Is(err, auth.ErrForbidden) {
				writeBlacklisted(w, r)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// toPrincipalView narrows an auth.Principal to what handlers use.
func toPrincipalView(p *auth.Principal) *principal {
	if p == nil {
		return nil
	}
	return &principal{
		UserID:    p.UserID,
		Name:      p.Name,
		Role:      roleValue(p.Role),
		ViaAPIKey: p.ViaAPIKey,
	}
}

// ensure slog and time remain referenced.
var (
	_ = slog.Default
	_ = time.Now
	_ = config.RoleUser
)

// logConversionFailure records a WebP conversion failure.
//
// Conversion failures never fail a request: the original bytes are stored
// instead, matching ImageConverter.cs which catches, warns, and returns null.
// They still deserve a log line, because a silent run of them means uploads
// are being stored unconverted while the operator believes the feature is on.
//
// This takes a context rather than the request because the replacement path
// runs after the handler has returned and has no *http.Request to hand.
func (s *Server) logConversionFailure(ctx context.Context, err error) {
	if s.log == nil {
		return
	}
	s.log.Error("webp conversion failed; storing original bytes",
		"error", err.Error(),
	)
}
