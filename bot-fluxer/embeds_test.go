package main

import "testing"

func testImage() map[string]any {
	return map[string]any{
		"id":         float64(42),
		"url":        "https://cdn.example/42.webp",
		"width":      float64(1920),
		"height":     float64(1080),
		"extension":  "png",
		"isAnimated": true,
		"source":     "https://example.com/post",
		"tags": []any{
			map[string]any{"name": "maid"},
			map[string]any{"name": "uniform"},
		},
		"artists": []any{
			map[string]any{"name": "alice"},
		},
	}
}

func fieldValue(e embed, name string) string {
	for _, field := range e.Fields {
		if field.Name == name {
			return field.Value
		}
	}
	return ""
}

func countFields(e embed, name string) int {
	count := 0
	for _, field := range e.Fields {
		if field.Name == name {
			count++
		}
	}
	return count
}

func TestBuildImageEmbed(t *testing.T) {
	e := buildImageEmbed(testImage())

	if e.Image == nil || e.Image.URL != "https://cdn.example/42.webp" {
		t.Fatalf("image = %#v", e.Image)
	}
	if e.Footer == nil || e.Footer.Text != "waifu.im" {
		t.Fatalf("footer = %#v", e.Footer)
	}
	if got := fieldValue(e, "ID"); got != "42" {
		t.Errorf("ID = %q", got)
	}
	if got := fieldValue(e, "Tags"); got != "maid, uniform" {
		t.Errorf("Tags = %q", got)
	}
	if got := fieldValue(e, "Artist"); got != "alice" {
		t.Errorf("Artist = %q", got)
	}
	if got := fieldValue(e, "Format"); got != "GIF" {
		t.Errorf("Format = %q", got)
	}
}

func TestBuildImageDetailEmbed(t *testing.T) {
	e := buildImageDetailEmbed(testImage())

	if e.Title != "Image 42" {
		t.Errorf("title = %q", e.Title)
	}
	if got := fieldValue(e, "Size"); got != "1920x1080" {
		t.Errorf("Size = %q", got)
	}
	if got := fieldValue(e, "Format"); got != "png" {
		t.Errorf("Format = %q", got)
	}
	if got := countFields(e, "Format"); got != 1 {
		t.Errorf("Format fields = %d, want 1", got)
	}
}

func TestBuildImageEmbedEmptyImage(t *testing.T) {
	e := buildImageEmbed(map[string]any{})
	if e.Image != nil {
		t.Errorf("image = %#v, want nil", e.Image)
	}
	if len(e.Fields) != 0 {
		t.Errorf("fields = %#v, want none", e.Fields)
	}
}
