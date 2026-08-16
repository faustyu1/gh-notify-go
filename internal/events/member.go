package events

import (
	"encoding/json"
	"fmt"

	"github.com/faustyu/gh-notify-go/internal/events/render"
	"github.com/faustyu/gh-notify-go/internal/i18n"
)

type memberPayload struct {
	Action string `json:"action"`
	Member struct {
		Login   string `json:"login"`
		HTMLURL string `json:"html_url"`
	} `json:"member"`
	Repo struct {
		FullName string `json:"full_name"`
	} `json:"repository"`
	Sender struct {
		Login   string `json:"login"`
		HTMLURL string `json:"html_url"`
	} `json:"sender"`
}

func init() {
	Register("member", ActionFilter{"added", "removed"}, renderMember)
}

func renderMember(loc *i18n.Localizer, raw json.RawMessage) (string, error) {
	var p memberPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return "", fmt.Errorf("parse member: %w", err)
	}

	key := "ev.member.added"
	if p.Action == "removed" {
		key = "ev.member.removed"
	}

	return render.Emoji(render.EmojiPeople, "👤") +
		" <b>" + render.Escape(p.Repo.FullName) + "</b>\n" + loc.T(key,
		"user", render.Link(p.Sender.HTMLURL, p.Sender.Login),
		"member", render.Link(p.Member.HTMLURL, p.Member.Login),
	), nil
}
