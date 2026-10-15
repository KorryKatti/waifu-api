package httpapi_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"testing"
	"time"

	"waifuapi/internal/config"
	"waifuapi/internal/instances"
)

// The instance index endpoints.
//
// The public endpoint is the one that cannot get this wrong quietly: everything
// it does wrong is invisible locally and visible to somebody pointing a client
// at a stranger's server. The tests below are weighted towards the shapes of
// failure that produce valid-looking answers — a leaked owner id, a field
// defaulting to a confident value, a race that loses a registration.

type publicList struct {
	Instances []map[string]any `json:"instances"`
}

func getInstanceList(t *testing.T, h *harness, token string) (int, []byte) {
	t.Helper()

	resp := h.request(http.MethodGet, "/instances", token, nil, "")
	return resp.StatusCode, readAll(t, resp)
}

// register posts an instance as an admin and returns the status and body.
func register(t *testing.T, h *harness, token, url, name, discordID string) (int, []byte) {
	t.Helper()

	payload, err := json.Marshal(map[string]string{
		"url":           url,
		"name":          name,
		"discordUserId": discordID,
	})
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}

	resp := h.request(http.MethodPost, "/instances", token,
		bytes.NewReader(payload), "application/json")
	return resp.StatusCode, readAll(t, resp)
}

// TestInstancesListIsEmptyNotNull pins the shape of an index nobody has
// registered with.
//
// A null where a client expects an array is a crash in every consumer that
// iterates, and it only shows up once someone has registered an instance, which
// is the worst time to find out.
func TestInstancesListIsEmptyNotNull(t *testing.T) {
	h := newHarness(t)

	code, raw := getInstanceList(t, h, "")
	if code != http.StatusOK {
		t.Fatalf("GET /instances = %d, want 200: %s", code, raw)
	}

	var decoded publicList
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("decode %s: %v", raw, err)
	}
	if decoded.Instances == nil {
		t.Errorf("instances is null in %s; an empty index is an empty array", raw)
	}
	if len(decoded.Instances) != 0 {
		t.Errorf("got %d instances before any were registered", len(decoded.Instances))
	}
}

// TestInstancesListNeedsNoAuth confirms the discovery page is open.
func TestInstancesListNeedsNoAuth(t *testing.T) {
	h := newHarness(t)

	code, _ := getInstanceList(t, h, "")
	if code == http.StatusUnauthorized {
		t.Error("GET /instances requires auth; it is a public discovery list")
	}
	if code != http.StatusOK {
		t.Errorf("GET /instances = %d, want 200", code)
	}
}

// TestRegisterRequiresAdmin covers the write gate.
//
// Registration is admin-gated because the backend will make an HTTP request to
// whatever URL it is given. That lowers the risk to social engineering an
// operator rather than opening it to the public, and the URL block list is the
// second layer.
func TestRegisterRequiresAdmin(t *testing.T) {
	h := newHarness(t)

	user := h.issueToken(9101, config.RoleUser)
	code, raw := register(t, h, user, "https://alt.example", "alt", "123456789")
	if code != http.StatusForbidden {
		t.Errorf("POST /instances as a user = %d, want 403: %s", code, raw)
	}

	if h.prober.probed("https://alt.example") {
		t.Error("a refused registration still caused an outbound request")
	}
}

// TestRegisterNormalisesAndProbes covers the happy path and the two things that
// make it meaningful: the URL is canonicalised before it becomes an identity,
// and the probe happens before anything is written.
func TestRegisterNormalisesAndProbes(t *testing.T) {
	h := newHarness(t)
	admin := h.issueToken(9102, config.RoleAdmin)

	code, raw := register(t, h, admin, "  HTTPS://Alt.Example:443/api/  ", "  Alt Instance ", "123456789")
	if code != http.StatusCreated {
		t.Fatalf("POST /instances = %d, want 201: %s", code, raw)
	}

	if !h.prober.probed("https://alt.example") {
		t.Errorf("the instance was stored without being probed; probed %v", h.prober.probedURLs())
	}

	_, listRaw := getInstanceList(t, h, "")

	var listed publicList
	if err := json.Unmarshal(listRaw, &listed); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(listed.Instances) != 1 {
		t.Fatalf("got %d instances, want 1", len(listed.Instances))
	}

	entry := listed.Instances[0]
	if entry["url"] != "https://alt.example" {
		t.Errorf("url = %v, want the normalised origin", entry["url"])
	}
	if entry["name"] != "  Alt Instance " {
		t.Errorf("name = %v; display text should be kept verbatim", entry["name"])
	}
}

