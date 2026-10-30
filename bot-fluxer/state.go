package main

import (
	"sync"
	"time"
)

type statsSnapshot struct {
	Latency     time.Duration
	LatencyAt   time.Time
	Images      int
	Tags        int
	Artists     int
	Served      int
	Errors      int
	LastErrorAt time.Time
}

type pingRecord struct {
	MS     float64
	HasMS  bool
	Status int
	Err    string
	At     time.Time
}

type appState struct {
	mu          sync.Mutex
	started     time.Time
	latency     time.Duration
	latencyAt   time.Time
	images      int
	tags        int
	artists     int
	served      int
	errors      int
	lastErrorAt time.Time
	cooldowns   map[string]time.Time
	pings       map[string]pingRecord
}

func newAppState() *appState {
	return &appState{
		started:   time.Now(),
		cooldowns: make(map[string]time.Time),
		pings:     make(map[string]pingRecord),
	}
}

func (s *appState) noteOK(latency time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.latency = latency
	s.latencyAt = time.Now()
}

func (s *appState) noteError() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.errors++
	s.lastErrorAt = time.Now()
}

func (s *appState) noteServed() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.served++
}

func (s *appState) setCatalogue(images, tags, artists int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.images = images
	s.tags = tags
	s.artists = artists
}

func (s *appState) snapshot() statsSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	return statsSnapshot{
		Latency:     s.latency,
		LatencyAt:   s.latencyAt,
		Images:      s.images,
		Tags:        s.tags,
		Artists:     s.artists,
		Served:      s.served,
		Errors:      s.errors,
		LastErrorAt: s.lastErrorAt,
	}
}

func (s *appState) freshLatency() (time.Duration, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.latencyAt.IsZero() || time.Since(s.latencyAt) > 15*time.Minute {
		return 0, false
	}
	return s.latency, true
}

func (s *appState) latencyAge() (time.Duration, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.latencyAt.IsZero() {
		return 0, false
	}
	return time.Since(s.latencyAt), true
}

func (s *appState) allow(userID, command string, window time.Duration) (time.Duration, bool) {
	key := command + ":" + userID
	now := time.Now()

	s.mu.Lock()
	defer s.mu.Unlock()

	if last, ok := s.cooldowns[key]; ok {
		if wait := window - now.Sub(last); wait > 0 {
			return wait, false
		}
	}

	s.cooldowns[key] = now
	if len(s.cooldowns) > 10000 {
		for name, used := range s.cooldowns {
			if now.Sub(used) > time.Hour {
				delete(s.cooldowns, name)
			}
		}
	}
	return 0, true
}

func (s *appState) replacePings(pings map[string]pingRecord) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pings = pings
}

func (s *appState) pingSnapshot() map[string]pingRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	copyOfPings := make(map[string]pingRecord, len(s.pings))
	for url, record := range s.pings {
		copyOfPings[url] = record
	}
	return copyOfPings
}

func (s *appState) getPing(url string) (pingRecord, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	record, ok := s.pings[url]
	return record, ok
}

func (s *appState) pingCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.pings)
}
