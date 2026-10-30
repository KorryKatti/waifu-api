package main

import (
	"errors"
	"testing"
	"time"
)

func TestRegisterTargetIsCDN(t *testing.T) {
	cases := map[string]bool{
		"https://waifu-cdn.waifu-api.workers.dev": true,
		"https://workers.dev":                     true,
		"https://waifu-api-031n.onrender.com":     false,
		"https://example.com":                     false,
	}
	for target, want := range cases {
		if got := registerTargetIsCDN(target); got != want {
			t.Errorf("registerTargetIsCDN(%q) = %v, want %v", target, got, want)
		}
	}
}

func TestLoadConfigRequiresExplicitRegisterHost(t *testing.T) {
	t.Setenv("WAIFU_API", "https://cdn.example")
	t.Setenv("WAIFU_ORIGIN", "")

	cfg := loadConfig()
	if cfg.registerBase != "" {
		t.Fatalf("registerBase = %q, want empty without WAIFU_ORIGIN", cfg.registerBase)
	}
}

func TestParseWaifuQueryNSFWDenied(t *testing.T) {
	_, err := parseWaifuQuery("nsfw maid", false)
	if !errors.Is(err, errNSFWNotAllowed) {
		t.Fatalf("got %v, want errNSFWNotAllowed", err)
	}
}

func TestParseWaifuQueryFlagsAndTags(t *testing.T) {
	params, err := parseWaifuQuery("NSFW Gif Portrait Maid Office", true)
	if err != nil {
		t.Fatal(err)
	}

	cases := map[string]string{
		"isNsfw":       "True",
		"isAnimated":   "True",
		"orientation":  "Portrait",
		"includedTags": "maid,office",
		"orderBy":      "Random",
		"pageSize":     "1",
	}
	for key, want := range cases {
		if got := params.Get(key); got != want {
			t.Errorf("%s = %q, want %q", key, got, want)
		}
	}
}

func TestParseWaifuQueryUsesAllInNSFWChannel(t *testing.T) {
	params, err := parseWaifuQuery("maid", true)
	if err != nil {
		t.Fatal(err)
	}
	if got := params.Get("isNsfw"); got != "all" {
		t.Errorf("isNsfw = %q, want all", got)
	}
}

func TestParseWaifuQueryUsesFalseInSFWChannel(t *testing.T) {
	params, err := parseWaifuQuery("maid", false)
	if err != nil {
		t.Fatal(err)
	}
	if got := params.Get("isNsfw"); got != "False" {
		t.Errorf("isNsfw = %q, want False", got)
	}
}

func TestCooldownRemaining(t *testing.T) {
	now := time.Now().UTC().Add(-10 * time.Minute).Format(time.RFC3339)
	wait := cooldownRemaining(now, time.Hour)
	if wait < 49*time.Minute || wait > 51*time.Minute {
		t.Fatalf("wait = %s, want about 50m", wait)
	}
	if got := cooldownRemaining("", time.Hour); got != 0 {
		t.Fatalf("empty cooldown = %s, want 0", got)
	}
	if got := cooldownRemaining("not a time", time.Hour); got != 0 {
		t.Fatalf("bad cooldown = %s, want 0", got)
	}
}

func TestSplitFirst(t *testing.T) {
	name, rest := splitFirst("register https://example.com My Instance")
	if name != "register" || rest != "https://example.com My Instance" {
		t.Fatalf("got %q, %q", name, rest)
	}

	name, rest = splitFirst("help")
	if name != "help" || rest != "" {
		t.Fatalf("got %q, %q", name, rest)
	}
}

func TestCommaInt(t *testing.T) {
	cases := map[int]string{
		0:       "0",
		999:     "999",
		1000:    "1,000",
		1234567: "1,234,567",
	}
	for value, want := range cases {
		if got := commaInt(value); got != want {
			t.Errorf("commaInt(%d) = %q, want %q", value, got, want)
		}
	}
}
