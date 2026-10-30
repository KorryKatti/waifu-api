package main

import (
	"testing"
	"time"
)

func TestAllowCooldown(t *testing.T) {
	state := newAppState()

	if _, ok := state.allow("user", "waifu", 10*time.Second); !ok {
		t.Fatal("first use should be allowed")
	}

	wait, ok := state.allow("user", "waifu", 10*time.Second)
	if ok {
		t.Fatal("second use should be cooled down")
	}
	if wait <= 0 {
		t.Fatalf("wait = %s, want positive", wait)
	}

	if _, ok := state.allow("other", "waifu", 10*time.Second); !ok {
		t.Fatal("a different user should be allowed")
	}
}

func TestPings(t *testing.T) {
	state := newAppState()
	state.replacePings(map[string]pingRecord{
		"https://one.example": {MS: 12.5, HasMS: true, Status: 200, At: time.Now()},
	})

	record, ok := state.getPing("https://one.example")
	if !ok || record.MS != 12.5 {
		t.Fatalf("record = %#v, ok = %v", record, ok)
	}
	if state.pingCount() != 1 {
		t.Fatalf("count = %d", state.pingCount())
	}

	snapshot := state.pingSnapshot()
	delete(snapshot, "https://one.example")
	if _, ok := state.getPing("https://one.example"); !ok {
		t.Fatal("changing the snapshot changed the state")
	}
}
