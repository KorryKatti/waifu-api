package main

import (
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

const (
	instancePingLimit        = 25
	instancePingMaxRedirects = 3
)

var instanceClient = &http.Client{
	Timeout:   instanceTimeout,
	Transport: &http.Transport{DisableKeepAlives: true},
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= instancePingMaxRedirects {
			return http.ErrUseLastResponse
		}
		return nil
	},
}

func instanceProbeURL(rawURL string) (string, bool) {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return "", false
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", false
	}
	if parsed.Hostname() == "" || parsed.User != nil {
		return "", false
	}

	parsed.Path = strings.TrimRight(parsed.Path, "/")
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String(), true
}

func measureOne(dialURL string) pingRecord {
	start := time.Now()
	// The catalogue, not /version. The handshake is this repository's own
	// addition and an instance can be listed without it, so timing it would
	// print "(HTTP 404)" beside a deployment that works.
	resp, err := instanceClient.Get(dialURL + "/images?pageSize=1")
	if err != nil {
		record := pingRecord{At: time.Now()}
		if os.IsTimeout(err) {
			record.Err = "timeout"
		} else {
			record.Err = "unreachable"
		}
		return record
	}
	defer resp.Body.Close()

	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
	return pingRecord{
		MS:     float64(time.Since(start).Microseconds()) / 1000,
		HasMS:  true,
		Status: resp.StatusCode,
		At:     time.Now(),
	}
}

func (a *botApp) measureEntries(entries []map[string]any) int {
	type target struct {
		raw  string
		dial string
	}

	targets := make([]target, 0, len(entries))
	seen := make(map[string]bool)
	for _, entry := range entries {
		raw := getString(entry, "url")
		if seen[raw] {
			continue
		}
		dial, ok := instanceProbeURL(raw)
		if !ok {
			continue
		}
		seen[raw] = true
		targets = append(targets, target{raw: raw, dial: dial})
		if len(targets) >= instancePingLimit {
			break
		}
	}

	if len(targets) == 0 {
		a.state.replacePings(map[string]pingRecord{})
		return 0
	}

	results := make([]pingRecord, len(targets))
	var wg sync.WaitGroup
	for index, item := range targets {
		wg.Add(1)
		go func(index int, dial string) {
			defer wg.Done()
			results[index] = measureOne(dial)
		}(index, item.dial)
	}
	wg.Wait()

	measured := make(map[string]pingRecord, len(targets))
	for index, item := range targets {
		measured[item.raw] = results[index]
	}
	a.state.replacePings(measured)

	now := time.Now()
	for _, item := range targets {
		log.Printf("instance ping: %s -> %s", a.cfg.redact(item.raw), a.describePing(item.raw, now))
	}
	return len(targets)
}

func (a *botApp) refreshInstancePings() int {
	body, err := a.api.getJSON(a.cfg.apiBase, "instances", nil, longReadTimeout)
	if err != nil {
		log.Printf("instance sweep: could not read the index, keeping the last measurements: %s", a.cfg.redact(err.Error()))
		return 0
	}

	entries := getList(body, "instances")
	if len(entries) == 0 {
		a.state.replacePings(map[string]pingRecord{})
		return 0
	}
	return a.measureEntries(entries)
}

func (a *botApp) pingFresh() time.Duration {
	fresh := 2 * a.cfg.instancePingEvery
	if fresh < time.Minute {
		fresh = time.Minute
	}
	return fresh
}

func (a *botApp) describePing(rawURL string, now time.Time) string {
	record, ok := a.state.getPing(rawURL)
	if !ok || (!record.HasMS && record.Err == "") {
		return "not measured yet"
	}

	var text string
	if record.Err != "" {
		switch record.Err {
		case "timeout":
			text = fmt.Sprintf("no answer in %ds", int(instanceTimeout.Seconds()))
		case "sweep":
			text = "measurement failed"
		default:
			text = "no answer"
		}
	} else {
		text = fmt.Sprintf("%d ms", int(math.Round(record.MS)))
		if record.Status < 200 || record.Status >= 300 {
			text += fmt.Sprintf(" (HTTP %d)", record.Status)
		}
	}

	if age := now.Sub(record.At); age > a.pingFresh() {
		text += ", " + describeAge(age) + " old"
	}
	return text
}

