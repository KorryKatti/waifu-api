package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"waifuapi/internal/config"
)

// GET /version, the instance handshake.
//
// These tests are white-box and deliberately use a zero-value Server. /version
// is the one endpoint whose entire value depends on answering with no database,
// no repository and no configuration, because that is what lets an instance
// serving only from a CDN be recognised at all. Handing the handler a Server with
// every field nil makes "reads nothing from s" an assertion that fails loudly,
// rather than a property a future edit can quietly break.

// serveVersion calls the handler on a Server with no dependencies and returns
// the decoded body.
func serveVersion(t *testing.T) map[string]any {
	t.Helper()

	recorder := httptest.NewRecorder()
	(&Server{}).handleVersion(recorder, httptest.NewRequest(http.MethodGet, "/version", nil))

	if recorder.Code != http.StatusOK {
		t.Fatalf("GET /version = %d, want 200", recorder.Code)
	}
	if ctype := recorder.Header().Get("Content-Type"); ctype == "" {
		t.Error("Content-Type is empty; clients decoding this as JSON cannot tell")
	}

	var body map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body %q: %v", recorder.Body.String(), err)
	}
	return body
}

// TestVersionNeedsNoDependencies serves the handshake from a Server whose
// database, repository, config and storage are all nil.
//
// A handler that pinged the pool, read a setting or counted a request would
// panic here. That is the point: the constraint is that a CDN-only instance is
// verifiable, and it is cheap to keep true if breaking it breaks a test.
func TestVersionNeedsNoDependencies(t *testing.T) {
	body := serveVersion(t)

	want := []string{"software", "version", "commit", "builtAt"}
	for _, key := range want {
		if _, ok := body[key]; !ok {
			t.Errorf("handshake has no %q; a checker reading it cannot tell %q", key, key)
		}
	}

	// The software name is a constant precisely so that a fork or a
	// reimplementation can answer it. Nothing about the deployment may leak in.
	if body["software"] != softwareName {
		t.Errorf("software = %v, want %q", body["software"], softwareName)
	}

	// No field may be an empty string. "" reads as a broken build; "unknown"
	// is what "the builder did not supply this" has to look like.
	for key, value := range body {
		if text, isString := value.(string); isString && text == "" {
			t.Errorf("%s is empty; an unstamped field must read %q, not %q", key, "unknown", "")
		}
	}
}

// TestVersionReportsTheStampedIdentity checks the handler serves the package
// variables, which is the whole contract between -X and the response.
func TestVersionReportsTheStampedIdentity(t *testing.T) {
	restore := setBuildIdentity("7.12.0", "9544404", "2026-10-15T09:12:44Z")
	defer restore()

	body := serveVersion(t)

	if body["version"] != "7.12.0" {
		t.Errorf("version = %v, want 7.12.0", body["version"])
	}
	if body["commit"] != "9544404" {
		t.Errorf("commit = %v, want 9544404", body["commit"])
	}
	if body["builtAt"] != "2026-10-15T09:12:44Z" {
		t.Errorf("builtAt = %v, want the stamped timestamp", body["builtAt"])
	}
}

// TestVersionIsServedUnderTheBasePath confirms the handshake sits behind
// API_BASE_PATH like every other route.
//
// A checker probing an instance configured with a base path would get a 404 from
// the path itself, which reads as "not this software" rather than "wrong path".
func TestVersionIsServedUnderTheBasePath(t *testing.T) {
	for _, base := range []string{"", "/api"} {
		server := &Server{cfg: &config.Config{BasePath: base}}
		mux := http.NewServeMux()
		server.Register(mux)

		want := base + "version"
		for _, method := range []string{http.MethodGet, http.MethodHead} {
			probe := httptest.NewRequest(method, "/"+want, nil)
			if _, pattern := mux.Handler(probe); pattern == "" {
				t.Errorf("base %q: %s /%s is not served", base, method, want)
			}
		}
	}
}

// TestBuildInfoCarriesTheStamps keeps /healthz and /version from disagreeing
// about which build is running.
func TestBuildInfoCarriesTheStamps(t *testing.T) {
	restore := setBuildIdentity("7.12.0", "9544404", "2026-10-15T09:12:44Z")
	defer restore()

	info := buildInfo()

	for key, want := range map[string]string{
		"version": "7.12.0",
		"commit":  "9544404",
		"builtAt": "2026-10-15T09:12:44Z",
	} {
		if info[key] != want {
			t.Errorf("buildInfo[%q] = %v, want %q; /healthz and /version must report one build", key, info[key], want)
		}
	}
}

// TestStampedRecognisesAMissingStamp covers the CI assertion's own predicate.
//
// The defaults are the whole signal: a build whose -ldflags were dropped leaves
// every field at its link-time value, and the image still builds, still starts
// and still serves. Stamped is what turns that into a warning in the log and a
// failure in CI.
func TestStampedRecognisesAMissingStamp(t *testing.T) {
	cases := []struct {
		name                     string
		version, commit, builtAt string
		want                     bool
	}{
		{name: "nothing stamped", version: "0.0.0-dev", commit: "unknown", builtAt: "unknown", want: false},
		{name: "version only", version: "7.12.0", commit: "unknown", builtAt: "unknown", want: false},
		{name: "no commit", version: "7.12.0", commit: "unknown", builtAt: "2026-10-15T09:12:44Z", want: false},
		{name: "no build time", version: "7.12.0", commit: "9544404", builtAt: "unknown", want: false},
		{name: "no version", version: "0.0.0-dev", commit: "9544404", builtAt: "2026-10-15T09:12:44Z", want: false},
		{name: "fully stamped", version: "7.12.0", commit: "9544404", builtAt: "2026-10-15T09:12:44Z", want: true},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			defer setBuildIdentity(c.version, c.commit, c.builtAt)()

			if got := Stamped(); got != c.want {
				t.Errorf("Stamped() = %v for version=%q commit=%q builtAt=%q, want %v",
					got, c.version, c.commit, c.builtAt, c.want)
			}
		})
	}
}

// setBuildIdentity stamps the package variables and returns a function putting
// the previous values back, so a test cannot leak its build into the next one.
func setBuildIdentity(version, commit, builtAt string) func() {
	previousVersion, previousCommit, previousBuiltAt := BuildVersion, BuildCommit, BuildAt

	BuildVersion, BuildCommit, BuildAt = version, commit, builtAt

	return func() {
		BuildVersion, BuildCommit, BuildAt = previousVersion, previousCommit, previousBuiltAt
	}
}
