package events

import (
	"encoding/json"
	"fmt"

	"github.com/faustyu/gh-notify-go/internal/events/render"
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

func renderMember(raw json.RawMessage) (string, error) {
	var p memberPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return "", fmt.Errorf("parse member: %w", err)
	}

	verb := "добавил"
	if p.Action == "removed" {
		verb = "исключил"
	}

	return fmt.Sprintf("%s <b>%s</b>\n%s %s соавтора %s",
		render.Emoji(render.EmojiPeople, "👤"),
		render.Escape(p.Repo.FullName),
		render.Link(p.Sender.HTMLURL, p.Sender.Login),
		verb,
		render.Link(p.Member.HTMLURL, p.Member.Login),
	), nil
}
