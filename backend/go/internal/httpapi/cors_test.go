package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The site went down because this was missing. A route sweep cannot catch it:
// every path and method matched, and curl never sees a browser's refusal to
// read a response. These tests assert the headers directly.
func TestCORSPreflightIsAnswered(t *testing.T) {
	h := withCORS()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("preflight must not reach the handler")
	}))

	req := httptest.NewRequest(http.MethodOptions, "/images", nil)
	req.Header.Set("Origin", "https://waifu-api-eosin.vercel.app")
	req.Header.Set("Access-Control-Request-Method", "POST")
	req.Header.Set("Access-Control-Request-Headers", "authorization,content-type")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Errorf("preflight status = %d, want 204", rec.Code)
	}
	for header, want := range map[string]string{
		"Access-Control-Allow-Origin":  "*",
		"Access-Control-Allow-Methods": "",
		"Access-Control-Allow-Headers": "Origin, Content-Type, Accept, Authorization, X-Api-Key, X-Requested-With",
	} {
		got := rec.Header().Get(header)
		if got == "" {
			t.Errorf("%s missing from the preflight response", header)
		} else if want != "" && got != want {
			t.Errorf("%s = %q, want %q", header, got, want)
		}
	}
	if m := rec.Header().Get("Access-Control-Allow-Methods"); !strings.Contains(m, "POST") {
		t.Errorf("Allow-Methods %q does not include POST", m)
	}
}

func TestCORSHeadersPresentOnNormalResponses(t *testing.T) {
	h := withCORS()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// A real request from the deployed frontend's origin.
	req := httptest.NewRequest(http.MethodGet, "/stats/public", nil)
	req.Header.Set("Origin", "https://waifu-api-eosin.vercel.app")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Errorf("Access-Control-Allow-Origin = %q, want \"*\"", got)
	}
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d", rec.Code)
	}
}

func TestCORSHeadersPresentOnErrorResponses(t *testing.T) {
	// A browser that cannot read the error is as broken as one that cannot read
	// the success, and a 500 with no header looks like a network failure.
	h := withCORS()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))

	req := httptest.NewRequest(http.MethodGet, "/images", nil)
	req.Header.Set("Origin", "https://example.invalid")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Errorf("no CORS header on a 500: got %q", got)
	}
}

// Authorization is the whole point of the header list. A wildcard does not
// cover it, and a browser that is told "*" refuses the request that follows.
//
// This asserts the literal value rather than checking for a wildcard, because
// a wildcard here looks correct and fails in the browser.
func TestCORSAllowHeadersNamesAuthorizationExplicitly(t *testing.T) {
	h := withCORS()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))

	req := httptest.NewRequest(http.MethodOptions, "/users/me", nil)
	req.Header.Set("Origin", "https://waifu-api-eosin.vercel.app")
	req.Header.Set("Access-Control-Request-Method", "GET")
	req.Header.Set("Access-Control-Request-Headers", "authorization")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	got := rec.Header().Get("Access-Control-Allow-Headers")
	if got == "*" {
		t.Fatal("Allow-Headers is \"*\", which does not cover Authorization")
	}
	for _, want := range []string{"authorization", "content-type", "x-api-key"} {
		if !strings.Contains(strings.ToLower(got), want) {
			t.Errorf("Allow-Headers %q does not name %s", got, want)
		}
	}
}
