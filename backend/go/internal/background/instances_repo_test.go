package background

import (
	"context"
	"os"
	"testing"
	"time"

	"waifuapi/internal/instances"
	"waifuapi/internal/repo"
	"waifuapi/internal/store"
)

// The instance-probe job needs a repository, because what it writes is the whole
// point: a signal that is computed correctly and stored nowhere is indistinguishable
// from one that was never computed.
//
// It is also the only test in this package that needs a database. The cleanup
// jobs beside it are unit-tested because their behaviour is in the query; this
// one is in the read-probe-write cycle, and a fake repository would be testing
// the fake.

// instanceTestRepo opens the test database and clears the instance index.
//
// A missing WAIFU_TEST_DATABASE_URL is a hard failure rather than a skip, for the
// same reason as the HTTP suite: a silently skipped suite reports green while
// none of it ran, and "the index job was never tested" is exactly the kind of
// thing that is believed rather than checked.
func instanceTestRepo(t *testing.T) (*repo.Repo, func()) {
	t.Helper()

	databaseURL := os.Getenv("WAIFU_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Fatal("set WAIFU_TEST_DATABASE_URL to test the instance job; " +
			"see integration_test.go for how to start a database. " +
			"Skipping here is not allowed: a silently skipped suite hides real breakage.")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	db, err := store.Open(ctx, store.Config{URL: databaseURL, MaxConnections: 2})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}

	if _, err := db.Pool().Exec(ctx, `TRUNCATE "InstanceIndex"`); err != nil {
		db.Close()
		t.Fatalf("clear instance index: %v", err)
	}

	return repo.New(db.Pool()), db.Close
}

// seedInstanceRecord writes one entry directly, so the test is about the job's
// behaviour rather than about registration having worked.
func seedInstanceRecord(t *testing.T, data *repo.Repo, record instances.Record) {
	t.Helper()

	if _, added, err := data.RegisterInstance(t.Context(), record); err != nil {
		t.Fatalf("seed %s: %v", record.URL, err)
	} else if !added {
		t.Fatalf("seed %s: already present", record.URL)
	}
}

// readInstance returns one stored entry.
func readInstance(t *testing.T, data *repo.Repo, url string) instances.Record {
	t.Helper()

	records, err := data.GetInstances(t.Context())
	if err != nil {
		t.Fatalf("read index: %v", err)
	}

	found := instances.Find(records, url)
	if found == nil {
		t.Fatalf("%s is not in the index", url)
	}
	return *found
}
