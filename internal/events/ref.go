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

// refKey picks the sentence for this ref type. Languages that inflect the
// noun ("создал ветку", not "создал ветка") cannot build the line from a
// bare noun plus a verb, so each type carries its own full message; an
// unknown ref_type falls back to the generic one.
func refKey(action, refType string) string {
	switch refType {
	case "branch", "tag":
		return "ev.ref." + refType + "_" + action
	}
	return "ev.ref." + action
}

// refValue is the {ref} placeholder: the name alone for the types whose
// sentence already names the type, and type plus name otherwise.
func refValue(refType, ref string) string {
	name := render.Escape(strings.TrimPrefix(ref, "refs/"))
	switch refType {
	case "branch", "tag":
	default:
		name = render.Escape(refType) + " " + name
	}
	return "<code>" + name + "</code>"
}

func renderRefCreated(loc *i18n.Localizer, raw json.RawMessage) (string, error) {
	var p refPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return "", fmt.Errorf("parse create: %w", err)
	}
	return render.Emoji(render.EmojiUpload, "🌿") +
		" <b>" + render.Escape(p.Repo.FullName) + "</b>\n" + loc.T(refKey("created", p.RefType),
		"user", render.Link(p.Sender.HTMLURL, p.Sender.Login),
		"ref", refValue(p.RefType, p.Ref),
	), nil
}

func renderRefDeleted(loc *i18n.Localizer, raw json.RawMessage) (string, error) {
	var p refPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return "", fmt.Errorf("parse delete: %w", err)
	}
	return render.Emoji(render.EmojiTrash, "🗑") +
		" <b>" + render.Escape(p.Repo.FullName) + "</b>\n" + loc.T(refKey("deleted", p.RefType),
		"user", render.Link(p.Sender.HTMLURL, p.Sender.Login),
		"ref", refValue(p.RefType, p.Ref),
	), nil
}
