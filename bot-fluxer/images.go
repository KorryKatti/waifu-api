package main

import (
	"errors"
	"fmt"
	"math/rand"
	"net/url"
	"strings"
	"time"
)

var errNSFWNotAllowed = errors.New("nsfw is not allowed in this channel")

var orientations = map[string]bool{
	"landscape": true,
	"portrait":  true,
	"square":    true,
}

func parseWaifuQuery(args string, nsfwAllowed bool) (url.Values, error) {
	parts := strings.Fields(strings.ToLower(args))

	wantedNSFW := false
	filtered := make([]string, 0, len(parts))
	for _, part := range parts {
		if part == "nsfw" {
			wantedNSFW = true
			continue
		}
		filtered = append(filtered, part)
	}

	if wantedNSFW && !nsfwAllowed {
		return nil, errNSFWNotAllowed
	}

	params := url.Values{}
	switch {
	case wantedNSFW:
		params.Set("isNsfw", "True")
	case nsfwAllowed:
		params.Set("isNsfw", "all")
	default:
		params.Set("isNsfw", "False")
	}
	params.Set("isAnimated", "All")

	tags := make([]string, 0, len(filtered))
	for _, part := range filtered {
		if part == "gif" {
			params.Set("isAnimated", "True")
			continue
		}
		if orientations[part] {
			params.Set("orientation", strings.ToUpper(part[:1])+part[1:])
			continue
		}
		tags = append(tags, part)
	}

	if len(tags) > 0 {
		params.Set("includedTags", strings.Join(tags, ","))
	}

	params.Set("orderBy", "Random")
	params.Set("pageSize", "1")
	return params, nil
}

func (a *botApp) nsfwAllowed(channelID string) bool {
	if channelID == "" {
		return false
	}
	if a.cfg.nsfwChannels[channelID] {
		return true
	}
	return a.bot.IsNSFW(channelID)
}

func (a *botApp) nsfwParam(channelID string) string {
	if a.nsfwAllowed(channelID) {
		return "all"
	}
	return "False"
}

func nsfwHint(channelID string) string {
	return "Ask a server admin to enable *Channel Settings > Age Restriction*, " +
		"or add the channel ID (" + channelID + ") to `NSFW_CHANNELS` in the bot config."
}

func (a *botApp) cmdWaifu(m *message, args string) {
	if wait, ok := a.state.allow(m.Author.ID, "waifu", 10*time.Second); !ok {
		a.reply(m, "slow down — try again in "+describeWait(wait))
		return
	}

	params, err := parseWaifuQuery(args, a.nsfwAllowed(m.ChannelID))
	if errors.Is(err, errNSFWNotAllowed) {
		a.reply(m, "This channel isn't an NSFW channel. "+nsfwHint(m.ChannelID))
		return
	}

	body, err := a.api.getJSON(a.cfg.apiBase, "images", params, defaultAPITimeout)
	if err != nil {
		a.reply(m, apiFailure(err))
		return
	}

	items := getList(body, "items")
	if len(items) == 0 {
		a.reply(m, "No images matched that search.")
		return
	}

	a.state.noteServed()
	a.sendEmbed(m, buildImageEmbed(items[0]))
}

func (a *botApp) cmdTag(m *message, args string) {
	slug := strings.TrimSpace(args)
	if slug == "" {
		a.reply(m, "Usage: "+a.cfg.prefix+"tag <slug>")
		return
	}
	if wait, ok := a.state.allow(m.Author.ID, "tag", 10*time.Second); !ok {
		a.reply(m, "slow down — try again in "+describeWait(wait))
		return
	}

	params := url.Values{}
	params.Set("includedTags", slug)
	params.Set("pageSize", "5")
	params.Set("isNsfw", a.nsfwParam(m.ChannelID))

	a.postTagResults(m, params, "Tag", slug, "No images tagged `"+slug+"`.")
}

func (a *botApp) cmdArtist(m *message, args string) {
	name := strings.TrimSpace(args)
	if name == "" {
		a.reply(m, "Usage: "+a.cfg.prefix+"artist <name>")
		return
	}
	if wait, ok := a.state.allow(m.Author.ID, "artist", 10*time.Second); !ok {
		a.reply(m, "slow down — try again in "+describeWait(wait))
		return
	}

	params := url.Values{}
	params.Set("includedArtists", name)
	params.Set("pageSize", "5")
	params.Set("isNsfw", a.nsfwParam(m.ChannelID))

	a.postTagResults(m, params, "Artist", name, "No images by `"+name+"`.")
}

func (a *botApp) postTagResults(m *message, params url.Values, noun, name, empty string) {
	body, err := a.api.getJSON(a.cfg.apiBase, "images", params, defaultAPITimeout)
	if err != nil {
		a.reply(m, apiFailure(err))
		return
	}

	items := getList(body, "items")
	if len(items) == 0 {
		a.replyNothingFound(m, params, noun, name, empty)
		return
	}

	image := items[rand.Intn(len(items))]
	a.state.noteServed()
	a.sendEmbed(m, buildImageEmbed(image))
}

func (a *botApp) replyNothingFound(m *message, params url.Values, noun, name, plain string) {
	check := cloneValues(params)
	check.Set("isNsfw", "True")
	check.Set("pageSize", "1")

	body, err := a.api.getJSON(a.cfg.apiBase, "images", check, defaultAPITimeout)
	if err == nil {
		if total := getInt(body, "totalCount"); total > 0 && !a.nsfwAllowed(m.ChannelID) {
			a.reply(m, fmt.Sprintf(
				"%s `%s` has %d NSFW image(s) and nothing age-restricted is shown here. "+
					"Use a channel with *Age Restriction* enabled.",
				noun, name, total,
			))
			return
		}
	}
	a.reply(m, plain)
}

func cloneValues(values url.Values) url.Values {
	copyOfValues := make(url.Values, len(values))
	for key, items := range values {
		copyOfValues[key] = append([]string(nil), items...)
	}
	return copyOfValues
}
