package instances

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// # Probing tests
//
// The prober makes outbound requests on behalf of whoever registered a URL, so
// these tests are weighted towards what it refuses rather than what it returns.
// A handler bug shows up as a 500; a guard bug shows up as the backend reading
// its own infrastructure.

// TestNormaliseIsOneIdentityPerInstance covers the property the index depends
// on: two spellings of one instance must produce one string.
func TestNormaliseIsOneIdentityPerInstance(t *testing.T) {
	cases := []struct {
		raw  string
		want string
	}{
		{raw: "https://alt.example", want: "https://alt.example"},
		{raw: "https://alt.example/", want: "https://alt.example"},
		{raw: "https://alt.example:443", want: "https://alt.example"},
		{raw: "https://alt.example:443/", want: "https://alt.example"},
		{raw: "  https://alt.example/  ", want: "https://alt.example"},
		{raw: "HTTPS://ALT.EXAMPLE", want: "https://alt.example"},
		{raw: "https://Alt.Example/Path?x=1#frag", want: "https://alt.example"},
		{raw: "alt.example", want: "https://alt.example"},
		{raw: "alt.example:8443", want: "https://alt.example:8443"},
		{raw: "http://alt.example:80", want: "http://alt.example"},
	}

	for _, c := range cases {
		got, err := Normalise(c.raw)
		if err != nil {
			t.Errorf("Normalise(%q) returned %v", c.raw, err)
			continue
		}
		if got != c.want {
			t.Errorf("Normalise(%q) = %q, want %q", c.raw, got, c.want)
		}
	}
}

// TestNormaliseRejectsUnusableInput covers what must never become an identity.
func TestNormaliseRejectsUnusableInput(t *testing.T) {
	for _, raw := range []string{"", "   ", "https://", "https://user:pass@alt.example", "ftp://alt.example", "https://alt.example:99999"} {
		if got, err := Normalise(raw); err == nil {
			t.Errorf("Normalise(%q) = %q, want an error", raw, got)
		}
	}
}

// TestResolveBlockedIP covers the address ranges an instance URL must not reach.
//
// These are checked on the resolved address rather than on the hostname, because
// a name that resolves to a private address is exactly the case a string check
// on the URL misses.
func TestResolveBlockedIP(t *testing.T) {
	blocked := []string{
		"127.0.0.1",       // loopback
		"127.1.2.3",       // all of 127/8, not just 127.0.0.1
		"::1",             // IPv6 loopback
		"169.254.169.254", // cloud metadata
		"169.254.1.1",     // the rest of link-local
		"10.0.0.5",        // RFC1918
		"172.16.0.1",      // RFC1918
		"192.168.1.1",     // RFC1918
		"100.100.100.200", // Alibaba metadata, inside CGNAT
		"100.64.0.1",      // carrier-grade NAT
		"0.0.0.0",         // unspecified
		"::",              // unspecified
	}

	for _, address := range blocked {
		if !ResolveBlockedIP(net.ParseIP(address)) {
			t.Errorf("ResolveBlockedIP(%s) = false; it must not be dialled", address)
		}
	}

	allowed := []string{"8.8.8.8", "1.1.1.1", "93.184.216.34", "2606:2800:220:1:248:1893:25c8:1946"}

	for _, address := range allowed {
		if ResolveBlockedIP(net.ParseIP(address)) {
			t.Errorf("ResolveBlockedIP(%s) = true; a public address must be dialled", address)
		}
	}

	if !ResolveBlockedIP(nil) {
		t.Error("ResolveBlockedIP(nil) = false; an address that could not be resolved must not be dialled")
	}
}

// TestProberRefusesToReachTheLocalMachine is the end-to-end version of the same
// property: the block list is in the dialer, so it applies even when a caller
// hands the prober a loopback URL directly.
//
// This test is the one that would catch someone moving the check out of
// DialContext into a handler-level validation step, which would leave the
// redirect and re-resolution paths unprotected.
func TestProberRefusesToReachTheLocalMachine(t *testing.T) {
	// A real server on loopback, which is exactly what must not be reachable.
	hit := make(chan struct{}, 1)
	local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hit <- struct{}{}
		writeVersion(w)
	}))
	defer local.Close()

	probe := NewHTTPProber().Probe(context.Background(), local.URL)

	if probe.Reachable {
		t.Errorf("the prober reached %s; loopback must be refused", local.URL)
	}
	if probe.Error == "" {
		t.Error("no error recorded; an unreachable instance must say why")
	}

	select {
	case <-hit:
		t.Error("the request arrived despite the block list")
	default:
	}
}

