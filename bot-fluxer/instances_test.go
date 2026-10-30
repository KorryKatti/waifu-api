package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestInstanceProbeURL(t *testing.T) {
	cases := []struct {
		raw  string
		want string
		ok   bool
	}{
		{"https://example.com/api/?x=1#y", "https://example.com/api", true},
		{"https://example.com/", "https://example.com", true},
		{"http://example.com:8080/base", "http://example.com:8080/base", true},
		{"ftp://example.com", "", false},
		{"https://user:pass@example.com", "", false},
		{"example.com", "", false},
		{"", "", false},
	}

	for _, item := range cases {
		got, ok := instanceProbeURL(item.raw)
		if ok != item.ok || got != item.want {
			t.Errorf("instanceProbeURL(%q) = %q, %v; want %q, %v", item.raw, got, ok, item.want, item.ok)
		}
	}
}

func TestMeasureOne(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/images" || r.URL.Query().Get("pageSize") != "1" {
			t.Errorf("path = %q?%s, want the catalogue check", r.URL.Path, r.URL.RawQuery)
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	record := measureOne(server.URL)
	if !record.HasMS {
		t.Fatalf("record = %#v, want a measured round trip", record)
	}
	if record.Status != http.StatusNotFound {
		t.Errorf("status = %d", record.Status)
	}
}

func TestMeasureEntries(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"software":"waifu-api"}`))
	}))
	defer server.Close()

	app := &botApp{
		cfg:   &config{instancePingEvery: time.Hour},
		state: newAppState(),
	}

	entries := []map[string]any{
		{"url": server.URL},
		{"url": "https://user:pass@example.com"},
		{"url": server.URL},
	}

	if got := app.measureEntries(entries); got != 1 {
		t.Fatalf("measured = %d, want 1", got)
	}
	if app.state.pingCount() != 1 {
		t.Fatalf("pings = %d, want 1", app.state.pingCount())
	}
}

func TestDescribePing(t *testing.T) {
	now := time.Now()
	app := &botApp{
		cfg:   &config{instancePingEvery: time.Hour},
		state: newAppState(),
	}
	app.state.replacePings(map[string]pingRecord{
		"ok":    {MS: 42.4, HasMS: true, Status: 200, At: now},
		"bad":   {MS: 15, HasMS: true, Status: 404, At: now},
		"down":  {Err: "timeout", At: now},
		"stale": {MS: 10, HasMS: true, Status: 200, At: now.Add(-3 * time.Hour)},
	})

	cases := map[string]string{
		"ok":      "42 ms",
		"bad":     "15 ms (HTTP 404)",
		"down":    "no answer in 3s",
		"stale":   "10 ms, 3 h old",
		"missing": "not measured yet",
	}
	for url, want := range cases {
		if got := app.describePing(url, now); got != want {
			t.Errorf("describePing(%q) = %q, want %q", url, got, want)
		}
	}
}
