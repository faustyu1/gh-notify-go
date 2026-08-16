package events

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/faustyu/gh-notify-go/internal/events/render"
)

// maxCommitsListed caps how many commit lines a single push renders. Beyond
// this the message says how many were omitted rather than growing unbounded.
const maxCommitsListed = 10

type pushPayload struct {
	Ref     string `json:"ref"`
	Forced  bool   `json:"forced"`
	Compare string `json:"compare"`
	Repo    struct {
		FullName string `json:"full_name"`
		HTMLURL  string `json:"html_url"`
	} `json:"repository"`
	Sender struct {
		Login   string `json:"login"`
		HTMLURL string `json:"html_url"`
	} `json:"sender"`
	Commits []struct {
		ID      string `json:"id"`
		Message string `json:"message"`
		URL     string `json:"url"`
	} `json:"commits"`
}

func init() {
	// push has no action field, so every delivery is wanted.
	Register("push", nil, renderPush)
}

func renderPush(raw json.RawMessage) (string, error) {
	var p pushPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return "", fmt.Errorf("parse push: %w", err)
	}

	branch := strings.TrimPrefix(p.Ref, "refs/heads/")

	var b strings.Builder
	b.WriteString(render.Emoji(render.EmojiUpload, "⬆"))
	b.WriteString(" <b>")
	b.WriteString(render.Escape(p.Repo.FullName))
	b.WriteString("</b>\n")

	verb := "запушил"
	if p.Forced {
		verb = "форс-запушил"
	}
	b.WriteString(fmt.Sprintf("%s %s в %s — %s\n\n",
		render.Link(p.Sender.HTMLURL, p.Sender.Login),
		verb,
		"<code>"+render.Escape(branch)+"</code>",
		pluralCommits(len(p.Commits)),
	))

	shown := p.Commits
	if len(shown) > maxCommitsListed {
		shown = shown[:maxCommitsListed]
	}
	for _, c := range shown {
		title, _, _ := strings.Cut(c.Message, "\n")
		b.WriteString(fmt.Sprintf("• %s %s\n",
			render.Link(c.URL, shortSHA(c.ID)),
			render.Escape(render.Truncate(title, 72)),
		))
	}
	if omitted := len(p.Commits) - len(shown); omitted > 0 {
		b.WriteString(fmt.Sprintf("…и ещё %d\n", omitted))
	}

	if p.Compare != "" {
		b.WriteString("\n" + render.Link(p.Compare, "Посмотреть изменения"))
	}
	return b.String(), nil
}

func shortSHA(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

// pluralCommits produces the correct Russian plural form, which depends on
// the last digit and the teens exception.
func pluralCommits(n int) string {
	word := "коммитов"
	switch {
	case n%10 == 1 && n%100 != 11:
		word = "коммит"
	case n%10 >= 2 && n%10 <= 4 && (n%100 < 10 || n%100 >= 20):
		word = "коммита"
	}
	return fmt.Sprintf("%d %s", n, word)
}
