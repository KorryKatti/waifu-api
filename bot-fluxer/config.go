package main

import (
	"log"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	defaultAPIBase    = "https://waifu-cdn.waifu-api.workers.dev"
	defaultPrefix     = "w!"
	defaultAPITimeout = 30 * time.Second
	longReadTimeout   = 90 * time.Second
	registerTimeout   = 60 * time.Second
	instanceTimeout   = 3 * time.Second
	statsRefreshEvery = 30 * time.Minute
	// The only guild `w!register` works in, unless REGISTER_GUILDS overrides it.
	defaultRegisterGuildID = "1563189593090568192"
)

type config struct {
	token                string
	prefix               string
	apiBase              string
	registerBase         string
	registerGuilds       map[string]bool
	nsfwChannels         map[string]bool
	adminKey             string
	operatorRole         string
	registerCooldown     time.Duration
	instancePingEvery    time.Duration
	instancePingCooldown time.Duration
	stateDir             string
}

func loadConfig() *config {
	apiBase := strings.TrimRight(env("WAIFU_API", defaultAPIBase), "/")
	registerBase := strings.TrimRight(strings.TrimSpace(os.Getenv("WAIFU_ORIGIN")), "/")

	return &config{
		token:                env("FLUXER_TOKEN", ""),
		prefix:               env("PREFIX", defaultPrefix),
		apiBase:              apiBase,
		registerBase:         registerBase,
		registerGuilds:       parseIDSet(env("REGISTER_GUILDS", defaultRegisterGuildID)),
		nsfwChannels:         parseIDSet(os.Getenv("NSFW_CHANNELS")),
		adminKey:             env("WAIFU_ADMIN_API_KEY", ""),
		operatorRole:         env("INSTANCE_OPERATOR_ROLE", ""),
		registerCooldown:     envSeconds("REGISTER_COOLDOWN_SECONDS", 3600),
		instancePingEvery:    envSeconds("INSTANCE_PING_SECONDS", 3600),
		instancePingCooldown: envSeconds("INSTANCE_PING_COOLDOWN_SECONDS", 3600),
		stateDir:             env("BOT_STATE_DIR", "."),
	}
}

func env(name, fallback string) string {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	return value
}

func envSeconds(name string, fallback int) time.Duration {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return time.Duration(fallback) * time.Second
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 0 {
		log.Printf("config: %s=%q is not a non-negative number, using %d", name, raw, fallback)
		return time.Duration(fallback) * time.Second
	}
	return time.Duration(value) * time.Second
}

func parseIDSet(raw string) map[string]bool {
	set := make(map[string]bool)
	for _, item := range strings.Split(raw, ",") {
		item = strings.TrimSpace(item)
		if item != "" {
			set[item] = true
		}
	}
	return set
}

// registerGuildAllowed reports whether `w!register` may run in guildID.
// DMs (empty guildID) are never allowed. An empty allowlist (e.g. a
// config built by hand in a test) falls back to the default guild, so
// the command stays gated by default.
func (c *config) registerGuildAllowed(guildID string) bool {
	if guildID == "" {
		return false
	}
	if len(c.registerGuilds) == 0 {
		return guildID == defaultRegisterGuildID
	}
	return c.registerGuilds[guildID]
}

// registerGuildList returns the guild IDs `w!register` is enabled in,
// sorted, for use in the refusal message.
func (c *config) registerGuildList() []string {
	if len(c.registerGuilds) == 0 {
		return []string{defaultRegisterGuildID}
	}
	ids := make([]string, 0, len(c.registerGuilds))
	for id := range c.registerGuilds {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func registerTargetIsCDN(target string) bool {
	parsed, err := url.Parse(target)
	if err != nil {
		return false
	}
	host := strings.ToLower(parsed.Hostname())
	return host == "workers.dev" || strings.HasSuffix(host, ".workers.dev")
}

func (c *config) redact(text string) string {
	for _, secret := range []string{c.adminKey, c.token} {
		if len(secret) >= 8 {
			text = strings.ReplaceAll(text, secret, "[redacted]")
		}
	}
	return text
}

func (c *config) statePath(name string) string {
	dir := c.stateDir
	if dir == "" {
		dir = "."
	}
	return filepath.Join(dir, name)
}