// TestPublicListOmitsTheOwner is the leak test.
//
// A single endpoint with a role-dependent shape would make this a question about
// whether the auth check fired on this path. Separate response types make it a
// question about the type, which a compiler answers.
func TestPublicListOmitsTheOwner(t *testing.T) {
	h := newHarness(t)
	admin := h.issueToken(9103, config.RoleAdmin)

	if code, raw := register(t, h, admin, "https://alt.example", "alt", "123456789"); code != http.StatusCreated {
		t.Fatalf("register = %d: %s", code, raw)
	}

	_, raw := getInstanceList(t, h, "")
	if bytes.Contains(bytes.ToLower(raw), []byte("discord")) {
		t.Errorf("the public list mentions the owner: %s", raw)
	}
	if bytes.Contains(raw, []byte("123456789")) {
		t.Errorf("the public list contains the owner's Discord id: %s", raw)
	}

	// And the admin endpoint is the one that carries it.
	resp := h.request(http.MethodGet, "/instances/admin", admin, nil, "")
	adminRaw := readAll(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /instances/admin = %d: %s", resp.StatusCode, adminRaw)
	}
	if !bytes.Contains(adminRaw, []byte("123456789")) {
		t.Errorf("the admin list does not carry the owner: %s", adminRaw)
	}
}

// TestAdminListRequiresAdmin guards the endpoint that carries the owner.
func TestAdminListRequiresAdmin(t *testing.T) {
	h := newHarness(t)

	user := h.issueToken(9104, config.RoleUser)
	resp := h.request(http.MethodGet, "/instances/admin", user, nil, "")
	body := readAll(t, resp)

	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("GET /instances/admin as a user = %d, want 403: %s", resp.StatusCode, body)
	}

	anonymous := h.request(http.MethodGet, "/instances/admin", "", nil, "")
	anonymousBody := readAll(t, anonymous)
	if anonymous.StatusCode != http.StatusUnauthorized {
		t.Errorf("GET /instances/admin anonymously = %d, want 401: %s", anonymous.StatusCode, anonymousBody)
	}
}

// TestRegisterRefusesAnInstanceThatDoesNotAnswer is the point of probing before
// writing.
//
// Listing an instance that cannot serve its own images would make the list
// worse than useless: it would send readers to a URL that 404s, which is the one
// outcome the list exists to prevent.
func TestRegisterRefusesAnInstanceThatDoesNotAnswer(t *testing.T) {
	h := newHarness(t)
	admin := h.issueToken(9105, config.RoleAdmin)

	h.prober.result = unreachableProbe("GET /version: dial tcp: connection refused")

	code, raw := register(t, h, admin, "https://broken.example", "broken", "123456789")
	if code != http.StatusUnprocessableEntity {
		t.Fatalf("registering an unreachable instance = %d, want 422: %s", code, raw)
	}

	_, listed := getInstanceList(t, h, "")
	if bytes.Contains(listed, []byte("broken.example")) {
		t.Errorf("a failed probe was stored anyway: %s", listed)
	}
}

// TestRegisterIsIdempotent covers running the command twice.
//
// The failure this prevents is subtle and would be invisible: an overwrite
// resets the check history, which reports a fresh week of perfect uptime for an
// instance that has been failing for a month.
func TestRegisterIsIdempotent(t *testing.T) {
	h := newHarness(t)
	admin := h.issueToken(9106, config.RoleAdmin)

	if code, raw := register(t, h, admin, "https://alt.example", "first name", "111111111"); code != http.StatusCreated {
		t.Fatalf("first register = %d: %s", code, raw)
	}

	// A month of failing checks, as the prober would have recorded.
	record := mustFind(t, h, "https://alt.example")
	record.Checks = nil
	for i := 0; i < 20; i++ {
		record.Checks = append(record.Checks,
			instances.Check{At: time.Now().UTC().Add(-time.Duration(i) * 24 * time.Hour), OK: false})
	}
	if err := h.data.UpdateInstance(t.Context(), record.URL, func(r *instances.Record) {
		r.Checks = record.Checks
	}); err != nil {
		t.Fatalf("write failing history: %v", err)
	}

	code, raw := register(t, h, admin, "https://alt.example", "second name", "222222222")
	if code != http.StatusOK {
		t.Fatalf("second register = %d, want 200 rather than a rewrite: %s", code, raw)
	}

	after := mustFind(t, h, "https://alt.example")
	if after.Name != "first name" {
		t.Errorf("name = %q; re-registering must not overwrite the stored entry", after.Name)
	}
	if after.DiscordUserID != "111111111" {
		t.Errorf("owner = %q; re-registering must not hand the entry to the second caller", after.DiscordUserID)
	}
	if len(after.Checks) != 20 {
		t.Errorf("check history has %d entries, want 20; re-registering must not reset it", len(after.Checks))
	}
}

