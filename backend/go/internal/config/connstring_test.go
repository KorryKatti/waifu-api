package config

import (
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// The failure this guards against is silent and misleading: an unrecognised
// format does not error, it produces a config with an empty host, which pgx
// then resolves as a Unix socket. So every case here is checked by parsing the
// result with pgx itself and reading the fields back.

func parseOrFail(t *testing.T, connString string) *pgxpool.Config {
	t.Helper()
	cfg, err := pgxpool.ParseConfig(connString)
	if err != nil {
		t.Fatalf("pgx could not parse the result %q: %v", connString, err)
	}
	return cfg
}

func TestNormalizeConnectionStringAcceptsNpgsqlFormat(t *testing.T) {
	// Exactly what scripts/mirror/mkconn.py tells you to paste into Render.
	const npgsql = "Host=db.example.supabase.co;Port=5432;Database=postgres;" +
		"Username=postgres.xxxx;Password=hunter2"

	got, err := normalizeConnectionString(npgsql)
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}

	if strings.Contains(got, ";") {
		t.Errorf("result still contains a semicolon: %q", got)
	}

	cfg := parseOrFail(t, got)
	if cfg.ConnConfig.Host != "db.example.supabase.co" {
		t.Errorf("host = %q", cfg.ConnConfig.Host)
	}
	if got := cfg.ConnConfig.Port; got != 5432 {
		t.Errorf("port = %d", got)
	}
	if cfg.ConnConfig.Database != "postgres" {
		t.Errorf("database = %q", cfg.ConnConfig.Database)
	}
	if cfg.ConnConfig.User != "postgres.xxxx" {
		t.Errorf("user = %q", cfg.ConnConfig.User)
	}
	if cfg.ConnConfig.Password != "hunter2" {
		t.Errorf("password = %q", cfg.ConnConfig.Password)
	}
}

func TestNormalizeConnectionStringLeavesURLsAlone(t *testing.T) {
	const url = "postgres://u:p@host:5432/db?sslmode=require"
	got, err := normalizeConnectionString(url)
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if got != url {
		t.Errorf("a URL must pass through untouched:\n got %q\nwant %q", got, url)
	}
}

func TestNormalizeConnectionStringDecodesPercentEscapedPasswords(t *testing.T) {
	// Exactly what mkconn.py emits for a password containing punctuation: it
	// percent-encodes with safe='', so the value on Render is escaped and Npgsql
	// would unescape it. pgx would not, and authentication would fail.
	const npgsql = "Host=h;Port=5432;Database=d;Username=u;Password=p%40ss%3Bw%20rd"

	got, err := normalizeConnectionString(npgsql)
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	cfg := parseOrFail(t, got)
	if cfg.ConnConfig.Password != "p@ss;w rd" {
		t.Errorf("password = %q, want the decoded form", cfg.ConnConfig.Password)
	}
}

func TestNormalizeConnectionStringLeavesPlainPasswordsAlone(t *testing.T) {
	const npgsql = "Host=h;Port=5432;Database=d;Username=u;Password=sup3rSecret"

	got, err := normalizeConnectionString(npgsql)
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	cfg := parseOrFail(t, got)
	if cfg.ConnConfig.Password != "sup3rSecret" {
		t.Errorf("password = %q", cfg.ConnConfig.Password)
	}
}

func TestNormalizeConnectionStringHandlesSslModeWithASpace(t *testing.T) {
	// mkconn.py writes "SSL Mode=Require" — the key contains a space.
	const npgsql = "Host=h;Port=5432;Database=d;Username=u;Password=p;SSL Mode=Require"

	got, err := normalizeConnectionString(npgsql)
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if !strings.Contains(got, "sslmode=require") {
		t.Errorf("sslmode missing from %q", got)
	}
	parseOrFail(t, got)
}

func TestNormalizeConnectionStringAlwaysSetsHost(t *testing.T) {
	// Without an explicit host pgx silently looks for a Unix socket, which is
	// how a database outage gets reported when the real fault is a format.
	got, err := normalizeConnectionString("Database=d;Username=u;Password=p")
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if !strings.Contains(got, "host=") {
		t.Fatalf("no host in result: %q", got)
	}
	cfg := parseOrFail(t, got)
	if cfg.ConnConfig.Host != "localhost" {
		t.Errorf("host = %q, want an explicit localhost rather than a socket path", cfg.ConnConfig.Host)
	}
}

func TestNormalizeConnectionStringRejectsUnusableInput(t *testing.T) {
	for _, bad := range []string{"", "   ", "nonsense"} {
		if _, err := normalizeConnectionString(bad); err == nil {
			t.Errorf("normalizeConnectionString(%q) should have failed", bad)
		}
	}
}
