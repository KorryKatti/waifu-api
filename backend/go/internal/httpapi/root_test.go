package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"waifuapi/internal/config"
)

// GET /, the index.
//
// The tests here cover three things that are separately easy to break and
// separately invisible: the index is JSON, every link in it resolves to a route
// the mux actually serves, and registering the root did not turn the mux into a
// catch-all.

// rootMux registers the whole surface for a configured base path, and returns
// the mux alongside the base as the patterns were built from it.
//
// It goes through Register rather than calling the handler directly, because the
// base path is where the pattern and the links are both derived from and a
// mismatch between them is the interesting failure. The resolved base is
// returned rather than the configured one because that is what the routes use:
// an empty API_BASE_PATH is served from "/".
func rootMux(basePath string) (*http.ServeMux, string) {
	server := &Server{cfg: &config.Config{BasePath: basePath}}
	mux := http.NewServeMux()
	server.Register(mux)

	return mux, server.cfg.BasePathOrRoot()
}

// serveRoot GETs the root of a registered surface and returns the decoded body.
func serveRoot(t *testing.T, basePath string) map[string]any {
	t.Helper()

	mux, base := rootMux(basePath)

	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, base, nil))

	if recorder.Code != http.StatusOK {
		t.Fatalf("GET %q = %d, want 200", base, recorder.Code)
	}
	if ctype := recorder.Header().Get("Content-Type"); !strings.Contains(ctype, "json") {
		t.Errorf("Content-Type = %q, want JSON; a client parsing the index cannot read text", ctype)
	}

	var body map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body %q: %v", recorder.Body.String(), err)
	}
	return body
}

// TestRootIsJSONWithTheBuildIdentity checks the index answers with the same build
// identity as /version.
//
// One build, one answer. If the root reported a different version than /version,
// a client that checked the root would be told it was talking to a different
// deployment than one that checked /version.
func TestRootIsJSONWithTheBuildIdentity(t *testing.T) {
	restore := setBuildIdentity("7.12.0", "9544404", "2026-10-15T09:12:44Z")
	defer restore()

	for _, base := range []string{"", "/api"} {
		body := serveRoot(t, base)

		for key, want := range map[string]any{
			"software": softwareName,
			"version":  "7.12.0",
			"commit":   "9544404",
			"builtAt":  "2026-10-15T09:12:44Z",
		} {
			if body[key] != want {
				t.Errorf("base %q: %s = %v, want %v; the root and /version must report one build", base, key, body[key], want)
			}
		}
	}
}

// TestRootLinksAreServed resolves every advertised link against the registered
// mux.
//
// The index is a promise, and a promise nobody checks is decoration. Building the
// links from the same expression as the patterns is what should keep them true;
// this is the test that fails when someone hand-writes a path one day.
func TestRootLinksAreServed(t *testing.T) {
	for _, configured := range []string{"", "/api"} {
		mux, base := rootMux(configured)

		for name, path := range rootLinks(base) {
			if !strings.HasPrefix(path, "/") {
				t.Errorf("base %q: link %q is %q, which is not a path the mux could serve", base, name, path)
				continue
			}
			if _, pattern := mux.Handler(httptest.NewRequest(http.MethodGet, path, nil)); pattern == "" {
				t.Errorf("base %q: link %q advertises %q, which no route serves", base, name, path)
			}
		}
	}
}

// TestRootDoesNotCatchUnmatchedPaths is the test the {$} in the pattern earns.
//
// Registering "GET /" instead of "GET /{$}" is not a style preference: it is a
// subtree pattern, so every GET that no other route claims would answer 200 with
// the index. A typo'd path, a stale frontend asset, a scanner — all of them would
// be indistinguishable from a real request, and the 404s that make a broken
// deploy visible would be gone.
func TestRootDoesNotCatchUnmatchedPaths(t *testing.T) {
	server := &Server{cfg: &config.Config{}}
	mux := http.NewServeMux()
	server.Register(mux)

	for _, path := range []string{"/imagess", "/png", "/images/1/extra", "/a/b/c"} {
		probe := httptest.NewRequest(http.MethodGet, path, nil)
		_, pattern := mux.Handler(probe)

		recorder := httptest.NewRecorder()
		mux.ServeHTTP(recorder, probe)

		if recorder.Code == http.StatusOK {
			t.Errorf("GET %q = 200 from pattern %q; the index is answering paths it does not own", path, pattern)
		}
	}
}

// TestRootAnswersHEAD confirms GET also answers HEAD.
//
// net/http maps HEAD onto a GET pattern, so this needs no second registration —
// but the root is the path a human or a checker tends to poke first, and a
// 405 there reads as a broken service.
func TestRootAnswersHEAD(t *testing.T) {
	server := &Server{cfg: &config.Config{}}
	mux := http.NewServeMux()
	server.Register(mux)

	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, httptest.NewRequest(http.MethodHead, "/", nil))

	if recorder.Code != http.StatusOK {
		t.Errorf("HEAD / = %d, want 200", recorder.Code)
	}
}
