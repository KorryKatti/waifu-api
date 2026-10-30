package httpapi

import (
	"errors"
	"net/http"
	"time"

	"waifuapi/internal/apierror"
	"waifuapi/internal/config"
	"waifuapi/internal/instances"
)

// # The instance index
//
// A public list of other people's deployments of this software, so somebody
// deciding where to point a client can see who is running one and whether it
// works.
//
// # Two endpoints, not one with conditional fields
//
// GET /instances and GET /instances/admin return the same list, and the admin
// one adds the owner's Discord ID. They are separate routes rather than one
// route with a role-dependent shape on purpose: with a single endpoint the leak
// risk is "did the auth check fire on this path", which is not something you can
// see by reading the response type. Separate types mean the public type has no
// owner field in it at all and cannot leak one. This mirrors the split in
// reports_users.go, where the admin listing is a separate shape.
func (s *Server) registerInstanceRoutes(mux *http.ServeMux) {
	base := s.cfg.BasePathOrRoot()

	mux.HandleFunc("GET "+base+"instances", s.handleListInstances)
	mux.HandleFunc("GET "+base+"instances/admin", s.handleListInstancesAdmin)
	mux.HandleFunc("POST "+base+"instances", s.handleRegisterInstance)
	mux.HandleFunc("DELETE "+base+"instances", s.handleRemoveInstance)
}

// publicInstance is one entry on the public list.
//
// There is deliberately no field here that identifies who runs the instance, and
// no composite score. A number would hide its own weights; the components are
// published separately so an operator can check the uptime claim against their
// own experience.
//
// The genealogical fields are pointers and are absent unless the commit the
// instance reported resolves in this repository's history. A fork shares no
// ancestry with it, so "how far behind" has nothing to be measured against and
// the honest answer is absent rather than a number.
type publicInstance struct {
	URL  string `json:"url"`
	Name string `json:"name,omitempty"`

	// Behavioural.
	Uptime       *float64 `json:"uptime"`
	Schema       *bool    `json:"schema"`
	Images       *bool    `json:"images"`
	BuiltAgeDays *int     `json:"builtAgeDays"`

	// Genealogical. Absent unless the commit resolved upstream.
	Upstream      *bool `json:"upstream"`
	CommitAgeDays *int  `json:"commitAgeDays"`

	// What the instance says about itself. Carried for display and never
	// scored: a string an instance reports about itself is not evidence.
	Version string `json:"version,omitempty"`

	// Refused reports that the instance declined to answer the probe —
	// normally a firewall or bot rule aimed at the checker's address.
	//
	// It is separate from the signals themselves because it changes what they
	// mean: an instance that refuses the checker is unmeasured here, not
	// broken, and its catalogue and image figures are left absent rather than
	// published as failures.
	Refused bool `json:"refused,omitempty"`

	// When these figures were observed. Signals go stale — the prober runs
	// daily — and a stale list that did not say so would be worse than no list.
	LastCheckedAt *time.Time `json:"lastCheckedAt"`
}

// adminInstance is the same entry with the owner attached.
//
// It exists as its own type rather than as the public one plus a field, so the
// public response cannot grow an owner by accident.
type adminInstance struct {
	publicInstance
	DiscordUserID string     `json:"discordUserId,omitempty"`
	RegisteredAt  time.Time  `json:"registeredAt"`
	VerifiedAt    *time.Time `json:"verifiedAt,omitempty"`

	// Unverified marks an entry that was listed without a probe, so an
	// operator can tell "we have not checked this" from "we checked and it
	// failed". Admin-only, like the owner and the error detail: the public
	// type has no such field and cannot make the distinction for anyone else.
	Unverified bool `json:"unverified,omitempty"`

	// Error is why the last probe failed. Admin-only: an error string is an
	// invitation to probe the prober, and the address it would be probing is
	// this one.
	Error string `json:"error,omitempty"`
}

type instanceList struct {
	Instances []publicInstance `json:"instances"`
}

type adminInstanceList struct {
	Instances []adminInstance `json:"instances"`
}

// toPublic projects a record onto the public shape.
func toPublic(record instances.Record) publicInstance {
	entry := publicInstance{
		URL:  record.URL,
		Name: record.Name,
	}

	entry.Uptime = record.Uptime()
	entry.Upstream, entry.CommitAgeDays, entry.Version = nil, nil, ""

	if record.LastProbe != nil {
		probe := record.LastProbe
		checked := probe.CheckedAt

		entry.Schema = &probe.Schema
		entry.Images = &probe.Images
		if probe.Refused {
			entry.Refused = true
			// A refusal is not a failed check. Publishing false would claim we
			// saw a broken catalogue when what happened is that we were not
			// allowed to look.
			if !probe.Schema {
				entry.Schema = nil
			}
			if !probe.Images {
				entry.Images = nil
			}
		}
		entry.BuiltAgeDays = probe.BuiltAgeDays
		entry.Upstream = probe.Upstream
		entry.CommitAgeDays = probe.CommitAgeDays
		entry.Version = probe.Version
		entry.LastCheckedAt = &checked
	}

	return entry
}

