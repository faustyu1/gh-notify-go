package events

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/faustyu/gh-notify-go/internal/events/render"
	"github.com/faustyu/gh-notify-go/internal/i18n"
)

// starPayload is the coalesced shape the ingest path builds itself: one row
// per integration holding every actor whose star is still inside the debounce
// window. It is deliberately not the raw GitHub payload.
type starPayload struct {
	RepoFullName string   `json:"repo_full_name"`
	Actors       []string `json:"actors"`
	TotalStars   int      `json:"total_stars"`
}

func init() {
	// star.deleted never renders: it exists only as the debounce cancel
	// signal, consumed by the ingest service before Wanted is consulted.
	Register("star", ActionFilter{"created"}, renderStar)
}

func renderStar(loc *i18n.Localizer, raw json.RawMessage) (string, error) {
	var p starPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return "", fmt.Errorf("parse star: %w", err)
	}

	var b strings.Builder
	b.WriteString("⭐ <b>")
	b.WriteString(render.Escape(p.RepoFullName))
	b.WriteString("</b>\n")

	switch len(p.Actors) {
	case 0:
		return "", fmt.Errorf("star payload without actors")
	case 1:
		b.WriteString(loc.T("ev.star.single",
			"user", render.Link("https://github.com/"+p.Actors[0], p.Actors[0])))
		b.WriteString("\n")
	default:
		b.WriteString(loc.T("ev.star.multi",
			"n", len(p.Actors),
			"actors", render.Escape(strings.Join(p.Actors, ", ")),
		))
		b.WriteString("\n")
	}
	b.WriteString("\n" + loc.T("ev.star.total", "n", p.TotalStars))
	return b.String(), nil
}
