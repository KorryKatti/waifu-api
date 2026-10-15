package background

import (
	"context"
	"testing"
	"time"

	"waifuapi/internal/instances"
)

// fakeInstanceProber answers without leaving the process.
//
// These jobs make outbound HTTP requests, which a test in this package cannot
// do: the suite runs in CI with no route to whatever an instance would be, and a
// test that reached the internet would be testing the internet.
type fakeInstanceProber struct {
	results map[string]instances.Probe
	probed  []string
}

func (f *fakeInstanceProber) Probe(_ context.Context, baseURL string) instances.Probe {
	f.probed = append(f.probed, baseURL)

	if result, ok := f.results[baseURL]; ok {
		return result
	}
	return instances.Probe{CheckedAt: time.Now().UTC(), Error: "not configured"}
}

func (f *fakeInstanceProber) ResolveUpstream(probe *instances.Probe, _ time.Time) {
	if probe == nil || probe.Commit == "" {
		return
	}
	if resolved, age := instances.ResolveCommit(probe.Commit, time.Now().UTC()); resolved {
		upstream := true
		probe.Upstream = &upstream
		probe.CommitAgeDays = &age
	}
}

func healthyAt(at time.Time) instances.Probe {
	days := 2
	return instances.Probe{
		CheckedAt:    at,
		Reachable:    true,
		Schema:       true,
		Images:       true,
		Software:     instances.SoftwareName,
		Version:      "7.12.0",
		BuiltAgeDays: &days,
	}
}

// recordingInstanceLog captures what the job says.
type recordingInstanceLog struct {
	infos []string
}

func (l *recordingInstanceLog) Info(msg string, args ...any) {
	l.infos = append(l.infos, msg)
}

func (l *recordingInstanceLog) Error(string, ...any) {}

func (l *recordingInstanceLog) count(msg string) int {
	total := 0
	for _, line := range l.infos {
		if line == msg {
			total++
		}
	}
	return total
}

// TestInstanceProbeRecordsSignals covers the daily pass: every entry is probed,
// and the outcome is written back rather than merely logged.
func TestInstanceProbeRecordsSignals(t *testing.T) {
	data, cleanup := instanceTestRepo(t)
	defer cleanup()

	now := time.Now().UTC()
	seedInstanceRecord(t, data, instances.Record{
		URL: "https://alt.example", Name: "alt", DiscordUserID: "123456789",
		RegisteredAt: now.Add(-72 * time.Hour),
	})

	prober := &fakeInstanceProber{results: map[string]instances.Probe{
		"https://alt.example": healthyAt(now),
	}}
	log := &recordingInstanceLog{}

	job := InstanceProbe(data, prober, log)
	if job.Name == "" {
		t.Error("the job has no name; an operator reading logs could not tell which job failed")
	}

	if err := job.Run(t.Context()); err != nil {
		t.Fatalf("run: %v", err)
	}

	if len(prober.probed) != 1 || prober.probed[0] != "https://alt.example" {
		t.Errorf("probed %v, want the registered instance", prober.probed)
	}

	stored := readInstance(t, data, "https://alt.example")
	if stored.LastProbe == nil {
		t.Fatal("no probe result was stored")
	}
	if !stored.LastProbe.OK() {
		t.Errorf("the stored probe is not OK: %+v", stored.LastProbe)
	}
	if len(stored.Checks) != 1 || !stored.Checks[0].OK {
		t.Errorf("checks = %+v, want one passing entry", stored.Checks)
	}
	if log.count("instance index checked") != 1 {
		t.Error("the pass did not log its summary")
	}
}

// TestInstanceProbeNeverDeletes is the behaviour that keeps the list
// trustworthy.
//
// A Render free-tier instance sleeps for hours. Deleting on failure would mean a
// list that loses entries overnight and gets them back in the morning, which
// teaches readers to distrust the list rather than to notice an instance is
// down.
func TestInstanceProbeNeverDeletes(t *testing.T) {
	data, cleanup := instanceTestRepo(t)
	defer cleanup()

	now := time.Now().UTC()
	seedInstanceRecord(t, data, instances.Record{
		URL: "https://alt.example", RegisteredAt: now.Add(-72 * time.Hour),
	})

	// Reachable and well-formed, but its image does not resolve: the failure
	// mode that must count against uptime.
	broken := healthyAt(now)
	broken.Images = false
	broken.Error = "HEAD https://cdn.example/1.webp: 404"

	prober := &fakeInstanceProber{results: map[string]instances.Probe{
		"https://alt.example": broken,
	}}

	job := InstanceProbe(data, prober, &recordingInstanceLog{})
	if err := job.Run(t.Context()); err != nil {
		t.Fatalf("a failing probe must not fail the job: %v", err)
	}

	stored := readInstance(t, data, "https://alt.example")
	if stored.LastProbe == nil {
		t.Fatal("the failure was not recorded")
	}
	if stored.LastProbe.OK() {
		t.Error("the stored probe claims to be OK")
	}
	if stored.LastProbe.Error == "" {
		t.Error("no reason recorded; a drop has to be explainable")
	}
	if len(stored.Checks) != 1 || stored.Checks[0].OK {
		t.Errorf("checks = %+v, want one failing entry", stored.Checks)
	}
	if log := (&recordingInstanceLog{}); log.count("instance index checked") != 0 {
		t.Error("a fresh logger is not shared state")
	}
}

