package main

import (
	"strings"
)

const embedColor = 0xEB459E

type embed struct {
	Title       string       `json:"title,omitempty"`
	Description string       `json:"description,omitempty"`
	Color       int          `json:"color,omitempty"`
	Fields      []embedField `json:"fields,omitempty"`
	Image       *embedMedia  `json:"image,omitempty"`
	Footer      *embedFooter `json:"footer,omitempty"`
}

type embedField struct {
	Name   string `json:"name"`
	Value  string `json:"value"`
	Inline bool   `json:"inline"`
}

type embedMedia struct {
	URL string `json:"url"`
}

type embedFooter struct {
	Text string `json:"text"`
}

func newEmbed(title, description string) embed {
	return embed{
		Title:       title,
		Description: description,
		Color:       embedColor,
	}
}

func addField(e *embed, name, value string, inline bool) {
	if value == "" {
		return
	}
	e.Fields = append(e.Fields, embedField{Name: name, Value: truncate(value, 1024), Inline: inline})
}

func buildImageEmbed(image map[string]any) embed {
	e := embed{Color: embedColor, Footer: &embedFooter{Text: "waifu.im"}}

	addField(&e, "ID", getString(image, "id"), true)

	tags := make([]string, 0)
	for _, tag := range getList(image, "tags") {
		if name := getString(tag, "name"); name != "" {
			tags = append(tags, name)
		}
	}
	addField(&e, "Tags", strings.Join(tags, ", "), true)

	artists := getList(image, "artists")
	if len(artists) > 0 {
		addField(&e, "Artist", getString(artists[0], "name"), true)
	}

	if getBool(image, "isAnimated") {
		addField(&e, "Format", "GIF", true)
	}

	if source := getString(image, "source"); source != "" {
		addField(&e, "Source", "[link]("+source+")", true)
	}

	if url := getString(image, "url"); url != "" {
		e.Image = &embedMedia{URL: url}
	}
	return e
}

func buildImageDetailEmbed(image map[string]any) embed {
	e := buildImageEmbed(image)
	e.Title = "Image " + getString(image, "id")

	kept := make([]embedField, 0, len(e.Fields))
	for _, field := range e.Fields {
		if field.Name == "Format" || field.Name == "Size" {
			continue
		}
		kept = append(kept, field)
	}

	size := getString(image, "width") + "x" + getString(image, "height")
	e.Fields = append([]embedField{
		{Name: "Size", Value: size, Inline: true},
		{Name: "Format", Value: getString(image, "extension"), Inline: true},
	}, kept...)
	return e
}