// toAdmin adds the owner and the failure detail.
func toAdmin(record instances.Record) adminInstance {
	entry := adminInstance{
		publicInstance: toPublic(record),
		DiscordUserID:  record.DiscordUserID,
		RegisteredAt:   record.RegisteredAt,
		Unverified:     record.Unverified,
	}

	if !record.VerifiedAt.IsZero() {
		verified := record.VerifiedAt
		entry.VerifiedAt = &verified
	}
	if record.LastProbe != nil {
		entry.Error = record.LastProbe.Error
	}
	return entry
}

// handleListInstances serves the public list.
//
// Unauthenticated. The list is the reason the feature exists and there is
// nothing in it worth gating; requiring a token would make it unusable as the
// discovery page it is.
func (s *Server) handleListInstances(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := s.withTimeout(r)
	defer cancel()

	records, err := s.repo.GetInstances(ctx)
	if err != nil {
		internalServerError(w, r)
		return
	}

	// A nil slice would encode as null. An index nobody has registered with is
	// an empty list, not an absent one.
	entries := make([]publicInstance, 0, len(records))
	for _, record := range records {
		entries = append(entries, toPublic(record))
	}

	writeJSON(w, http.StatusOK, instanceList{Instances: entries})
}

// handleListInstancesAdmin serves the same list with the owner attached.
func (s *Server) handleListInstancesAdmin(w http.ResponseWriter, r *http.Request) {
	if !s.requireRole(w, r, config.RoleAdmin, "list the instance index") {
		return
	}

	ctx, cancel := s.withTimeout(r)
	defer cancel()

	records, err := s.repo.GetInstances(ctx)
	if err != nil {
		internalServerError(w, r)
		return
	}

	entries := make([]adminInstance, 0, len(records))
	for _, record := range records {
		entries = append(entries, toAdmin(record))
	}

	writeJSON(w, http.StatusOK, adminInstanceList{Instances: entries})
}

// registerInstanceRequest is the body of POST /instances.
type registerInstanceRequest struct {
	// URL is the instance's base URL. It is normalised before anything else
	// happens, because it is the entry's identity and two spellings of one
	// instance must not produce two entries.
	URL string `json:"url"`

	// Name is a display label. Optional, free text, and never used as a key:
	// people rename things and usernames change.
	Name string `json:"name,omitempty"`

	// DiscordUserID is supplied by the bot from the interaction, not typed by
	// the person running the command. It is the numeric account id rather than
	// a username, because usernames still resolve to a different account after
	// a rename — which would silently hand somebody else's role to whoever
	// took the name.
	DiscordUserID string `json:"discordUserId"`

	// Unverified lists the instance without requiring the handshake.
	//
	// The probe still runs: an instance that does not implement GET /version
	// can serve its whole catalogue, and those observations are published as
	// usual. What it cannot earn is uptime, which is defined over a check it
	// cannot take part in. This exists because the handshake is this
	// repository's invention — an instance is not worse for not having heard
	// of it. Registration stays admin-gated, which is what keeps this from
	// becoming any URL at all.
	Unverified bool `json:"unverified,omitempty"`
}

