package events

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/faustyu/gh-notify-go/internal/events/render"
)

type gollumPayload struct {
	Pages []struct {
		PageName string `json:"page_name"`
		Action   string `json:"action"`
		HTMLURL  string `json:"html_url"`
	} `json:"pages"`
	Repo struct {
		FullName string `json:"full_name"`
	} `json:"repository"`
	Sender struct {
		Login   string `json:"login"`
		HTMLURL string `json:"html_url"`
	} `json:"sender"`
}

func init() {
	Register("gollum", nil, renderGollum)
}

func renderGollum(raw json.RawMessage) (string, error) {
	var p gollumPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return "", fmt.Errorf("parse gollum: %w", err)
	}

	var b strings.Builder
	b.WriteString(render.Emoji(render.EmojiFont, "📖"))
	b.WriteString(" <b>")
	b.WriteString(render.Escape(p.Repo.FullName))
	b.WriteString("</b>\n")
	b.WriteString(fmt.Sprintf("%s обновил вики:", render.Link(p.Sender.HTMLURL, p.Sender.Login)))
	for _, page := range p.Pages {
		b.WriteString("\n• " + render.Link(page.HTMLURL,
			render.Truncate(page.PageName+" ("+page.Action+")", 80)))
	}
	return b.String(), nil
}
