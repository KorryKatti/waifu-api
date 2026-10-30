package main

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-fluxer/gofluxer"
)

type message = gofluxer.Message

type botApp struct {
	bot        *gofluxer.Bot
	cfg        *config
	api        *apiClient
	state      *appState
	registerMu sync.Mutex
	instanceMu sync.Mutex
}

type commandInfo struct {
	Name   string
	Usage  string
	Short  string
	Detail string
}

var commandList = []commandInfo{
	{
		Name:  "artist",
		Usage: "artist <name>",
		Short: "An image by an artist.",
		Detail: "Fetches up to five images by the artist and posts one at random. " +
			"Run it again for another.",
	},
	{
		Name:   "artists",
		Usage:  "artists",
		Short:  "Artists in the catalogue, with image counts.",
		Detail: "Lists artists that have at least one image in the catalogue.",
	},
	{
		Name:  "help",
		Usage: "help [command]",
		Short: "List every command, or explain one.",
		Detail: "With no argument, lists every command. With a command name, " +
			"shows what it does and how to call it.",
	},
	{
		Name:  "image",
		Usage: "image <id>",
		Short: "Show one image by its id.",
		Detail: "Shows one image with its size, format, tags and artist. " +
			"NSFW images only appear in an age-restricted channel.",
	},
	{
		Name:  "instances",
		Usage: "instances [needed]",
		Short: "Other people's deployments, and whether they work.",
		Detail: "Lists registered instances, whether each one is serving images, " +
			"and this bot's round trip to it. Add `needed` to measure again now " +
			"(one measurement an hour across the whole server).",
	},
	{
		Name:   "ping",
		Usage:  "ping",
		Short:  "Check the bot and the waifu API are alive.",
		Detail: "Calls the API's ping endpoint and reports the round trip.",
	},
	{
		Name:  "register",
		Usage: "register <url> [name]",
		Short: "List your own instance on the public index.",
		Detail: "Registers the base URL of your waifu API instance. The API probes " +
			"it before storing anything. One registration an hour across the whole server.",
	},
	{
		Name:   "stats",
		Usage:  "stats",
		Short:  "Live numbers: latency, catalogue size, uptime, errors.",
		Detail: "Shows the bot's own counters and the catalogue totals.",
	},
	{
		Name:  "tag",
		Usage: "tag <slug>",
		Short: "An image carrying a tag.",
		Detail: "Fetches up to five images with that tag and posts one at random. " +
			"Run it again for another.",
	},
	{
		Name:   "tags",
		Usage:  "tags",
		Short:  "List tags with image counts.",
		Detail: "Lists every tag that has at least one image.",
	},
	{
		Name:   "version",
		Usage:  "version",
		Short:  "Which build of the API is serving right now.",
		Detail: "Shows the software, version, commit and build time reported by the API.",
	},
	{
		Name:  "waifu",
		Usage: "waifu [nsfw] [gif] [orientation] [tag...]",
		Short: "Random image, filtered by flags or tags.",
		Detail: "Get a random waifu. Flags can be combined: `nsfw`, `gif`, " +
			"`landscape`, `portrait`, `square`, or one or more tag names. " +
			"`nsfw` only works in an age-restricted channel.",
	},
}

func (a *botApp) handleMessage(m *message) {
	content := strings.TrimSpace(m.Content)
	if !strings.HasPrefix(content, a.cfg.prefix) {
		return
	}

	rest := strings.TrimSpace(strings.TrimPrefix(content, a.cfg.prefix))
	if rest == "" {
		return
	}

	name, args := splitFirst(rest)
	switch strings.ToLower(name) {
	case "waifu":
		a.cmdWaifu(m, args)
	case "tag":
		a.cmdTag(m, args)
	case "artist":
		a.cmdArtist(m, args)
	case "tags":
		a.cmdTags(m)
	case "artists":
		a.cmdArtists(m)
	case "image":
		a.cmdImage(m, args)
	case "ping":
		a.cmdPing(m)
	case "stats":
		a.cmdStats(m)
	case "version":
		a.cmdVersion(m)
	case "instances":
		a.cmdInstances(m, args)
	case "register":
		a.cmdRegister(m, args)
	case "help":
		a.cmdHelp(m, args)
	}
}

func (a *botApp) reply(m *message, text string) {
	a.bot.ReplyMessageClean(m, a.cfg.redact(text))
}

func (a *botApp) sendEmbed(m *message, e embed) {
	a.bot.SendEmbed(m.ChannelID, e)
}

func (a *botApp) cmdTags(m *message) {
	body, err := a.api.getJSON(a.cfg.apiBase, "tags", nil, defaultAPITimeout)
	if err != nil {
		a.reply(m, apiFailure(err))
		return
	}

	lines := make([]string, 0)
	for _, tag := range getList(body, "items") {
		count := getInt(tag, "imageCount")
		if count == 0 {
			continue
		}
		lines = append(lines, fmt.Sprintf("`%s` (%d)", getString(tag, "slug"), count))
	}

	description := strings.Join(lines, "\n")
	if description == "" {
		description = "none"
	}
	a.sendEmbed(m, newEmbed("Tags", truncate(description, 4000)))
}