// handleRegisterInstance adds an entry, having probed it first.
//
// The probe is not optional. An index whose entries are whatever was typed into
// it is a list of links, and a link that 404s is the one thing a reader of the
// list is trying to avoid. What the unverified flag relaxes is only the
// handshake requirement: an instance that serves a correct catalogue without
// implementing GET /version is still measured and listed, with its uptime absent
// because that figure is defined over the handshake.
//
// Re-registering an existing URL is not an error and does not overwrite: the
// stored entry, its owner and its history are all left alone, and the caller is
// told it already existed. Overwriting would reset the check history, which
// manufactures a week of perfect uptime for an instance that had been failing.
func (s *Server) handleRegisterInstance(w http.ResponseWriter, r *http.Request) {
	if !s.requireRole(w, r, config.RoleAdmin, "register an instance") {
		return
	}

	var body registerInstanceRequest
	if err := decodeJSONBody(r, &body); err != nil {
		if apiErr, ok := err.(*apierror.Error); ok {
			apiErr.Write(w, r.URL.Path)
			return
		}
		apierror.BadRequest("Request body is not valid JSON.").Write(w, r.URL.Path)
		return
	}

	normalised, err := instances.Normalise(body.URL)
	if err != nil {
		apierror.BadRequest(err.Error()).Write(w, r.URL.Path)
		return
	}

	// Registering somebody else's instance is prevented by the bot, which sends
	// the invoker's own id. The backend records what it is told rather than
	// inventing a second source of identity: it cannot know who typed the
	// command, and guessing would make the stored owner a claim rather than a
	// fact.
	if err := validateDiscordUserID(body.DiscordUserID); err != nil {
		apierror.BadRequest(err.Error()).Write(w, r.URL.Path)
		return
	}

	ctx, cancel := s.withTimeout(r)
	defer cancel()

	existing, err := s.repo.GetInstances(ctx)
	if err != nil {
		internalServerError(w, r)
		return
	}

	// Already registered: report the stored entry and stop. This is a success,
	// not a conflict — running the command twice is the obvious way to lose the
	// argument described above.
	if found := instances.Find(existing, normalised); found != nil {
		writeJSON(w, http.StatusOK, adminInstance{publicInstance: toPublic(*found),
			DiscordUserID: found.DiscordUserID,
			RegisteredAt:  found.RegisteredAt,
			Unverified:    found.Unverified,
			Error:         "this instance is already registered; the stored entry was left unchanged"})
		return
	}

	now := time.Now().UTC()

	// One probe, always. An instance listed without the handshake still has a
	// catalogue and images worth measuring, and those are the signals a reader
	// uses; the flag relaxes the gate, not the measurement. See
	// instances.Probe.NoHandshake.
	probe := s.prober.Probe(ctx, normalised)
	compatible := probe.Compatible()
	if !compatible && !body.Unverified {
		detail := probe.Error
		if detail == "" {
			detail = "the instance did not pass its compatibility checks"
		}
		apierror.UnprocessableEntity("That instance did not answer correctly: "+detail).Write(w, r.URL.Path)
		return
	}

	record := instances.Record{
		URL:           normalised,
		Name:          body.Name,
		DiscordUserID: body.DiscordUserID,
		RegisteredAt:  now,
		LastProbe:     &probe,
		// Verified means it answered the handshake. Asking to be listed
		// unverified only lowers the bar for admission; an instance that does
		// answer is stored as verified whatever the caller asked for.
		Unverified: !compatible,
	}
	if compatible {
		record.VerifiedAt = now
	}
	// Only counted when the probe could actually observe something. See
	// instances.Probe.Checkable.
	if probe.Checkable() {
		record.RecordCheck(now, probe.OK())
	}

	stored, added, err := s.repo.RegisterInstance(ctx, record)
	if err != nil {
		internalServerError(w, r)
		return
	}

	// Two admins registering the same instance at once both pass the probe and
	// both call RegisterInstance; only one of them added the row, and the other
	// is told so rather than being told it created something.
	if !added {
		writeJSON(w, http.StatusOK, toAdmin(stored))
		return
	}

	// 201 without a Location header: the resource has no address of its own. It
	// is addressed by its URL in the collection, which is the identity it was
	// normalised to.
	writeJSON(w, http.StatusCreated, toAdmin(stored))
}

// handleRemoveInstance takes an entry off the list.
//
// It exists because registration is one-way otherwise: a URL registered in
// error, or an instance an operator has taken down, would stay on the list
// reporting itself as healthy until it was manually edited out of a JSON
// document.
func (s *Server) handleRemoveInstance(w http.ResponseWriter, r *http.Request) {
	if !s.requireRole(w, r, config.RoleAdmin, "remove an instance") {
		return
	}

	query := r.URL.Query()
	requested := queryParam(query, "url")
	if requested == "" {
		apierror.BadRequest("The url query parameter is required.").Write(w, r.URL.Path)
		return
	}

	normalised, err := instances.Normalise(requested)
	if err != nil {
		apierror.BadRequest(err.Error()).Write(w, r.URL.Path)
		return
	}

	ctx, cancel := s.withTimeout(r)
	defer cancel()

	removed, err := s.repo.RemoveInstance(ctx, normalised)
	if err != nil {
		internalServerError(w, r)
		return
	}
	if !removed {
		apierror.NotFound("No such instance is registered.").Write(w, r.URL.Path)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// validateDiscordUserID checks that an owner id is the numeric account id
// Discord issues.
//
// An empty value is allowed: the entry still belongs on the list, it just cannot
// be granted a role. Anything else must be all digits, because the role grant
// is keyed on this string and a username in it would resolve to whoever holds
// that name now rather than whoever ran the command.
func validateDiscordUserID(id string) error {
	if id == "" {
		return nil
	}
	for _, r := range id {
		if r < '0' || r > '9' {
			return errors.New("discordUserId must be the numeric Discord account id, not a username. " +
				"Usernames change and can be given up, so a role keyed on one would be handed to somebody else after a rename")
		}
	}
	return nil
}
