package background

import (
	"context"
	"errors"
	"time"

	"waifuapi/internal/instances"
	"waifuapi/internal/repo"
)

// InstanceProbe re-checks every registered instance once a day.
//
// # What a pass does, and what it deliberately does not
//
// One probe per instance: a handshake, a one-item image listing, and a HEAD on
// the image that listing names. It does not walk the catalogue, count images, or
// compare content, because the only question being answered is "will this work
// if I point a client at it".
//
// # Why a failure never deletes an entry
//
// A Render free-tier instance sleeps for hours, and a sleeping instance is a
// working instance with an idle one. Deleting on failure would mean a list that
// loses entries overnight and gets them back in the morning, which teaches
// readers that the list is unreliable rather than that a particular instance is
// down. A failed check is recorded as a failed check and the entry stays.
//
// # Why an unverified entry has no uptime
//
// An entry an administrator listed without the handshake (instances.Record
// .Unverified) may not implement GET /version at all while serving its
// catalogue perfectly. Its probe is stored and its catalogue and image signals
// are published like any other entry's, but it records no check: uptime here is
// defined over a check that includes the handshake, and writing a failure every
// day would report a 0% nobody measured. It is probed daily like everything
// else and stops being unverified the first time it answers.
//
// # Why this is the only place a commit is resolved
//
// Resolving provenance is the one operation that needs this repository's history
// rather than the instance, and keeping it here means the registration path has
// no dependency on the bundle at all. A freshly registered instance is already
// known to answer correctly; whether its commit is one of ours is a separate
// question with a separate answer, and it may legitimately be "we could not
// tell" for a while.

// InstanceProber is the subset of instances.HTTPProber this job needs.
//
// It is an interface so the job can be tested without network access, which
// matters more here than for the cleanup jobs: these make outbound requests, and
// a test that reached the network would be testing the internet.
type InstanceProber interface {
	Probe(ctx context.Context, baseURL string) instances.Probe
	ResolveUpstream(probe *instances.Probe, now time.Time)
}

// InstanceProbe builds the daily instance-check job.
func InstanceProbe(data *repo.Repo, prober InstanceProber, log Logger) Job {
	return Job{
		Name: "instance-probe",
		Run: func(ctx context.Context) error {
			records, err := data.GetInstances(ctx)
			if err != nil {
				return err
			}
			if len(records) == 0 {
				return nil
			}

			now := time.Now().UTC()
			healthy, degraded, unverified := 0, 0, 0

			for _, record := range records {
				probe := prober.Probe(ctx, record.URL)
				prober.ResolveUpstream(&probe, now)

				// A run that could not observe anything is not counted, rather
				// than counted as a failure: an instance with an empty catalogue
				// would otherwise record one every day and report 0% uptime for
				// the one thing it has no way to change. An instance without the
				// handshake is the same case — its catalogue and images are
				// still recorded below, but the uptime figure is defined over a
				// check it cannot take part in.
				stored := record
				if probe.Checkable() {
					stored.RecordCheck(now, probe.OK())
				}

				// An entry listed without the handshake stops being unverified
				// the day it answers one.
				promote := record.Unverified && probe.Compatible()

				if err := data.UpdateInstance(ctx, record.URL, func(entry *instances.Record) {
					if promote {
						entry.Unverified = false
						entry.VerifiedAt = now
					}
					entry.LastProbe = &probe
					entry.Checks = stored.Checks
				}); err != nil {
					// The entry may have been removed while this probe was in
					// flight, which is not a failure worth retrying. Anything
					// else is, and the pass continues: one instance's row must
					// not cost every other instance its daily check.
					if errors.Is(err, repo.ErrInstanceNotFound) {
						continue
					}
					return err
				}

				switch {
				case probe.OK():
					healthy++
				case probe.NoHandshake:
					// Answering, catalogue readable, just not on this contract:
					// reported apart from the failures so a working instance
					// does not read as downtime.
					unverified++
					log.Info("instance check without handshake",
						"url", record.URL,
						"schema", probe.Schema,
						"images", probe.Images)
				default:
					degraded++
					log.Info("instance check failed",
						"url", record.URL,
						"reason", probe.Error)
				}
			}

			log.Info("instance index checked",
				"instances", len(records),
				"healthy", healthy,
				"degraded", degraded,
				"unverified", unverified)
			return nil
		},
	}
}
