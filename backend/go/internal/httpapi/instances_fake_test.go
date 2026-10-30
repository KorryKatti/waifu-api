package httpapi_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"waifuapi/internal/instances"
)

// fakeProber stands in for outbound instance checks.
//
// The real prober makes HTTP requests, and the integration suite runs against a
// database in a CI container with no route to the instances it would be asked
// about. Every assertion about the *handling* of a probe result therefore has to
// be made against a result the test chooses, which is what this provides.
type fakeProber struct {
	result instances.Probe

	// mu guards seen, which the concurrency test writes from several request
	// goroutines at once. The real prober is stateless, so this lock has no
	// counterpart in production code — it is here because the fake is shared by
	// every request the harness serves, and an unguarded append would report a
	// race in the fake as a race in the handler.
	mu   sync.Mutex
	seen []string
}

func newFakeProber(result *instances.Probe) *fakeProber {
	fake := &fakeProber{}
	if result != nil {
		fake.result = *result
	} else {
		fake.result = workingProbe()
	}
	return fake
}

// workingProbe is an instance that answered everything correctly.
func workingProbe() instances.Probe {
	age := 3
	return instances.Probe{
		CheckedAt:     time.Now().UTC(),
		Reachable:     true,
		Schema:        true,
		Images:        true,
		Software:      "waifu-im",
		Version:       "7.12.0",
		Commit:        "9544404",
		BuiltAt:       time.Now().UTC().Add(-72 * time.Hour).Format(time.RFC3339),
		BuiltAgeDays:  &age,
		Upstream:      &[]bool{true}[0],
		CommitAgeDays: &[]int{2}[0],
	}
}

// unreachableProbe is an instance that did not answer.
func unreachableProbe(reason string) instances.Probe {
	return instances.Probe{
		CheckedAt: time.Now().UTC(),
		Error:     reason,
	}
}

// noUpstreamProbe is a working instance whose commit resolves to no history we
// hold, which is what a fork looks like.
func noUpstreamProbe() instances.Probe {
	probe := workingProbe()
	probe.Commit = "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef"
	probe.Upstream = nil
	probe.CommitAgeDays = nil
	return probe
}

// refusedProbe is an instance whose edge answers the prober with 403.
func refusedProbe() instances.Probe {
	return instances.Probe{
		CheckedAt: time.Now().UTC(),
		Refused:   true,
		Error:     "GET /images: 403 Forbidden",
	}
}

// noHandshakeProbe is an instance that serves its catalogue and images and does
// not implement GET /version — a deployment this repository did not write, and
// a perfectly usable one.
func noHandshakeProbe() instances.Probe {
	return instances.Probe{
		CheckedAt:   time.Now().UTC(),
		Schema:      true,
		Images:      true,
		NoHandshake: true,
		Error:       "GET /version: 404 Not Found",
	}
}

func (f *fakeProber) Probe(_ context.Context, baseURL string) instances.Probe {
	f.mu.Lock()
	f.seen = append(f.seen, baseURL)
	f.mu.Unlock()

	result := f.result
	// The timestamp is the real time rather than the fixture's, so an assertion
	// about staleness means what it says.
	result.CheckedAt = time.Now().UTC()
	return result
}

// probedURLs is a snapshot of what has been checked, for assertions.
func (f *fakeProber) probedURLs() []string {
	f.mu.Lock()
	defer f.mu.Unlock()

	return append([]string(nil), f.seen...)
}

// probed reports whether this URL was checked, which is how a test asserts that
// an endpoint probed *before* writing rather than trusting the input.
func (f *fakeProber) probed(url string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()

	for _, seen := range f.seen {
		if seen == url {
			return true
		}
	}
	return false
}

var _ instances.Prober = (*fakeProber)(nil)

// mustNormalise is Normalise for test setup, where a bad fixture is a bug in the
// test rather than an input to assert on.
func mustNormalise(t *testing.T, raw string) string {
	t.Helper()

	normalised, err := instances.Normalise(raw)
	if err != nil {
		t.Fatalf("Normalise(%q): %v", raw, err)
	}
	return normalised
}
