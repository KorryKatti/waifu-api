// Package instances holds the instance index: the list of other people's
// deployments of this software, and the probing that produces their signals.
//
// # What is measured, and what is not
//
// The index answers one question: "will this work if I point my client at it".
// Every signal here is answerable from the instance alone or absent. Nothing is
// scored into a single number, because a number hides its own weights and the
// person it is about is the one who would have to trust it.
//
// Signals split into two classes, and the split is enforced by the types rather
// than left to convention:
//
//   - Behavioural (Behavioural) — reachable, schema, images, builtAgeDays.
//     Populated for everyone, because every deployment can answer them.
//
//   - Genealogical (Genealogical) — upstream, commitAgeDays. Populated only when
//     the commit the instance reports resolves in this repository's history. A
//     fork has a commit that resolves nowhere, and there is no shared ancestry
//     against which "how far behind" could be measured. Absent means "we could
//     not tell", which is a different claim from "behind".
//
// Image count is deliberately absent. Partial instances are explicitly welcome,
// and counting images would penalise exactly what this feature is trying to
// encourage.
package instances

import (
	"sort"
	"time"
)

// SoftwareName is what a handshake names this project as.
//
// It lives here rather than beside the handler that emits it because this is the
// package that reads it back: one constant, so the value an instance reports and
// the value the index accepts cannot drift into two different strings that each
// look correct.
//
// It is deliberately the software rather than this repository. A fork or a clean
// reimplementation answers it too, which is what makes the check about
// compatibility rather than about provenance.
const SoftwareName = "waifu-im"

// CheckWindow is the trailing period the uptime figure is computed over.
//
// It matches the probing interval times a month of checks. A shorter window
// would describe this week; a longer one would average away a deployment that
// has been down for a fortnight.
const CheckWindow = 30 * 24 * time.Hour

// Record is one registered instance, exactly as it is persisted.
//
// It is a single JSON document inside the InstanceIndex row rather than a table
// row of its own. The whole index is read and written as one value, which is
// what lets it live in one JSONB cell: see repo/instances.go.
type Record struct {
	// URL is the instance's base URL, normalised. It is the identity — the
	// primary key of the index — because a name is free text and can change.
	URL string `json:"url"`

	// Name is a display label, free text, and may be empty.
	Name string `json:"name,omitempty"`

	// DiscordUserID is the operator who registered this, captured from the
	// interaction rather than typed by hand.
	//
	// It is the numeric account id, never a username: usernames are
	// mid-migration to globally-unique and still resolve to a different account
	// after a rename, so keying the role grant on one silently orphans
	// someone's role or hands it to someone else. It is never published on the
	// public endpoint.
	DiscordUserID string `json:"discordUserId,omitempty"`

	// RegisteredAt is when the entry was added.
	RegisteredAt time.Time `json:"registeredAt"`

	// VerifiedAt is when the handshake last passed at registration time.
	//
	// The re-check compares against this rather than against whatever answers
	// later, so an instance that was broken on the day it was added is recorded
	// as broken rather than quietly becoming healthy by being re-probed.
	VerifiedAt time.Time `json:"verifiedAt,omitempty"`

	// LastProbe is the most recent probe result, and nil when the entry has
	// never been probed. Absent is not the same as failed.
	LastProbe *Probe `json:"lastProbe,omitempty"`

	// Checks is the trailing history the uptime figure is computed from. It is
	// pruned to CheckWindow on every write, so it holds about a month.
	Checks []Check `json:"checks,omitempty"`
}

// Check is one day's outcome. A check passes when the instance served its own
// images: handshake, expected response shape, and a resolvable image URL.
type Check struct {
	At time.Time `json:"at"`
	OK bool      `json:"ok"`
}

// Probe is the outcome of probing one instance once.
//
// Every field is measured per probe rather than accumulated, so a stale figure
// is always visibly stale by its CheckedAt rather than looking current.
type Probe struct {
	// CheckedAt is when the probe ran.
	CheckedAt time.Time `json:"checkedAt"`

	// Behavioural. All three are always set: an unreachable instance is one
	// that failed, not one we could not ask.
	Reachable bool `json:"reachable"`
	Schema    bool `json:"schema"`
	Images    bool `json:"images"`

	// What the instance said about itself. Carried through verbatim and never
	// scored: a string an instance reports about itself is not evidence.
	Software string `json:"software,omitempty"`
	Version  string `json:"version,omitempty"`
	Commit   string `json:"commit,omitempty"`
	BuiltAt  string `json:"builtAt,omitempty"`

	// Genealogical. Both nil unless the commit resolved upstream, in which case
	// Upstream is true — the question "does this commit exist in this
	// repository" has no false case that means "behind".
	Upstream      *bool `json:"upstream,omitempty"`
	CommitAgeDays *int  `json:"commitAgeDays,omitempty"`

	// BuiltAgeDays is the behavioural staleness signal: how long the deployment
	// has been standing still, which is true regardless of whose code it runs.
	//
	// Nil when the instance reported no usable build timestamp. That is a real
	// state — a fork or an old build may not set it — and reporting 0 would be
	// indistinguishable from an instance built today.
	BuiltAgeDays *int `json:"builtAgeDays,omitempty"`

	// Error is why the probe failed, for the admin endpoint. It is not
	// published publicly: an error string is an invitation to probe further.
	Error string `json:"error,omitempty"`
}

// OK reports whether the instance served its own images on this probe.
//
// It is deliberately not "reachable". An instance that answers /version and
// then returns a shape no client can use is down as far as anyone pointing a
// client at it is concerned.
func (p *Probe) OK() bool {
	return p != nil && p.Reachable && p.Schema && p.Images
}

// Uptime is the fraction of the trailing window's checks that passed, or nil
// when there are no checks to divide by.
//
// Nil rather than 1.0 for an instance that has never been checked: nobody has
// observed it working, and reporting perfect uptime for it would be a claim
// nobody made.
func (r Record) Uptime() *float64 {
	if len(r.Checks) == 0 {
		return nil
	}

	passed := 0
	for _, check := range r.Checks {
		if check.OK {
			passed++
		}
	}

	value := float64(passed) / float64(len(r.Checks))
	return &value
}

// RecordCheck appends the probe outcome to the trailing history and prunes
// anything older than the window.
//
// It replaces the entry for the same day rather than appending a second one, so
// running the prober twice in a day does not make an instance look twice as
// reliable as one probed once.
func (r *Record) RecordCheck(at time.Time, ok bool) {
	day := at.UTC().Truncate(24 * time.Hour)
	cutoff := day.Add(-CheckWindow)

	kept := r.Checks[:0]
	for _, check := range r.Checks {
		// The entry for this day is replaced by the probe being recorded, so a
		// second run in one day cannot make an instance look twice as reliable
		// as one probed once. Compared for equality rather than "not after",
		// because every stored timestamp is day-truncated and this must not
		// discard the history that uptime is computed from.
		if check.At.Equal(day) {
			continue
		}
		if !check.At.After(cutoff) {
			continue
		}
		kept = append(kept, check)
	}
	r.Checks = append(kept, Check{At: day, OK: ok})

	sort.SliceStable(r.Checks, func(i, j int) bool {
		return r.Checks[i].At.Before(r.Checks[j].At)
	})
}

// Find returns the entry for a URL, or nil when the index does not hold it.
//
// The comparison is on the normalised form, so a caller holding
// "https://alt.example/" finds the entry registered as "https://alt.example"
// rather than adding a second one for the same instance.
func Find(records []Record, url string) *Record {
	for i := range records {
		if records[i].URL == url {
			return &records[i]
		}
	}
	return nil
}