// TestRegisterRejectsABadURL covers the input that would otherwise become an
// identity.
func TestRegisterRejectsABadURL(t *testing.T) {
	h := newHarness(t)
	admin := h.issueToken(9107, config.RoleAdmin)

	cases := []struct {
		name string
		url  string
	}{
		{name: "empty", url: ""},
		{name: "not a url", url: "http://%zz"},
		{name: "credentials", url: "https://user:pass@alt.example"},
		{name: "unsupported scheme", url: "ftp://alt.example"},
		{name: "no host", url: "https://"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			code, raw := register(t, h, admin, c.url, "x", "")
			if code != http.StatusBadRequest {
				t.Errorf("register(%q) = %d, want 400: %s", c.url, code, raw)
			}
		})
	}

	if len(h.prober.seen) != 0 {
		t.Errorf("a rejected URL was still fetched: %v", h.prober.probedURLs())
	}
}

// TestRegisterRejectsAUsernameAsOwner guards the role grant's key.
func TestRegisterRejectsAUsernameAsOwner(t *testing.T) {
	h := newHarness(t)
	admin := h.issueToken(9108, config.RoleAdmin)

	code, raw := register(t, h, admin, "https://alt.example", "alt", "max")
	if code != http.StatusBadRequest {
		t.Errorf("register with a username as the owner = %d, want 400: %s", code, raw)
	}
}

// TestPublicSignalsAreAbsentForAFork pins the difference between "behind" and
// "we could not tell".
//
// A fork that reimplements the API faithfully passes every behavioural check,
// which is the intended outcome: the index promises compatibility, not
// provenance. What it must not do is invent a commit age for a hash that
// resolves to no history.
func TestPublicSignalsAreAbsentForAFork(t *testing.T) {
	h := newHarness(t)
	admin := h.issueToken(9109, config.RoleAdmin)

	h.prober.result = noUpstreamProbe()

	if code, raw := register(t, h, admin, "https://fork.example", "fork", "123456789"); code != http.StatusCreated {
		t.Fatalf("register = %d: %s", code, raw)
	}

	_, raw := getInstanceList(t, h, "")

	var listed publicList
	if err := json.Unmarshal(raw, &listed); err != nil {
		t.Fatalf("decode: %v", err)
	}
	entry := listed.Instances[0]

	for _, field := range []string{"upstream", "commitAgeDays"} {
		value, present := entry[field]
		if present && value != nil {
			t.Errorf("%s = %v for a fork; absent means we could not tell, and a number here would be meaningless", field, value)
		}
	}

	// Behavioural signals are populated for everyone, including forks.
	if entry["schema"] != true || entry["images"] != true {
		t.Errorf("a working fork reports schema=%v images=%v, want both true", entry["schema"], entry["images"])
	}
}

// TestPublicListReportsSignalsSeparately covers the published components.
func TestPublicListReportsSignalsSeparately(t *testing.T) {
	h := newHarness(t)
	admin := h.issueToken(9110, config.RoleAdmin)

	if code, raw := register(t, h, admin, "https://alt.example", "alt", "123456789"); code != http.StatusCreated {
		t.Fatalf("register = %d: %s", code, raw)
	}

	_, raw := getInstanceList(t, h, "")

	var listed publicList
	if err := json.Unmarshal(raw, &listed); err != nil {
		t.Fatalf("decode: %v", err)
	}
	entry := listed.Instances[0]

	for _, field := range []string{"url", "uptime", "schema", "images", "builtAgeDays", "lastCheckedAt"} {
		if _, present := entry[field]; !present {
			t.Errorf("%s is missing; the components are published individually rather than as a score", field)
		}
	}

	if entry["uptime"] != float64(1) {
		t.Errorf("uptime = %v, want 1 for a single passing check", entry["uptime"])
	}

	// The score is a composite and is deliberately absent: a lone number hides
	// its own weights and the person it is about is the one who would have to
	// trust it.
	for _, field := range []string{"score", "rank", "tier", "rating"} {
		if _, present := entry[field]; present {
			t.Errorf("%s is published; the index reports components, not a composite", field)
		}
	}
}

