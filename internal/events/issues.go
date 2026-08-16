package events

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/faustyu/gh-notify-go/internal/events/render"
	"github.com/faustyu/gh-notify-go/internal/i18n"
)

type issuesPayload struct {
	Action string `json:"action"`
	Issue  struct {
		Number  int    `json:"number"`
		HTMLURL string `json:"html_url"`
		Title   string `json:"title"`
		Body    string `json:"body"`
		User    struct {
			Login   string `json:"login"`
			HTMLURL string `json:"html_url"`
		} `json:"user"`
	} `json:"issue"`
	Repo struct {
		FullName string `json:"full_name"`
	} `json:"repository"`
	Sender struct {
		Login   string `json:"login"`
		HTMLURL string `json:"html_url"`
	} `json:"sender"`
}

func init() {
	Register("issues", ActionFilter{"opened", "closed", "reopened"}, renderIssues)
}

func renderIssues(loc *i18n.Localizer, raw json.RawMessage) (string, error) {
	var p issuesPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return "", fmt.Errorf("parse issues: %w", err)
	}

	emoji, key := issuesHeadline(p.Action)

	var b strings.Builder
	b.WriteString(emoji)
	b.WriteString(" <b>")
	b.WriteString(render.Escape(p.Repo.FullName))
	b.WriteString("</b>\n")
	b.WriteString(loc.T(key,
		"user", render.Link(p.Sender.HTMLURL, p.Sender.Login),
		"link", render.Link(p.Issue.HTMLURL, fmt.Sprintf("#%d", p.Issue.Number)),
	))
	b.WriteString("\n\n")
	b.WriteString("<b>" + render.Escape(render.Truncate(p.Issue.Title, 120)) + "</b>")
	if body := strings.TrimSpace(p.Issue.Body); body != "" {
		b.WriteString("\n\n" + render.Markdown(body, 500))
	}
	return b.String(), nil
}

func issuesHeadline(action string) (string, string) {
	switch action {
	case "closed":
		return render.Emoji(render.EmojiCheck, "✅"), "ev.issues.closed"
	case "reopened":
		return render.Emoji(render.EmojiLockOpen, "🔓"), "ev.issues.reopened"
	default:
		return render.Emoji(render.EmojiMegaphone, "📣"), "ev.issues.opened"
	}
}
