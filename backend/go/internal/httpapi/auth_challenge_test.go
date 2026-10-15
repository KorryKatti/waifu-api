package httpapi_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"waifuapi/internal/auth"
	"waifuapi/internal/config"
)

// Authentication failure is not a challenge.
//
// ASP.NET separates authentication from authorization. ApiKeyAuthenticationHandler
// returns AuthenticateResult.Fail for an unknown key, UseAuthentication records
// the failure, and an action without [Authorize] still runs — as an anonymous
// caller, with a 200. Only a handler carrying [Authorize] calls
// HandleChallengeAsync, and that is what produces 401.
//
// These tests pin that separation. The bug they exist for was that the
// authentication middleware itself answered 401, which meant one caller with a
// revoked API key returned 401 for GET /images — a public route that the C#
// service serves without complaint. That is the shape a stale credential takes
// across a shared endpoint: everyone behind it sees an empty gallery.

// requestWithHeaders performs a call carrying arbitrary headers.
//
// h.request only knows how to set Authorization, and these tests are about the
// X-Api-Key header and about sending no credential at all.
func (h *harness) requestWithHeaders(method, path string, headers map[string]string) *http.Response {
	h.t.Helper()

	req, err := http.NewRequest(method, h.server.URL+path, nil)
	if err != nil {
		h.t.Fatalf("build request: %v", err)
	}
	for name, value := range headers {
		req.Header.Set(name, value)
	}

	resp, err := h.server.Client().Do(req)
	if err != nil {
		h.t.Fatalf("%s %s: %v", method, path, err)
	}
	return resp
}

// publicRoutes are endpoints the C# service leaves without [Authorize]. They
// must keep answering for a caller whose credential does not resolve.
var publicRoutes = []struct {
	method string
	path   string
}{
	{http.MethodGet, "/images"},
	{http.MethodGet, "/tags"},
	{http.MethodGet, "/artists"},
	{http.MethodGet, "/stats/public"},
	{http.MethodGet, "/ping"},
}

// TestUnresolvableCredentialDoesNotChallengePublicRoutes is the regression test
// for the production outage: a public route answered 401 because the caller
// presented an API key nobody recognised.
func TestUnresolvableCredentialDoesNotChallengePublicRoutes(t *testing.T) {
	h := newHarness(t)

	credentials := map[string]map[string]string{
		"unknown API key":  {"X-Api-Key": "waifu_this-key-was-never-issued"},
		"empty API key":    {"X-Api-Key": ""},
		"whitespace key":   {"X-Api-Key": "   "},
		"garbage bearer":   {"Authorization": "Bearer not.a.jwt"},
		"malformed bearer": {"Authorization": "Bearer "},
	}

	for name, headers := range credentials {
		t.Run(name, func(t *testing.T) {
			for _, route := range publicRoutes {
				resp := h.requestWithHeaders(route.method, route.path, headers)
				defer resp.Body.Close()

				if resp.StatusCode == http.StatusUnauthorized {
					t.Errorf("%s %s returned 401 for a credential that does not "+
						"resolve; the C# service has no [Authorize] here and serves it",
						route.method, route.path)
				}
			}
		})
	}
}

// TestUnresolvableCredentialStillChallengesProtectedRoutes is the other half.
//
// Deferring the 401 must not turn a refusal into an anonymous success on the
// routes that genuinely need a principal. requireAuth produces it instead, at
// the handler, which is where C# produces it too.
func TestUnresolvableCredentialStillChallengesProtectedRoutes(t *testing.T) {
	h := newHarness(t)

	protected := []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/users/me"},
		{http.MethodGet, "/stats/admin"},
		{http.MethodGet, "/reports"},
		{http.MethodGet, "/review/tasks"},
		{http.MethodGet, "/auth/api-keys"},
	}

	for _, route := range protected {
		t.Run(route.method+" "+route.path, func(t *testing.T) {
			resp := h.requestWithHeaders(route.method, route.path,
				map[string]string{"X-Api-Key": "waifu_this-key-was-never-issued"})
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusUnauthorized {
				t.Errorf("expected 401 from the handler guard, got %d", resp.StatusCode)
			}
		})
	}
}