// TestUptimeIsNullBeforeAnyCheck pins honesty about what has been observed.
func TestUptimeIsNullBeforeAnyCheck(t *testing.T) {
	record := instances.Record{URL: "https://alt.example"}

	uptime := record.Uptime()
	if uptime != nil {
		t.Errorf("Uptime() = %v for an instance with no checks, want nil", *uptime)
	}
}

// TestRemoveInstance covers the way back off the list.
//
// Without it a URL registered in error stays on a public list claiming to be
// healthy until somebody edits a JSON document by hand.
func TestRemoveInstance(t *testing.T) {
	h := newHarness(t)
	admin := h.issueToken(9111, config.RoleAdmin)

	if code, raw := register(t, h, admin, "https://alt.example", "alt", "123456789"); code != http.StatusCreated {
		t.Fatalf("register = %d: %s", code, raw)
	}

	resp := h.request(http.MethodDelete, "/instances?url=HTTPS://Alt.Example/", admin, nil, "")
	body := readAll(t, resp)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("DELETE /instances = %d, want 204: %s", resp.StatusCode, body)
	}

	_, raw := getInstanceList(t, h, "")
	if bytes.Contains(raw, []byte("alt.example")) {
		t.Errorf("the instance is still listed after removal: %s", raw)
	}

	// Removing something absent is a 404 rather than a silent success.
	resp = h.request(http.MethodDelete, "/instances?url=https://alt.example", admin, nil, "")
	missing := readAll(t, resp)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("DELETE for an unregistered URL = %d, want 404: %s", resp.StatusCode, missing)
	}
}

// TestRemoveRequiresAdmin guards the delete gate.
func TestRemoveRequiresAdmin(t *testing.T) {
	h := newHarness(t)

	user := h.issueToken(9112, config.RoleUser)
	resp := h.request(http.MethodDelete, "/instances?url=https://alt.example", user, nil, "")
	body := readAll(t, resp)

	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("DELETE as a user = %d, want 403: %s", resp.StatusCode, body)
	}
}

// mustFind reads one entry straight from the repository.
func mustFind(t *testing.T, h *harness, url string) *instances.Record {
	t.Helper()

	records, err := h.data.GetInstances(t.Context())
	if err != nil {
		t.Fatalf("read index: %v", err)
	}

	found := instances.Find(records, url)
	if found == nil {
		t.Fatalf("%s is not in the index: %+v", url, records)
	}
	return found
}

// TestConcurrentRegistrationsAllSurvive is the test the row lock exists for.
//
// Read-modify-write on a single JSONB cell loses a writer whenever two of them
// overlap: both read the same list, both write their own version, and the
// second write discards the first. Nothing reports it — the operator sees two
// commands succeed and one instance simply never appears.
//
// Without SELECT ... FOR UPDATE this fails intermittently, which is worse than
// failing predictably, so the goroutines are released together rather than
// staggered.
func TestConcurrentRegistrationsAllSurvive(t *testing.T) {
	h := newHarness(t)

	const registrations = 8

	var wg sync.WaitGroup
	start := make(chan struct{})
	codes := make([]int, registrations)

	for i := 0; i < registrations; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()

			admin := h.issueToken(int64(9200+index), config.RoleAdmin)
			url := fmt.Sprintf("https://alt%d.example", index)

			<-start

			resp := h.request(http.MethodPost, "/instances", admin,
				bytes.NewReader([]byte(`{"url":"`+url+`","name":"alt","discordUserId":"123456789"}`)),
				"application/json")
			codes[index] = resp.StatusCode
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
		}(i)
	}

	close(start)
	wg.Wait()

	for index, code := range codes {
		if code != http.StatusCreated && code != http.StatusOK {
			t.Errorf("registration %d = %d", index, code)
		}
	}

	_, raw := getInstanceList(t, h, "")
	var listed publicList
	if err := json.Unmarshal(raw, &listed); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if len(listed.Instances) != registrations {
		names := make([]string, 0, len(listed.Instances))
		for _, entry := range listed.Instances {
			names = append(names, fmt.Sprint(entry["url"]))
		}
		t.Errorf("stored %d of %d registrations: %v; a concurrent write was lost",
			len(listed.Instances), registrations, names)
	}
}