// TestProberReadsAWorkingInstance covers the happy path end to end, including
// the image HEAD that exercises the {id}{ext} key invariant.
func TestProberReadsAWorkingInstance(t *testing.T) {
	// The listing has to name the test server's own address, which is only known
	// once it is listening.
	var serverURL string

	var imageHit bool
	mux := http.NewServeMux()
	mux.HandleFunc("/version", func(w http.ResponseWriter, r *http.Request) {
		writeVersion(w)
	})
	mux.HandleFunc("/images", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("pageSize") != "1" {
			t.Errorf("pageSize = %q, want 1; the probe must ask for one image, not a page", r.URL.Query().Get("pageSize"))
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"items":[{"id":1,"extension":".webp","url":"%s/media/1.webp"}],`+
			`"pageNumber":1,"totalPages":1,"totalCount":1,"hasNextPage":false,"hasPreviousPage":false}`,
			serverURL)
	})
	mux.HandleFunc("/media/1.webp", func(w http.ResponseWriter, r *http.Request) {
		imageHit = true
		w.WriteHeader(http.StatusOK)
	})

	server := httptest.NewServer(mux)
	defer server.Close()
	serverURL = server.URL

	probe := testProber().Probe(context.Background(), server.URL)
	if !probe.Reachable || !probe.Schema || !probe.Images {
		t.Fatalf("probe = reachable:%v schema:%v images:%v error:%q",
			probe.Reachable, probe.Schema, probe.Images, probe.Error)
	}
	if probe.Software != SoftwareName {
		t.Errorf("software = %q, want %q", probe.Software, SoftwareName)
	}
	if !probe.OK() {
		t.Error("OK() = false for an instance that served its own images")
	}
	if !imageHit {
		t.Error("the image URL was never fetched; that HEAD is the check that catches a 404-ing instance")
	}
}

// TestProbeWithoutHandshakeStillReadsTheCatalogue is the instance that never
// heard of GET /version.
//
// The endpoint is this repository's invention, so its absence says nothing about
// whether the catalogue works. Treating it as downtime would publish a false
// failure for a perfectly good deployment — so the probe asks the two questions
// that do not depend on it, and the contract mismatch is recorded separately.
func TestProbeWithoutHandshakeStillReadsTheCatalogue(t *testing.T) {
	var serverURL string
	var imageHit bool

	mux := http.NewServeMux()
	mux.HandleFunc("/version", func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	})
	mux.HandleFunc("/images", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"items":[{"id":1,"extension":".webp","url":"%s/media/1.webp"}],`+
			`"pageNumber":1,"totalPages":1,"totalCount":1,"hasNextPage":false,"hasPreviousPage":false}`,
			serverURL)
	})
	mux.HandleFunc("/media/1.webp", func(w http.ResponseWriter, r *http.Request) {
		imageHit = true
		w.WriteHeader(http.StatusOK)
	})

	server := httptest.NewServer(mux)
	defer server.Close()
	serverURL = server.URL

	probe := testProber().Probe(context.Background(), server.URL)

	if !probe.NoHandshake {
		t.Errorf("NoHandshake = false; the instance answered 404 on /version. probe = %+v", probe)
	}
	if probe.Reachable {
		t.Error("Reachable = true; the handshake is what it means")
	}
	if !probe.Schema || !probe.Images {
		t.Errorf("schema:%v images:%v, want both true: the catalogue works without the handshake", probe.Schema, probe.Images)
	}
	if !imageHit {
		t.Error("the image URL was never fetched; the checks that do not need the handshake must still run")
	}
	if probe.OK() {
		t.Error("OK() = true without a handshake; any client asking for /version would break")
	}
	if probe.Checkable() {
		t.Error("Checkable() = true; uptime is defined over a check this instance cannot take part in")
	}
	if probe.Error == "" || !strings.Contains(probe.Error, "/version") {
		t.Errorf("Error = %q, want the handshake failure recorded", probe.Error)
	}
}

// TestProbeRecordsARefusalAsUnexamined covers the instance whose edge refuses
// the prober.
//
// A 403 is the "not you" answer: a firewall or bot rule that says nothing about
// whether the catalogue works, because a client on another network may be served
// perfectly. Recording it as a failed check would publish an outage nobody
// observed — so the refusal is carried separately and the signals stay absent.
func TestProbeRecordsARefusalAsUnexamined(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/version", func(w http.ResponseWriter, r *http.Request) {
		writeVersion(w)
	})
	mux.HandleFunc("/images", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "Forbidden", http.StatusForbidden)
	})

	server := httptest.NewServer(mux)
	defer server.Close()

	probe := testProber().Probe(context.Background(), server.URL)

	if !probe.Refused {
		t.Fatalf("Refused = false; the instance answered 403. probe = %+v", probe)
	}
	if probe.Schema || probe.Images {
		t.Errorf("schema:%v images:%v, want both false: nothing was readable", probe.Schema, probe.Images)
	}
	if probe.NoHandshake {
		t.Error("NoHandshake = true; the handshake passed")
	}
	if probe.OK() {
		t.Error("OK() = true")
	}
	if probe.Checkable() {
		t.Error("Checkable() = true; a refusal is not a check anybody can pass or fail")
	}
	if probe.Error == "" || !strings.Contains(probe.Error, "/images") {
		t.Errorf("Error = %q, want the refusal recorded", probe.Error)
	}
}