// TestBlacklistedAccountIsForbiddenNotUnauthorized pins the one authentication
// failure that is still a refusal.
//
// C# runs BlacklistMiddleware after authorization and throws ForbiddenException
// regardless of which endpoint was called, so a blacklisted caller gets 403 even
// on a public route. It is never 401: the credential resolved, so this is an
// authorization decision, and treating it as anonymous would hand the account
// back the public read path it was revoked.
func TestBlacklistedAccountIsForbiddenNotUnauthorized(t *testing.T) {
	h := newHarness(t)

	ctx := context.Background()
	const userID = 4242
	const plaintext = "waifu_blacklisted-account-key"

	_, err := h.db.Pool().Exec(ctx, `
        INSERT INTO "Users" ("Id", "DiscordId", "Name", "Role",
                         "IsBlacklisted", "RequestCount", "JwtRequestCount", "ApiKeyRequestCount")
        VALUES ($1, $2, $3, $4, true, 0, 0, 0)`,
		userID, "discord-blacklisted", "blacklisted", int(config.RoleUser))
	if err != nil {
		t.Fatalf("seed blacklisted user: %v", err)
	}

	_, err = h.db.Pool().Exec(ctx, `
        INSERT INTO "ApiKeys" ("UserId", "Description", "KeyHash", "CreatedAt")
        VALUES ($1, $2, $3, now())`,
		userID, "test key", auth.HashAPIKey(plaintext))
	if err != nil {
		t.Fatalf("seed api key: %v", err)
	}

	headers := map[string]string{"X-Api-Key": plaintext}

	// A public route, because BlacklistMiddleware has no [Authorize] exemption.
	resp := h.requestWithHeaders(http.MethodGet, "/images", headers)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("GET /images as a blacklisted account: expected 403, got %d", resp.StatusCode)
	}

	// And a protected one, which must refuse rather than challenge.
	resp = h.requestWithHeaders(http.MethodGet, "/users/me", headers)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("GET /users/me as a blacklisted account: expected 403, got %d", resp.StatusCode)
	}
}

// TestValidAPIKeyStillAuthenticates guards the fix against over-correcting into
// anonymity. A key that resolves must produce a principal, or the change would
// have broken authentication rather than repaired it.
func TestValidAPIKeyStillAuthenticates(t *testing.T) {
	h := newHarness(t)

	ctx := context.Background()
	const userID = 5252
	const plaintext = "waifu_valid-account-key"

	_, err := h.db.Pool().Exec(ctx, `
        INSERT INTO "Users" ("Id", "DiscordId", "Name", "Role",
                         "IsBlacklisted", "RequestCount", "JwtRequestCount", "ApiKeyRequestCount")
        VALUES ($1, $2, $3, $4, false, 0, 0, 0)`,
		userID, "discord-valid", "keyed", int(config.RoleUser))
	if err != nil {
		t.Fatalf("seed user: %v", err)
	}

	_, err = h.db.Pool().Exec(ctx, `
        INSERT INTO "ApiKeys" ("UserId", "Description", "KeyHash", "CreatedAt")
        VALUES ($1, $2, $3, now())`,
		userID, "test key", auth.HashAPIKey(plaintext))
	if err != nil {
		t.Fatalf("seed api key: %v", err)
	}

	// /users/me has no [Authorize] in C# but is the endpoint whose response
	// proves a principal was resolved: anonymous returns 401.
	resp := h.requestWithHeaders(http.MethodGet, "/users/me",
		map[string]string{"X-Api-Key": plaintext})
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("GET /users/me with a valid API key: expected 200, got %d", resp.StatusCode)
	}
}

// TestExpiredAPIKeyIsNotAChallenge covers the other way a key stops resolving.
// The lookup filters on ExpirationDate, so an expired key looks unknown, and must
// behave identically rather than becoming a refusal.
func TestExpiredAPIKeyIsNotAChallenge(t *testing.T) {
	h := newHarness(t)

	ctx := context.Background()
	const userID = 6262
	const plaintext = "waifu_expired-account-key"

	_, err := h.db.Pool().Exec(ctx, `
        INSERT INTO "Users" ("Id", "DiscordId", "Name", "Role",
                         "IsBlacklisted", "RequestCount", "JwtRequestCount", "ApiKeyRequestCount")
        VALUES ($1, $2, $3, $4, false, 0, 0, 0)`,
		userID, "discord-expired", "expired", int(config.RoleUser))
	if err != nil {
		t.Fatalf("seed user: %v", err)
	}

	_, err = h.db.Pool().Exec(ctx, `
        INSERT INTO "ApiKeys" ("UserId", "Description", "KeyHash", "CreatedAt", "ExpirationDate")
        VALUES ($1, $2, $3, now() - interval '2 days', now() - interval '1 day')`,
		userID, "expired key", auth.HashAPIKey(plaintext))
	if err != nil {
		t.Fatalf("seed expired api key: %v", err)
	}

	resp := h.requestWithHeaders(http.MethodGet, "/images",
		map[string]string{"X-Api-Key": plaintext})
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		t.Error("an expired API key returned 401 on GET /images; it should fall " +
			"through as anonymous, matching AuthenticateResult.Fail")
	}

	// It must not authenticate either.
	resp = h.requestWithHeaders(http.MethodGet, "/users/me",
		map[string]string{"X-Api-Key": plaintext})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("an expired API key authenticated: GET /users/me returned %d", resp.StatusCode)
	}
}

// ensure fmt stays referenced if the helpers above change shape.
var _ = fmt.Sprintf