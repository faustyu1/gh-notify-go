package events

import (
	"encoding/json"
	"fmt"

	"github.com/faustyu/gh-notify-go/internal/events/render"
	"github.com/faustyu/gh-notify-go/internal/i18n"
)

type publicPayload struct {
	Repo struct {
		FullName string `json:"full_name"`
		HTMLURL  string `json:"html_url"`
	} `json:"repository"`
	Sender struct {
		Login   string `json:"login"`
		HTMLURL string `json:"html_url"`
	} `json:"sender"`
}

func init() {
	Register("public", nil, renderPublic)
}

func renderPublic(loc *i18n.Localizer, raw json.RawMessage) (string, error) {
	var p publicPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return "", fmt.Errorf("parse public: %w", err)
	}

	return render.Emoji(render.EmojiLockOpen, "🔓") +
		" <b>" + render.Link(p.Repo.HTMLURL, p.Repo.FullName) + "</b> " +
		loc.T("ev.public.now_public") + "\n" +
		loc.T("ev.public.opened",
			"user", render.Link(p.Sender.HTMLURL, p.Sender.Login)), nil
}
