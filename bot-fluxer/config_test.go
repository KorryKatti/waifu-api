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

func TestRegisterGuildAllowed(t *testing.T) {
	cfg := loadConfig()
	// Default: only the configured guild, never DMs or other guilds.
	if !cfg.registerGuildAllowed("1563189593090568192") {
		t.Fatal("default guild should be allowed")
	}
	for _, guild := range []string{"", "123", "1563189593090568193"} {
		if cfg.registerGuildAllowed(guild) {
			t.Errorf("guild %q should be denied", guild)
		}
	}

	// A hand-built config with no allowlist also stays gated to the default.
	if (&config{}).registerGuildAllowed("123") {
		t.Error("empty allowlist should deny other guilds")
	}

	// REGISTER_GUILDS overrides the default.
	t.Setenv("REGISTER_GUILDS", "111, 222")
	cfg = loadConfig()
	for _, guild := range []string{"111", "222"} {
		if !cfg.registerGuildAllowed(guild) {
			t.Errorf("guild %q should be allowed", guild)
		}
	}
	if cfg.registerGuildAllowed("1563189593090568192") {
		t.Error("default guild should be denied once REGISTER_GUILDS is set")
	}
	if got := cfg.registerGuildList(); len(got) != 2 || got[0] != "111" || got[1] != "222" {
		t.Errorf("registerGuildList() = %v, want [111 222]", got)
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

func TestParseWaifuQueryUsesTrueInNSFWChannel(t *testing.T) {
	params, err := parseWaifuQuery("maid", true)
	if err != nil {
		t.Fatal(err)
	}
	if got := params.Get("isNsfw"); got != "True" {
		t.Errorf("isNsfw = %q, want True (NSFW-only in NSFW channel, not all)", got)
	}
}

func TestParseWaifuQueryNonceIsUniquePerCall(t *testing.T) {
	first, err := parseWaifuQuery("", false)
	if err != nil {
		t.Fatal(err)
	}
	second, err := parseWaifuQuery("", false)
	if err != nil {
		t.Fatal(err)
	}
	if first.Get("nonce") == "" || second.Get("nonce") == "" {
		t.Fatal("nonce must be set on every waifu query")
	}
	if first.Get("nonce") == second.Get("nonce") {
		t.Error("nonce must differ per call, otherwise the CDN cache key repeats")
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
