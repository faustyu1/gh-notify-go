package events

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/faustyu/gh-notify-go/internal/events/render"
	"github.com/faustyu/gh-notify-go/internal/i18n"
)

type refPayload struct {
	Ref     string `json:"ref"`
	RefType string `json:"ref_type"` // branch | tag
	Repo    struct {
		FullName string `json:"full_name"`
	} `json:"repository"`
	Sender struct {
		Login   string `json:"login"`
		HTMLURL string `json:"html_url"`
	} `json:"sender"`
}

func init() {
	Register("create", nil, renderRefCreated)
	Register("delete", nil, renderRefDeleted)
}

func shortRef(loc *i18n.Localizer, refType, ref string) string {
	kind := refType
	switch refType {
	case "branch":
		kind = loc.T("ev.ref.branch")
	case "tag":
		kind = loc.T("ev.ref.tag")
	}
	return kind + " " + render.Escape(strings.TrimPrefix(ref, "refs/"))
}

func renderRefCreated(loc *i18n.Localizer, raw json.RawMessage) (string, error) {
	var p refPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return "", fmt.Errorf("parse create: %w", err)
	}
	return render.Emoji(render.EmojiUpload, "🌿") +
		" <b>" + render.Escape(p.Repo.FullName) + "</b>\n" + loc.T("ev.ref.created",
		"user", render.Link(p.Sender.HTMLURL, p.Sender.Login),
		"ref", "<code>"+shortRef(loc, p.RefType, p.Ref)+"</code>",
	), nil
}

func renderRefDeleted(loc *i18n.Localizer, raw json.RawMessage) (string, error) {
	var p refPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return "", fmt.Errorf("parse delete: %w", err)
	}
	return render.Emoji(render.EmojiTrash, "🗑") +
		" <b>" + render.Escape(p.Repo.FullName) + "</b>\n" + loc.T("ev.ref.deleted",
		"user", render.Link(p.Sender.HTMLURL, p.Sender.Login),
		"ref", "<code>"+shortRef(loc, p.RefType, p.Ref)+"</code>",
	), nil
}