// TestProbeRecordsAnEmptyCatalogueAsUnusableByImages covers the instance that
// answers correctly but has nothing to serve.
//
// It is not failed outright: the shape was right and the URL was not the problem.
// The image signal is the one that cannot be satisfied, and that is the honest
// place for it to show up.
func TestProbeRecordsAnEmptyCatalogueAsUnusableByImages(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/version", func(w http.ResponseWriter, r *http.Request) { writeVersion(w) })
	mux.HandleFunc("/images", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"items":[],"pageNumber":1,"totalPages":0,"totalCount":0,` +
			`"hasNextPage":false,"hasPreviousPage":false}`))
	})

	server := httptest.NewServer(mux)
	defer server.Close()

	probe := testProber().Probe(context.Background(), server.URL)

	if !probe.Reachable || !probe.Schema {
		t.Errorf("reachable:%v schema:%v, want both true; an empty catalogue is well-formed", probe.Reachable, probe.Schema)
	}
	if probe.Images {
		t.Error("images = true for an instance with no image to serve")
	}
	if probe.OK() {
		t.Error("OK() = true with no images; the uptime figure would count an unusable instance as working")
	}
}

// TestProbeCatchesAnImageThatDoesNotResolve is the check that earns the word
// trust: the instance answers everything and still hands a client a 404.
func TestProbeCatchesAnImageThatDoesNotResolve(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/version", func(w http.ResponseWriter, r *http.Request) { writeVersion(w) })
	mux.HandleFunc("/images", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"items":[{"id":1,"extension":".webp",` +
			`"url":"https://cdn.example/gone.webp"}],"totalCount":1}`))
	})

	server := httptest.NewServer(mux)
	defer server.Close()

	probe := testProber().Probe(context.Background(), server.URL)

	if probe.Schema != true {
		t.Errorf("schema = false; the listing itself was well-formed")
	}
	if probe.Images {
		t.Error("images = true although the image URL does not resolve")
	}
	if probe.OK() {
		t.Error("OK() = true for an instance whose images 404; this is the failure mode the check exists for")
	}
}

// TestProbeCapsTheResponseSize stops a registered URL being a way to exhaust the
// process's memory.
func TestProbeCapsTheResponseSize(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/version", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"software":"waifu-im","pad":"` + strings.Repeat("x", MaxResponseBytes*2) + `"}`))
	})

	server := httptest.NewServer(mux)
	defer server.Close()

	probe := testProber().Probe(context.Background(), server.URL)

	if probe.Reachable {
		t.Error("the probe accepted an oversized response; a hostile URL could otherwise exhaust memory")
	}
	if probe.Error == "" {
		t.Error("no error recorded for an oversized response")
	}
}

// TestBuiltAgeDaysIsNilWhenUnparseable pins the difference between "built
// today" and "we cannot tell".
func TestBuiltAgeDaysIsNilWhenUnparseable(t *testing.T) {
	now := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)

	if days := builtAgeDays("", now); days != nil {
		t.Errorf("builtAgeDays(\"\") = %d, want nil", *days)
	}
	if days := builtAgeDays("not a date", now); days != nil {
		t.Errorf("builtAgeDays(\"not a date\") = %d, want nil", *days)
	}

	three := builtAgeDays("2026-10-12T09:00:00Z", now)
	if three == nil || *three != 3 {
		t.Errorf("builtAgeDays for three days ago = %v, want 3", three)
	}

	future := builtAgeDays("2027-01-01T00:00:00Z", now)
	if future == nil || *future != 0 {
		t.Errorf("builtAgeDays for a future build = %v, want 0 rather than a negative age", future)
	}
}

// TestResolveCommit pins the one-bit genealogy answer.
func TestResolveCommit(t *testing.T) {
	now := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)

	if resolved, _ := ResolveCommit("", now); resolved {
		t.Error("an empty commit resolved; absence must leave the fields absent")
	}
	if resolved, _ := ResolveCommit("abc", now); resolved {
		t.Error("a three-character commit resolved; that is below any safe prefix length")
	}
	if resolved, _ := ResolveCommit("deadbeefdeadbeefdeadbeef", now); resolved {
		t.Error("a commit that is not in the bundle resolved")
	}
}

