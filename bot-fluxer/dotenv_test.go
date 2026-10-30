package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadDotEnv(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	content := "# comment\nFLUXER_TOKEN=from-file\nPREFIX=\"?\"\nEXISTING=from-file\n"
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}

	t.Setenv("EXISTING", "from-shell")
	t.Setenv("FLUXER_TOKEN", "")
	os.Unsetenv("FLUXER_TOKEN")
	os.Unsetenv("PREFIX")

	loadDotEnv(path)

	if got := os.Getenv("FLUXER_TOKEN"); got != "from-file" {
		t.Errorf("FLUXER_TOKEN = %q", got)
	}
	if got := os.Getenv("PREFIX"); got != "?" {
		t.Errorf("PREFIX = %q", got)
	}
	if got := os.Getenv("EXISTING"); got != "from-shell" {
		t.Errorf("EXISTING = %q, existing env should win", got)
	}
}
