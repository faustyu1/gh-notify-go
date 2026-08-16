package events

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/faustyu/gh-notify-go/internal/events/render"
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

func renderIssues(raw json.RawMessage) (string, error) {
	var p issuesPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return "", fmt.Errorf("parse issues: %w", err)
	}

	emoji, verb := issuesHeadline(p.Action)

	var b strings.Builder
	b.WriteString(emoji)
	b.WriteString(" <b>")
	b.WriteString(render.Escape(p.Repo.FullName))
	b.WriteString("</b>\n")
	b.WriteString(fmt.Sprintf("%s %s issue %s\n\n",
		render.Link(p.Sender.HTMLURL, p.Sender.Login),
		verb,
		render.Link(p.Issue.HTMLURL, fmt.Sprintf("#%d", p.Issue.Number)),
	))
	b.WriteString("<b>" + render.Escape(render.Truncate(p.Issue.Title, 120)) + "</b>")
	if body := strings.TrimSpace(p.Issue.Body); body != "" {
		b.WriteString("\n\n" + render.Markdown(body, 500))
	}
	return b.String(), nil
}

func issuesHeadline(action string) (string, string) {
	switch action {
	case "closed":
		return render.Emoji(render.EmojiCheck, "✅"), "закрыл"
	case "reopened":
		return render.Emoji(render.EmojiLockOpen, "🔓"), "переоткрыл"
	default:
		return render.Emoji(render.EmojiMegaphone, "📣"), "открыл"
	}
}
