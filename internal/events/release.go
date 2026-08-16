package events

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/faustyu/gh-notify-go/internal/events/render"
)

type releasePayload struct {
	Action  string `json:"action"`
	Release struct {
		TagName    string `json:"tag_name"`
		Name       string `json:"name"`
		HTMLURL    string `json:"html_url"`
		Prerelease bool   `json:"prerelease"`
		Draft      bool   `json:"draft"`
		Body       string `json:"body"`
	} `json:"release"`
	Repo struct {
		FullName string `json:"full_name"`
	} `json:"repository"`
	Sender struct {
		Login   string `json:"login"`
		HTMLURL string `json:"html_url"`
	} `json:"sender"`
}

func init() {
	Register("release", ActionFilter{"published"}, renderRelease)
}

func renderRelease(raw json.RawMessage) (string, error) {
	var p releasePayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return "", fmt.Errorf("parse release: %w", err)
	}

	title := p.Release.Name
	if title == "" {
		title = p.Release.TagName
	}

	var b strings.Builder
	b.WriteString(render.Emoji(render.EmojiParty, "🚀"))
	b.WriteString(" <b>")
	b.WriteString(render.Escape(p.Repo.FullName))
	b.WriteString("</b>\n")
	b.WriteString(fmt.Sprintf("%s выпустил %s\n",
		render.Link(p.Sender.HTMLURL, p.Sender.Login),
		render.Link(p.Release.HTMLURL, render.Truncate(title, 80)),
	))
	if p.Release.Prerelease {
		b.WriteString("pre-release\n")
	}
	if body := strings.TrimSpace(p.Release.Body); body != "" {
		b.WriteString("\n" + render.Markdown(body, 300))
	}
	return b.String(), nil
}
