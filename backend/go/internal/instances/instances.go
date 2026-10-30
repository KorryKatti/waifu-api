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

	// Unverified marks an entry an administrator listed without requiring the
	// handshake.
	//
	// The handshake is this repository's invention: an instance that serves a
	// correct catalogue but does not implement it would otherwise be
	// unlistable, and its own working images are not what is in question.
	// Such an entry publishes everything that can be measured — catalogue and
	// images — and only its uptime is absent, because that figure is defined
	// over a check it cannot take part in. The daily pass clears the flag the
	// first time the instance answers the handshake.
	Unverified bool `json:"unverified,omitempty"`

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

	// NoImages records that the catalogue was readable and empty, so the image
	// check could not be made rather than failing.
	NoImages bool `json:"noImages,omitempty"`

	// NoHandshake records that the instance answered and does not implement
	// GET /version — a deployment of the original service, or anything else
	// built without this repository's contract.
	//
	// It is a contract mismatch rather than downtime: the checks that do not
	// depend on the handshake are still made, and the catalogue and image
	// signals are real observations about a working instance. What it cannot
	// earn is uptime, because a check here is defined over the handshake too,
	// and recording a failure every day would report a number nobody measured.
	NoHandshake bool `json:"noHandshake,omitempty"`

	// Refused records that the instance answered with an access refusal — 401,
	// 403 or 429 — rather than a result.
	//
	// This is the "not you" class of answer: a firewall, a bot rule or a rate
	// limit aimed at the prober's address. It is not evidence that the instance
	// is broken, because a client on another network may be served perfectly,
	// so whatever the refusal prevented stays unobserved rather than failed and
	// the index can say why instead of implying a measurement nobody made.
	Refused bool `json:"refused,omitempty"`

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
// It is what the uptime figure counts, and it is deliberately not "reachable":
// an instance that answers /version and then returns a shape no client can use
// is down as far as anyone pointing a client at it is concerned.
func (p *Probe) OK() bool {
	return p != nil && p.Reachable && p.Schema && p.Images
}

// NoImages records that the instance answered correctly but had an empty
// catalogue, so there was nothing to serve and nothing to fetch.
//
// It exists to separate "we asked and it failed" from "we never asked", which is
// the difference between counting a check and skipping it.
// Checkable reports whether this probe produced a meaningful pass or fail for
// the uptime history.
//
// Almost everything does, including an unreachable instance: a client pointed at
// a server that is not answering is experiencing downtime, and that is what the
// figure is supposed to report.
//
// The one exception is an empty catalogue, where there was nothing to serve. An
// instance with nothing uploaded is working exactly as configured, and recording
// a failure against it every day would report 0% uptime for the one thing it has
// no way to change. The same applies to an instance that does not implement the
// handshake: its uptime is unknown rather than bad, and its other signals are
// still recorded.
func (p *Probe) Checkable() bool {
	return p != nil && !p.NoImages && !p.NoHandshake && !p.Refused
}

// Compatible reports whether the instance speaks this software well enough to be
// worth listing.
//
// It is the registration gate, and it is weaker than OK on purpose. Gating on OK
// would refuse every instance that has not uploaded anything yet, which is the
// opposite of what this feature is for: partial instances are explicitly welcome,
// and a fresh deployment is the most partial one there is.
//
// The image check is therefore a *signal*, not a gate. An instance whose listing
// is readable but whose images 404 is still listed — with the image signal shown
// as failed, which is information a reader wants and which no amount of refusing
// to register would convey.
func (p *Probe) Compatible() bool {
	return p != nil && p.Reachable && p.Schema
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
