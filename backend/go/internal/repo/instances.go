package repo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"

	"waifuapi/internal/instances"
)

// This file holds the instance index queries.
//
// # Why one row
//
// The whole index is a single JSONB array in one row of InstanceIndex, rather
// than a table of instances. The list is read whole and written whole, it is
// small enough that nobody will page it, and there is no query that wants to
// filter it. A table would buy indexes nothing here uses and add a second place
// for the schema to drift from what production has.
//
// The cost is that a read-modify-write is only atomic if it is wrapped in a
// transaction that locks the row. Update below does that; a plain SELECT
// followed by a plain UPDATE would lose a concurrent registration, and losing one
// silently is exactly the failure mode a public list must not have.

// InstanceIndexID is the one row the index lives in.
//
// The table carries a CHECK constraint forcing this value, so a second row is
// rejected by the database rather than by convention.
const InstanceIndexID = 1

// GetInstances returns the registered instances, newest registration last.
//
// A missing row is an empty index rather than an error: the table is created
// empty and nothing has to seed it, so "never registered" and "registered
// nothing" are the same state and both are served as an empty list.
func (r *Repo) GetInstances(ctx context.Context) ([]instances.Record, error) {
	var raw []byte
	err := r.db.QueryRow(ctx, `
        SELECT "Instances"
        FROM "InstanceIndex"
        WHERE "Id" = $1`, InstanceIndexID).Scan(&raw)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("get instances: %w", err)
	}

	records, err := decodeInstances(raw)
	if err != nil {
		return nil, err
	}

	sortInstances(records)
	return records, nil
}

// UpdateInstances replaces the index inside a transaction that holds a row lock
// for its duration.
//
// The lock is what makes read-modify-write safe. Two registrations arriving at
// once would otherwise both read the same list and the second write would
// discard the first, and nothing would report it: the operator would see the
// command succeed and the instance would simply not appear.
//
// The caller has already read the list through GetInstances, so this re-reads it
// under the lock rather than trusting that copy — a caller that read before
// another writer committed would otherwise still lose the newer entry.
func (r *Repo) UpdateInstances(ctx context.Context, mutate func([]instances.Record) ([]instances.Record, error)) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin instance update: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// FOR UPDATE serialises concurrent writers on this row. The read inside the
	// transaction is what the mutation sees.
	var raw []byte
	err = tx.QueryRow(ctx, `
        SELECT "Instances"
        FROM "InstanceIndex"
        WHERE "Id" = $1
        FOR UPDATE`, InstanceIndexID).Scan(&raw)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("lock instance index: %w", err)
	}

	current, err := decodeInstances(raw)
	if err != nil {
		return err
	}

	updated, err := mutate(current)
	if err != nil {
		return err
	}

	sortInstances(updated)

	encoded, err := json.Marshal(updated)
	if err != nil {
		return fmt.Errorf("encode instances: %w", err)
	}

	// The row is created here rather than seeded in the schema, so applying the
	// table to production is a single statement with nothing else to run after
	// it.
	if _, err := tx.Exec(ctx, `
        INSERT INTO "InstanceIndex" ("Id", "Instances", "UpdatedAt")
        VALUES ($1, $2, now())
        ON CONFLICT ("Id") DO UPDATE
            SET "Instances" = EXCLUDED."Instances",
                "UpdatedAt" = EXCLUDED."UpdatedAt"`,
		InstanceIndexID, encoded); err != nil {
		return fmt.Errorf("save instances: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit instance update: %w", err)
	}
	return nil
}

// RegisterInstance adds an entry, or returns the existing one when the URL is
// already registered.
//
// Re-registering is not an update. Somebody running /register twice has not
// changed their mind about their instance, and overwriting the entry would reset
// their check history — which would silently manufacture a week of perfect
// uptime for an instance that had been failing.
func (r *Repo) RegisterInstance(ctx context.Context, record instances.Record) (instances.Record, bool, error) {
	var stored instances.Record
	var added bool

	err := r.UpdateInstances(ctx, func(current []instances.Record) ([]instances.Record, error) {
		if existing := instances.Find(current, record.URL); existing != nil {
			stored = *existing
			return current, nil
		}

		if record.RegisteredAt.IsZero() {
			record.RegisteredAt = time.Now().UTC()
		}
		stored = record
		added = true
		return append(current, record), nil
	})
	if err != nil {
		return instances.Record{}, false, err
	}

	return stored, added, nil
}

// UpdateInstance applies a change to one entry, reporting whether it existed.
//
// A mutation for an entry that has been removed returns the sentinel
// ErrInstanceNotFound rather than re-creating it, because the prober reads the
// list, takes its time probing, and writes back afterwards: recreating a deleted
// instance on write-back is how a removal turns into a resurrection.
func (r *Repo) UpdateInstance(ctx context.Context, url string, mutate func(*instances.Record)) error {
	return r.UpdateInstances(ctx, func(current []instances.Record) ([]instances.Record, error) {
		existing := instances.Find(current, url)
		if existing == nil {
			return nil, ErrInstanceNotFound
		}
		mutate(existing)
		return current, nil
	})
}

// RemoveInstance deletes an entry, reporting whether it was there.
func (r *Repo) RemoveInstance(ctx context.Context, url string) (bool, error) {
	removed := false

	err := r.UpdateInstances(ctx, func(current []instances.Record) ([]instances.Record, error) {
		kept := make([]instances.Record, 0, len(current))
		for _, record := range current {
			if record.URL == url {
				removed = true
				continue
			}
			kept = append(kept, record)
		}
		return kept, nil
	})
	if err != nil {
		return false, err
	}
	return removed, nil
}

// ErrInstanceNotFound is returned when a mutation targets an entry that is not
// in the index.
var ErrInstanceNotFound = errors.New("instance is not in the index")

// decodeInstances parses the stored array.
//
// A row whose JSON is unreadable is an error rather than an empty list. Returning
// nil would publish an empty index, and an empty index looks like a working
// service that nobody has registered with — the opposite of what has happened.
func decodeInstances(raw []byte) ([]instances.Record, error) {
	if len(raw) == 0 {
		return nil, nil
	}

	var records []instances.Record
	if err := json.Unmarshal(raw, &records); err != nil {
		return nil, fmt.Errorf("the stored instance index is unreadable: %w", err)
	}
	return records, nil
}

// sortInstances orders the list by registration time.
//
// It is applied on every read and every write so the order does not depend on
// which code path produced the row, and so a concurrent registration cannot
// reorder a list someone is reading.
func sortInstances(records []instances.Record) {
	sort.SliceStable(records, func(i, j int) bool {
		if records[i].RegisteredAt.Equal(records[j].RegisteredAt) {
			return records[i].URL < records[j].URL
		}
		return records[i].RegisteredAt.Before(records[j].RegisteredAt)
	})
}
