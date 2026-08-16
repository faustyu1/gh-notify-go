package events

import (
	"encoding/json"
	"fmt"

	"github.com/faustyu/gh-notify-go/internal/events/render"
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

func renderPublic(raw json.RawMessage) (string, error) {
	var p publicPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return "", fmt.Errorf("parse public: %w", err)
	}

	return fmt.Sprintf("%s <b>%s</b> теперь открытый!\n%s открыл репозиторий",
		render.Emoji(render.EmojiLockOpen, "🔓"),
		render.Link(p.Repo.HTMLURL, p.Repo.FullName),
		render.Link(p.Sender.HTMLURL, p.Sender.Login),
	), nil
}