func (a *botApp) cmdInstances(m *message, args string) {
	needed := strings.EqualFold(strings.TrimSpace(args), "needed")
	if args != "" && !needed {
		a.reply(m, "Usage: "+a.cfg.prefix+"instances [needed]")
		return
	}

	body, err := a.api.getJSON(a.cfg.apiBase, "instances", nil, longReadTimeout)
	if err != nil {
		a.reply(m, apiFailure(err))
		return
	}

	items := getList(body, "instances")
	if len(items) == 0 {
		a.reply(m, "Nobody has registered an instance yet.")
		return
	}

	note := ""
	if needed {
		refusal, measurementNote := a.forceInstanceMeasurement(m, items)
		if refusal != "" {
			a.reply(m, refusal)
			return
		}
		note = measurementNote
	}

	now := time.Now()
	lines := make([]string, 0, 20)
	for index, entry := range items {
		if index >= 20 {
			break
		}

		label := getString(entry, "name")
		if label == "" {
			label = getString(entry, "url")
		}
		if label == "" {
			label = "?"
		}

		mark := "not checked yet"
		if value, ok := entry["images"]; ok && value != nil {
			if serving, ok := value.(bool); ok {
				if serving {
					mark = "serving"
				} else {
					mark = "image check failed"
				}
			}
		}

		lines = append(lines, fmt.Sprintf(
			"[%s](%s) — %s · %s",
			label, getString(entry, "url"), mark, a.describePing(getString(entry, "url"), now),
		))
	}

	description := strings.Join(lines, "\n")
	if note != "" {
		description = "*" + note + "*\n\n" + description
	}

	e := newEmbed(fmt.Sprintf("Instances (%d)", len(items)), truncate(description, 4000))
	if a.cfg.instancePingEvery > 0 {
		e.Footer = &embedFooter{Text: "Latency is this bot's round trip, measured every " + describeAge(a.cfg.instancePingEvery) + "."}
	} else {
		e.Footer = &embedFooter{Text: "Latency measurement is switched off on this bot."}
	}
	a.sendEmbed(m, e)
}

func (a *botApp) forceInstanceMeasurement(m *message, items []map[string]any) (string, string) {
	a.instanceMu.Lock()
	defer a.instanceMu.Unlock()

	path := a.cfg.statePath("instance_ping_log.json")
	data := loadCooldownLog(path)

	if wait := cooldownRemaining(data.LastCooldownAt, a.cfg.instancePingCooldown); wait > 0 {
		recordAttempt(&data, m, "rate_limited", "", "", "")
		saveCooldownLog(path, data)
		return fmt.Sprintf(
			"Someone measured this %s ago. One measurement an hour, across the whole server - try again then.",
			describeWait(wait),
		), ""
	}

	log.Printf("instance ping attempt from %s", m.Author.Username)
	measured := a.measureEntries(items)
	outcome := "measured"
	detail := ""
	if measured == 0 {
		outcome = "nothing_to_measure"
		detail = "no listed instance could be measured"
	}
	recordAttempt(&data, m, outcome, "", "", detail)
	data.LastCooldownAt = time.Now().Format(time.RFC3339)
	saveCooldownLog(path, data)

	unreachable := 0
	for _, record := range a.state.pingSnapshot() {
		if record.Err != "" {
			unreachable++
		}
	}

	note := fmt.Sprintf("Measured %d instance", measured)
	if measured != 1 {
		note += "s"
	}
	note += " just now."
	if unreachable > 0 {
		note += fmt.Sprintf(" %d did not answer.", unreachable)
	}
	return "", note
}

func describeAPIError(err error) string {
	var apiErr *apiError
	if errors.As(err, &apiErr) {
		return fmt.Sprintf("HTTP %d", apiErr.Status)
	}
	return "unreachable (" + err.Error() + ")"
}

func apiFailure(err error) string {
	var apiErr *apiError
	if errors.As(err, &apiErr) {
		return fmt.Sprintf("The waifu API answered HTTP %d.", apiErr.Status)
	}
	return "Could not reach the waifu API: " + err.Error()
}