func (a *botApp) cmdArtists(m *message) {
	params := url.Values{}
	params.Set("pageSize", "25")

	body, err := a.api.getJSON(a.cfg.apiBase, "artists", params, defaultAPITimeout)
	if err != nil {
		a.reply(m, apiFailure(err))
		return
	}

	lines := make([]string, 0)
	for _, artist := range getList(body, "items") {
		count := getInt(artist, "imageCount")
		if count == 0 {
			continue
		}
		lines = append(lines, fmt.Sprintf("`%s` (%d)", getString(artist, "name"), count))
	}

	description := strings.Join(lines, "\n")
	if description == "" {
		description = "none"
	}
	a.sendEmbed(m, newEmbed("Artists", truncate(description, 4000)))
}

func (a *botApp) cmdImage(m *message, args string) {
	id := strings.TrimSpace(args)
	if _, err := strconv.ParseInt(id, 10, 64); err != nil {
		a.reply(m, "Usage: "+a.cfg.prefix+"image <id>")
		return
	}

	body, err := a.api.getJSON(a.cfg.apiBase, "images/"+id, nil, defaultAPITimeout)
	if err != nil {
		var apiErr *apiError
		if errors.As(err, &apiErr) && apiErr.Status == http.StatusNotFound {
			a.reply(m, fmt.Sprintf("No image with id `%s`.", id))
			return
		}
		a.reply(m, apiFailure(err))
		return
	}

	if getBool(body, "isNsfw") && !a.nsfwAllowed(m.ChannelID) {
		a.reply(m, fmt.Sprintf(
			"Image %s is NSFW and this channel isn't age-restricted. %s",
			id, nsfwHint(m.ChannelID),
		))
		return
	}

	a.state.noteServed()
	a.sendEmbed(m, buildImageDetailEmbed(body))
}

func (a *botApp) cmdPing(m *message) {
	latency, err := a.api.ping()
	if err != nil {
		a.reply(m, "waifu API: "+describeAPIError(err))
		return
	}
	a.reply(m, fmt.Sprintf("waifu API: up | round trip: %dms", latency.Milliseconds()))
}

func (a *botApp) cmdStats(m *message) {
	snapshot := a.state.snapshot()

	e := newEmbed("Stats", "")

	reading := "no reading yet"
	if latency, ok := a.state.freshLatency(); ok {
		reading = fmt.Sprintf("%dms", latency.Milliseconds())
	} else if age, seen := a.state.latencyAge(); seen {
		reading = "last reading " + humanDuration(age) + " ago"
	}
	addField(&e, "API latency", reading, true)
	addField(&e, "Uptime", humanDuration(time.Since(a.state.started)), true)
	addField(&e, "Served here", fmt.Sprintf("%d images", snapshot.Served), true)

	if snapshot.Errors > 0 {
		addField(&e, "API errors", fmt.Sprintf("%d since start", snapshot.Errors), true)
	}
	if snapshot.Images > 0 {
		addField(&e, "Images", commaInt(snapshot.Images), true)
	}
	if snapshot.Tags > 0 {
		addField(&e, "Tags", commaInt(snapshot.Tags), true)
	}
	if snapshot.Artists > 0 {
		addField(&e, "Artists", commaInt(snapshot.Artists), true)
	}

	e.Footer = &embedFooter{Text: "Host: " + a.cfg.apiBase}
	a.sendEmbed(m, e)
}

func (a *botApp) cmdVersion(m *message) {
	body, err := a.api.getJSON(a.cfg.apiBase, "version", nil, longReadTimeout)
	if err != nil {
		a.reply(m, apiFailure(err))
		return
	}

	e := newEmbed("Deployment", "")
	for _, key := range []string{"software", "version", "commit", "builtAt"} {
		addField(&e, key, getString(body, key), false)
	}
	a.sendEmbed(m, e)
}

func (a *botApp) cmdHelp(m *message, args string) {
	name := strings.ToLower(strings.TrimSpace(args))
	if name != "" {
		for _, command := range commandList {
			if command.Name != name {
				continue
			}
			e := newEmbed("`"+a.cfg.prefix+command.Usage+"`", command.Detail)
			e.Footer = &embedFooter{Text: "Run " + a.cfg.prefix + "help for the full list."}
			a.sendEmbed(m, e)
			return
		}
		a.reply(m, "No command named `"+name+"`.")
		return
	}

	commands := append([]commandInfo(nil), commandList...)
	sort.Slice(commands, func(i, j int) bool {
		return commands[i].Name < commands[j].Name
	})

	e := newEmbed(
		"waifu.im — commands",
		"Powered by the [waifu.im](https://waifu-api-eosin.vercel.app) API.",
	)
	for _, command := range commands {
		addField(&e, "`"+a.cfg.prefix+command.Usage+"`", command.Short, false)
	}
	e.Footer = &embedFooter{Text: "Run " + a.cfg.prefix + "help <command> for details."}
	a.sendEmbed(m, e)
}
