package events

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/faustyu/gh-notify-go/internal/events/render"
)

type forkPayload struct {
	Forkee struct {
		FullName string `json:"full_name"`
		HTMLURL  string `json:"html_url"`
	} `json:"forkee"`
	Repo struct {
		FullName string `json:"full_name"`
	} `json:"repository"`
	Sender struct {
		Login   string `json:"login"`
		HTMLURL string `json:"html_url"`
	} `json:"sender"`
}

func init() {
	Register("fork", nil, renderFork)
}

func renderFork(raw json.RawMessage) (string, error) {
	var p forkPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return "", fmt.Errorf("parse fork: %w", err)
	}

	var b strings.Builder
	b.WriteString(render.Emoji(render.EmojiDownload, "🍴"))
	b.WriteString(" <b>")
	b.WriteString(render.Escape(p.Repo.FullName))
	b.WriteString("</b>\n")
	b.WriteString(fmt.Sprintf("%s форкнул в %s",
		render.Link(p.Sender.HTMLURL, p.Sender.Login),
		render.Link(p.Forkee.HTMLURL, p.Forkee.FullName),
	))
	return b.String(), nil
}
