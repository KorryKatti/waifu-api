package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-fluxer/gofluxer"
)

type cooldownLog struct {
	LastCooldownAt string     `json:"lastCooldownAt"`
	Entries        []logEntry `json:"entries"`
}

type logEntry struct {
	At        string `json:"at"`
	DiscordID string `json:"discordId"`
	Username  string `json:"username"`
	Guild     string `json:"guild,omitempty"`
	URL       string `json:"url,omitempty"`
	Name      string `json:"name,omitempty"`
	Outcome   string `json:"outcome"`
	Detail    string `json:"detail,omitempty"`
}

func loadCooldownLog(path string) cooldownLog {
	data := cooldownLog{}
	raw, err := os.ReadFile(path)
	if err != nil {
		return data
	}
	if err := json.Unmarshal(raw, &data); err != nil {
		log.Printf("could not read %s, starting a new one: %s", path, err)
		return cooldownLog{}
	}
	if data.Entries == nil {
		data.Entries = []logEntry{}
	}
	return data
}

func saveCooldownLog(path string, data cooldownLog) {
	if len(data.Entries) > 200 {
		data.Entries = data.Entries[len(data.Entries)-200:]
	}

	raw, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		log.Printf("could not encode %s: %s", path, err)
		return
	}

	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		log.Printf("could not create the state directory: %s", err)
		return
	}

	temporary := path + ".tmp"
	if err := os.WriteFile(temporary, raw, 0600); err != nil {
		log.Printf("could not write %s: %s", path, err)
		return
	}
	if err := os.Rename(temporary, path); err != nil {
		log.Printf("could not replace %s: %s", path, err)
		_ = os.Remove(temporary)
	}
}

func cooldownRemaining(last string, window time.Duration) time.Duration {
	if last == "" {
		return 0
	}
	started, err := time.Parse(time.RFC3339, last)
	if err != nil {
		return 0
	}
	wait := window - time.Since(started)
	if wait < 0 {
		return 0
	}
	return wait
}

func recordAttempt(data *cooldownLog, m *gofluxer.Message, outcome, rawURL, name, detail string) {
	data.Entries = append(data.Entries, logEntry{
		At:        time.Now().UTC().Format(time.RFC3339),
		DiscordID: m.Author.ID,
		Username:  m.Author.Username,
		Guild:     m.GuildID,
		URL:       rawURL,
		Name:      name,
		Outcome:   outcome,
		Detail:    detail,
	})
}

func (a *botApp) cmdRegister(m *message, args string) {
	if a.cfg.adminKey == "" {
		a.reply(m, "This bot is not configured to register instances. Set `WAIFU_ADMIN_API_KEY` on it and try again.")
		return
	}
	if a.cfg.registerBase == "" {
		a.reply(m, "This bot is not configured to register instances. Set `WAIFU_ORIGIN` to the API host in `.env` - the Render origin, not the CDN.")
		return
	}
	if registerTargetIsCDN(a.cfg.registerBase) {
		a.reply(m, "The bot is configured to send registrations to the CDN, which drops the API key, "+
			"so they would be rejected. Set `WAIFU_ORIGIN` to the API host in `.env` - the Render origin, not the CDN.")
		return
	}

	rawURL, name := splitFirst(args)
	if rawURL == "" {
		a.reply(m, "Usage: "+a.cfg.prefix+"register <url> [name]")
		return
	}

	a.registerMu.Lock()
	message, grantRole := a.registerAttempt(m, rawURL, name)
	a.registerMu.Unlock()

	if grantRole && a.cfg.operatorRole != "" {
		switch {
		case m.GuildID == "":
			message += "\n\nYou used this in DMs, so the operator role was not granted."
		default:
			if err := a.bot.AddRole(m.GuildID, m.Author.ID, a.cfg.operatorRole); err != nil {
				log.Printf("register: could not add the operator role: %s", a.cfg.redact(err.Error()))
				message += "\n\nI could not grant the operator role: I need the Manage Roles " +
					"permission and my highest role has to be above it."
			} else {
				message += "\n\nYou have been given the operator role."
			}
		}
	}

	a.reply(m, message)
}

func (a *botApp) registerAttempt(m *message, rawURL, name string) (string, bool) {
	path := a.cfg.statePath("register_log.json")
	data := loadCooldownLog(path)
	now := time.Now()
	log.Printf("register attempt from %s for %s", m.Author.Username, rawURL)

	if wait := cooldownRemaining(data.LastCooldownAt, a.cfg.registerCooldown); wait > 0 {
		recordAttempt(&data, m, "rate_limited", rawURL, name, "")
		saveCooldownLog(path, data)
		return fmt.Sprintf(
			"One registration an hour, across the whole server. Try again in %s.",
			describeWait(wait),
		), false
	}

	status, body, err := a.api.registerInstance(rawURL, name, m.Author.ID)
	if err != nil {
		var apiErr *apiError
		if errors.As(err, &apiErr) {
			detail := apiErr.Detail
			switch apiErr.Status {
			case http.StatusBadRequest:
				recordAttempt(&data, m, "invalid", rawURL, name, detail)
				saveCooldownLog(path, data)
			case http.StatusUnprocessableEntity:
				detail = "Your instance did not answer correctly.\n" + detail
				recordAttempt(&data, m, "probe_failed", rawURL, name, detail)
				data.LastCooldownAt = now.Format(time.RFC3339)
				saveCooldownLog(path, data)
			default:
				if apiErr.Status == http.StatusForbidden {
					detail = "This bot is not allowed to register instances."
				}
				recordAttempt(&data, m, "refused", rawURL, name, detail)
				data.LastCooldownAt = now.Format(time.RFC3339)
				saveCooldownLog(path, data)
			}
			return a.cfg.redact(detail), false
		}

		recordAttempt(&data, m, "api_unreachable", rawURL, name, "")
		saveCooldownLog(path, data)
		log.Printf("register: %s", a.cfg.redact(err.Error()))
		return "Could not reach the API. Nothing was changed - try again in a moment.", false
	}

	already := status == http.StatusOK
	outcome := "registered"
	if already {
		outcome = "already_registered"
	}
	recordAttempt(&data, m, outcome, rawURL, name, "")
	data.LastCooldownAt = now.Format(time.RFC3339)
	saveCooldownLog(path, data)

	label := getString(body, "name")
	if label == "" {
		label = getString(body, "url")
	}
	if label == "" {
		label = rawURL
	}

	if already {
		return fmt.Sprintf("**%s** is already on the list; nothing was changed.", label), false
	}

	storedURL := getString(body, "url")
	if storedURL == "" {
		storedURL = rawURL
	}
	return fmt.Sprintf("Registered **%s**.\n%s is now on the public list.", label, storedURL), true
}

func splitFirst(text string) (string, string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return "", ""
	}
	if index := strings.IndexAny(text, " \t\n"); index >= 0 {
		return text[:index], strings.TrimSpace(text[index+1:])
	}
	return text, ""
}
