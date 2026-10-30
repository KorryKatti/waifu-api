package httpapi_test

import (
	"context"
	"encoding/json"
	"io"
	"testing"

	"waifuapi/internal/config"
)

// The admin dashboard broke in production because the route, the method and
// the status code were all correct and only the body was wrong. These tests
// assert the body, against the field list the reference service returns.

// getAdminStats calls the endpoint as an admin and returns the status and body.
func ctxBackground() context.Context { return context.Background() }

func getAdminStats(t *testing.T, h *harness, token string) (int, []byte) {
	t.Helper()
	resp := h.request("GET", "/stats/admin", token, nil, "")
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return resp.StatusCode, body
}

// requiredAdminFields is AdminStatsDto from the C# service, in the order the
// OpenAPI schema declares them. Every one is read by frontend/src/pages/AdminStats.tsx.
var requiredAdminFields = []string{
	"requestsToday",
	"history",
	"topUsers",
	"topApiKeyUsers",
	"topJwtUsers",
	"topUploaders",
	"topAlbumUsers",
	"totalRequests",
	"totalAuthenticatedRequests",
	"totalUnauthenticatedRequests",
	"totalApiKeyRequests",
	"totalJwtRequests",
	"totalImages",
	"totalTags",
	"totalArtists",
}

func TestAdminStatsReturnsEveryFieldTheDashboardReads(t *testing.T) {
	h := newHarness(t)
	defer h.server.Close()

	admin := h.issueToken(9001, config.RoleAdmin)
	code, raw := getAdminStats(t, h, admin)
	if code != 200 {
		t.Fatalf("GET /stats/admin = %d, want 200: %s", code, raw)
	}

	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("response is not JSON: %v", err)
	}

	for _, field := range requiredAdminFields {
		if _, ok := body[field]; !ok {
			t.Errorf("field %q is missing from GET /stats/admin; the dashboard reads it "+
				"and throws when it is absent", field)
		}
	}
}

func TestAdminStatsLeaderboardsAreArraysNotNull(t *testing.T) {
	h := newHarness(t)
	defer h.server.Close()

	admin := h.issueToken(9002, config.RoleAdmin)
	code, raw := getAdminStats(t, h, admin)
	if code != 200 {
		t.Fatalf("GET /stats/admin = %d, want 200: %s", code, raw)
	}

	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("response is not JSON: %v", err)
	}

	// The dashboard iterates these directly. A null serialises fine and then
	// throws in the browser, so [] is asserted rather than just "present".
	for _, field := range []string{
		"history", "topUsers", "topApiKeyUsers",
		"topJwtUsers", "topUploaders", "topAlbumUsers",
	} {
		v, ok := body[field]
		if !ok {
			t.Errorf("%q missing", field)
			continue
		}
		if _, isArray := v.([]any); !isArray {
			t.Errorf("%q is %T, want [] - null breaks the dashboard", field, v)
		}
	}
}

func TestAdminStatsLeaderboardRowsHaveTheRightColumns(t *testing.T) {
	h := newHarness(t)
	defer h.server.Close()

	// A user with requests, so at least one leaderboard row exists to inspect.
	// The Id is explicit because the Users identity sequence is shared with
	// rows other tests create, and relying on nextval collided.
	const seededID = 900100
	if _, err := h.db.Pool().Exec(ctxBackground(),
		`INSERT INTO "Users" ("Id","DiscordId","Name","Role","IsBlacklisted",
			"RequestCount","JwtRequestCount","ApiKeyRequestCount")
		 VALUES ($1,'seed-admin-top','topuser',0,false,900,5,7)
		 ON CONFLICT ("Id") DO UPDATE
		   SET "RequestCount"=900, "ApiKeyRequestCount"=7, "JwtRequestCount"=5`,
		seededID); err != nil {
		t.Fatalf("seed top user: %v", err)
	}

	admin := h.issueToken(9003, config.RoleAdmin)
	code, raw := getAdminStats(t, h, admin)
	if code != 200 {
		t.Fatalf("GET /stats/admin = %d: %s", code, raw)
	}

	var body struct {
		TopUsers []struct {
			ID                 int64  `json:"id"`
			Name               string `json:"name"`
			RequestCount       int64  `json:"requestCount"`
			APIKeyRequestCount int64  `json:"apiKeyRequestCount"`
			JWTRequestCount    int64  `json:"jwtRequestCount"`
		} `json:"topUsers"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.TopUsers) == 0 {
		t.Fatal("topUsers is empty; the seeded user with 900 requests is missing")
	}

	top := body.TopUsers[0]
	if top.Name != "topuser" {
		t.Errorf("top user is %q, want the seeded one", top.Name)
	}
	if top.RequestCount != 900 {
		t.Errorf("requestCount = %d, want 900", top.RequestCount)
	}
	if top.APIKeyRequestCount != 7 || top.JWTRequestCount != 5 {
		t.Errorf("per-type counters = %d/%d, want 7/5",
			top.APIKeyRequestCount, top.JWTRequestCount)
	}
}

func TestAdminStatsUnauthenticatedCountsCannotGoNegative(t *testing.T) {
	h := newHarness(t)
	defer h.server.Close()

	admin := h.issueToken(9004, config.RoleAdmin)
	code, raw := getAdminStats(t, h, admin)
	if code != 200 {
		t.Fatalf("GET /stats/admin = %d: %s", code, raw)
	}

	var body struct {
		TotalRequests                int64 `json:"totalRequests"`
		TotalAuthenticatedRequests   int64 `json:"totalAuthenticatedRequests"`
		TotalUnauthenticatedRequests int64 `json:"totalUnauthenticatedRequests"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.TotalUnauthenticatedRequests < 0 {
		t.Errorf("totalUnauthenticatedRequests = %d, must not be negative",
			body.TotalUnauthenticatedRequests)
	}
}