// TestInstanceProbeSkipsAnEmptyCatalogue is the case that must not count.
//
// An instance with nothing uploaded is working exactly as configured. Recording
// a failure against it every day would report 0% uptime for the one thing it has
// no way to change, and would do it forever.
func TestInstanceProbeSkipsAnEmptyCatalogue(t *testing.T) {
	data, cleanup := instanceTestRepo(t)
	defer cleanup()

	now := time.Now().UTC()
	seedInstanceRecord(t, data, instances.Record{URL: "https://alt.example", RegisteredAt: now})

	empty := healthyAt(now)
	empty.Images = false
	empty.NoImages = true
	empty.Error = "the instance returned no images to check"

	prober := &fakeInstanceProber{results: map[string]instances.Probe{
		"https://alt.example": empty,
	}}

	if err := InstanceProbe(data, prober, &recordingInstanceLog{}).Run(t.Context()); err != nil {
		t.Fatalf("run: %v", err)
	}

	stored := readInstance(t, data, "https://alt.example")
	if stored.LastProbe == nil || stored.LastProbe.Error == "" {
		t.Error("the outcome must still be recorded; it is what makes the signal explainable")
	}
	if len(stored.Checks) != 0 {
		t.Errorf("checks = %+v, want none: nothing could be observed", stored.Checks)
	}
	if uptime := stored.Uptime(); uptime != nil {
		t.Errorf("Uptime() = %v, want nil when no check ever ran", *uptime)
	}
}

// TestInstanceProbeCountsAnUnreachableInstance is the other half.
//
// Down is observable, and it is what the uptime figure exists to report. Only the
// empty catalogue is exempt, because nothing about that instance was wrong.
func TestInstanceProbeCountsAnUnreachableInstance(t *testing.T) {
	data, cleanup := instanceTestRepo(t)
	defer cleanup()

	now := time.Now().UTC()
	seedInstanceRecord(t, data, instances.Record{URL: "https://alt.example", RegisteredAt: now})

	prober := &fakeInstanceProber{results: map[string]instances.Probe{
		"https://alt.example": {CheckedAt: now, Error: "dial tcp: connection refused"},
	}}

	if err := InstanceProbe(data, prober, &recordingInstanceLog{}).Run(t.Context()); err != nil {
		t.Fatalf("run: %v", err)
	}

	stored := readInstance(t, data, "https://alt.example")
	if len(stored.Checks) != 1 || stored.Checks[0].OK {
		t.Errorf("checks = %+v, want one failing entry: downtime is a real observation", stored.Checks)
	}
	if uptime := stored.Uptime(); uptime == nil || *uptime != 0 {
		t.Errorf("Uptime() = %v, want 0 for a single failed check", uptime)
	}
}

// TestInstanceProbeContinuesPastAFailure keeps one instance's broken row from
// costing every other instance its daily check.
func TestInstanceProbeContinuesPastAFailure(t *testing.T) {
	data, cleanup := instanceTestRepo(t)
	defer cleanup()

	now := time.Now().UTC()
	seedInstanceRecord(t, data, instances.Record{URL: "https://a.example", RegisteredAt: now})
	seedInstanceRecord(t, data, instances.Record{URL: "https://b.example", RegisteredAt: now})

	// Both answer the same; the point is that a probe returning a failure result
	// for one does not abort the loop.
	prober := &fakeInstanceProber{results: map[string]instances.Probe{
		"https://a.example": healthyAt(now),
		"https://b.example": {CheckedAt: now, Error: "no answer"},
	}}

	job := InstanceProbe(data, prober, &recordingInstanceLog{})
	if err := job.Run(t.Context()); err != nil {
		t.Fatalf("run: %v", err)
	}

	if len(prober.probed) != 2 {
		t.Errorf("probed %v, want both instances", prober.probed)
	}
	if readInstance(t, data, "https://a.example").LastProbe == nil {
		t.Error("the healthy instance's result was lost")
	}
	if readInstance(t, data, "https://b.example").LastProbe == nil {
		t.Error("the failing instance's result was lost")
	}
}

// TestInstanceProbeOnAnEmptyIndex is a no-op rather than an error.
//
// There is no row in InstanceIndex until something registers, so this is the
// ordinary state of a deployment that has never been registered with, and
// logging it as a failure every day would be noise that hides a real one.
func TestInstanceProbeOnAnEmptyIndex(t *testing.T) {
	data, cleanup := instanceTestRepo(t)
	defer cleanup()

	job := InstanceProbe(data, &fakeInstanceProber{}, &recordingInstanceLog{})
	if err := job.Run(t.Context()); err != nil {
		t.Errorf("an empty index failed the job: %v", err)
	}
}

// TestInstanceProbeResolvesUpstream pins that genealogy is decided here and
// nowhere else.
func TestInstanceProbeResolvesUpstream(t *testing.T) {
	data, cleanup := instanceTestRepo(t)
	defer cleanup()

	now := time.Now().UTC()
	seedInstanceRecord(t, data, instances.Record{URL: "https://alt.example", RegisteredAt: now})

	probe := healthyAt(now)
	probe.Commit = "deadbeefdeadbeefdeadbeef"

	prober := &fakeInstanceProber{results: map[string]instances.Probe{
		"https://alt.example": probe,
	}}

	if err := InstanceProbe(data, prober, &recordingInstanceLog{}).Run(t.Context()); err != nil {
		t.Fatalf("run: %v", err)
	}

	stored := readInstance(t, data, "https://alt.example")
	if stored.LastProbe == nil {
		t.Fatal("no probe result was stored")
	}
	if stored.LastProbe.Upstream != nil || stored.LastProbe.CommitAgeDays != nil {
		t.Errorf("a commit that resolves nowhere was given a value: upstream=%v age=%v",
			stored.LastProbe.Upstream, stored.LastProbe.CommitAgeDays)
	}
}
