package events

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/faustyu/gh-notify-go/internal/events/render"
)

type refPayload struct {
	Ref        string `json:"ref"`
	RefType    string `json:"ref_type"` // branch | tag
	Repo       struct {
		FullName string `json:"full_name"`
	} `json:"repository"`
	Sender     struct {
		Login   string `json:"login"`
		HTMLURL string `json:"html_url"`
	} `json:"sender"`
}

func init() {
	Register("create", nil, renderRefCreated)
	Register("delete", nil, renderRefDeleted)
}

func shortRef(refType, ref string) string {
	return refType + " " + render.Escape(strings.TrimPrefix(ref, "refs/"))
}

func renderRefCreated(raw json.RawMessage) (string, error) {
	var p refPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return "", fmt.Errorf("parse create: %w", err)
	}
	return fmt.Sprintf("%s <b>%s</b>\n%s создал %s",
		render.Emoji(render.EmojiUpload, "🌿"),
		render.Escape(p.Repo.FullName),
		render.Link(p.Sender.HTMLURL, p.Sender.Login),
		"<code>"+shortRef(p.RefType, p.Ref)+"</code>",
	), nil
}

func renderRefDeleted(raw json.RawMessage) (string, error) {
	var p refPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return "", fmt.Errorf("parse delete: %w", err)
	}
	return fmt.Sprintf("%s <b>%s</b>\n%s удалил %s",
		render.Emoji(render.EmojiTrash, "🗑"),
		render.Escape(p.Repo.FullName),
		render.Link(p.Sender.HTMLURL, p.Sender.Login),
		"<code>"+shortRef(p.RefType, p.Ref)+"</code>",
	), nil
}