// TestRecordCheckReplacesTheSameDay stops a second run in one day from
// inflating the denominator.
func TestRecordCheckReplacesTheSameDay(t *testing.T) {
	var record Record
	day := time.Date(2026, 10, 15, 9, 0, 0, 0, time.UTC)

	record.RecordCheck(day, false)
	record.RecordCheck(day.Add(time.Hour), true)

	if len(record.Checks) != 1 {
		t.Fatalf("got %d checks for one day, want 1: %+v", len(record.Checks), record.Checks)
	}
	if !record.Checks[0].OK {
		t.Error("the later check for the same day did not win")
	}
	if uptime := record.Uptime(); uptime == nil || *uptime != 1 {
		t.Errorf("Uptime() = %v, want 1", uptime)
	}
}

// TestRecordCheckPrunesTheWindow bounds the stored history.
//
// Ninety consecutive daily probes is what the prober actually does over three
// months, and the row has to stay bounded by all of them: an index whose JSONB
// cell grows without limit is a read cost paid on every page load.
func TestRecordCheckPrunesTheWindow(t *testing.T) {
	var record Record
	now := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)

	// Oldest first, which is the order time actually arrives in.
	for daysAgo := 90; daysAgo >= 0; daysAgo-- {
		record.RecordCheck(now.Add(-time.Duration(daysAgo)*24*time.Hour), daysAgo%5 != 0)
	}

	if len(record.Checks) > 31 {
		t.Errorf("kept %d checks after 91 daily probes, want at most a month", len(record.Checks))
	}
	if len(record.Checks) == 0 {
		t.Error("pruning removed everything")
	}

	// The retained window must still be the recent one, not an arbitrary subset.
	newest := record.Checks[len(record.Checks)-1]
	if newest.At.After(now.Truncate(24 * time.Hour)) {
		t.Errorf("the newest retained check is %v, which is in the future", newest.At)
	}
	oldest := record.Checks[0]
	if oldest.At.Before(now.Add(-CheckWindow).Truncate(24 * time.Hour)) {
		t.Errorf("the oldest retained check is %v, outside the window", oldest.At)
	}
}

// writeVersion is a minimal valid handshake for the fake instances above.
func writeVersion(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"software":"waifu-im","version":"7.12.0","commit":"unknown","builtAt":"2026-10-14T00:00:00Z"}`))
}

// testProber is the real prober with the block list lifted.
//
// The knob exists because httptest serves on loopback, which is the one range
// the prober is built to refuse. Everything else — the request shaping, the
// response cap, the redirect rules, the signal extraction — is the production
// path, so what these tests exercise is what runs in production, except for the
// address check, which has its own test above.
func testProber() *HTTPProber {
	prober := NewHTTPProber()
	prober.allowBlocked = true
	return prober
}

// TestParseImageListing pins the envelope this endpoint actually answers.
//
// The array is under "items" and the count is "totalCount", alongside the paging
// fields a client reads. A fixture written from memory got this wrong once, and
// every registration was then refused with a schema failure - so the exact bytes
// are asserted here rather than approximated.
//
// The matching test in internal/httpapi feeds this function a response produced
// by the real handler, which is what stops the two drifting apart again.
func TestParseImageListing(t *testing.T) {
	real := `{"items":[{"id":5497,"extension":".webp",` +
		`"url":"https://cdn.example/5497.webp","width":1000,"height":1400}],` +
		`"pageNumber":1,"totalPages":1,"totalCount":1,"maxPageSize":100,` +
		`"defaultPageSize":30,"hasPreviousPage":false,"hasNextPage":false}`

	url, err := ParseImageListing([]byte(real))
	if err != nil {
		t.Fatalf("ParseImageListing on the real response shape: %v", err)
	}
	if url != "https://cdn.example/5497.webp" {
		t.Errorf("url = %q, want the first entry's url", url)
	}

	if _, err := ParseImageListing([]byte(`{"images":[{"url":"x"}]}`)); err == nil {
		t.Error(`an "images" array was accepted; the endpoint answers "items", and ` +
			`accepting both would mean a real shape change passed silently`)
	}
	if _, err := ParseImageListing([]byte(`{"items":[{"url":"x"}]}`)); err == nil {
		t.Error("a listing with no totalCount was accepted")
	}
	if _, err := ParseImageListing([]byte(`not json`)); err == nil {
		t.Error("a non-JSON body was accepted")
	}
	// The empty case needs its own sentinel, because the handler tells the two
	// apart: a well-formed empty catalogue leaves the schema signal true, while
	// a parse failure leaves it false.
	if _, err := ParseImageListing([]byte(`{"items":[],"totalCount":0}`)); err != errEmptyCatalogue {
		t.Errorf("an empty listing returned %v, want the empty-catalogue sentinel", err)
	}
}
