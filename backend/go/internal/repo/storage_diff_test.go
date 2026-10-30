package repo

import (
	"testing"

	"waifuapi/internal/storage"
)

// Every PNG is stored as "{id}.webp" and served back at its .png path, so a
// catalogue that expects only "{id}.png" calls all of them orphans - beside a
// delete button, on the storage reconciliation page. This is the test for that.
func TestListCatalogueKeysCoversTheWebpFallback(t *testing.T) {
	keys := storage.ObjectKeyCandidates(42, ".png")
	if len(keys) != 2 {
		t.Fatalf("a png row yields %d keys, want 2: %v", len(keys), keys)
	}
	if keys[0] != "42.png" {
		t.Errorf("first candidate = %q, want the catalogued name 42.png", keys[0])
	}
	if keys[1] != "42.webp" {
		t.Errorf("second candidate = %q, want the stored name 42.webp", keys[1])
	}
}

func TestListCatalogueKeysHasNoFallbackForOtherFormats(t *testing.T) {
	// Only PNG was converted. A jpeg must not acquire a webp candidate, or a
	// genuinely missing jpeg would be reported as present whenever a stale
	// .webp happened to share its id.
	for _, ext := range []string{".jpg", ".jpeg", ".gif", ".webp"} {
		keys := storage.ObjectKeyCandidates(7, ext)
		if len(keys) != 1 || keys[0] != "7"+ext {
			t.Errorf("%s yields %v, want exactly [7%s]", ext, keys, ext)
		}
	}
}

func TestCatalogueKeyForPrefersTheCataloguedExtension(t *testing.T) {
	// The operator knows the image by its catalogued name, not by the format it
	// happens to be stored in.
	candidates := storage.ObjectKeyCandidates(99, ".png")
	if got := CatalogueKeyFor(candidates); got != "99.png" {
		t.Errorf("CatalogueKeyFor(%v) = %q, want 99.png", candidates, got)
	}
	if got := CatalogueKeyFor(nil); got != "" {
		t.Errorf("CatalogueKeyFor(nil) = %q, want empty", got)
	}
}
