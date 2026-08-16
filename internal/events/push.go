package events

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/faustyu/gh-notify-go/internal/events/render"
	"github.com/faustyu/gh-notify-go/internal/i18n"
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
		Author  struct {
			Username string `json:"username"`
		} `json:"author"`
	} `json:"commits"`
}

func init() {
	// push has no action field, so every delivery is wanted.
	Register("push", nil, renderPush)
}

func renderPush(loc *i18n.Localizer, raw json.RawMessage) (string, error) {
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

	headline := "ev.push.pushed"
	if p.Forced {
		headline = "ev.push.forced"
	}
	b.WriteString(loc.T(headline,
		"user", render.Link(p.Sender.HTMLURL, p.Sender.Login),
		"branch", "<code>"+render.Escape(branch)+"</code>",
		"commits", loc.T("ev.push.commits", "n", len(p.Commits)),
	))
	b.WriteString("\n")

	shown := p.Commits
	if len(shown) > maxCommitsListed {
		shown = shown[:maxCommitsListed]
	}
	if len(shown) > 0 {
		b.WriteString("\n<blockquote>")
	}
	for _, c := range shown {
		title, _, _ := strings.Cut(c.Message, "\n")
		author := ""
		if c.Author.Username != "" {
			author = " " + loc.T("ev.push.commit_author",
				"user", render.Escape(c.Author.Username))
		}
		b.WriteString("\n" + loc.T("ev.push.commit_line",
			"sha", render.Link(c.URL, shortSHA(c.ID)),
			"author", author,
			"title", render.Escape(render.Truncate(title, 72)),
		))
	}
	if len(shown) > 0 {
		b.WriteString("\n</blockquote>")
	}
	if omitted := len(p.Commits) - len(shown); omitted > 0 {
		b.WriteString("\n" + loc.T("ev.push.omitted", "n", omitted))
	}

	if p.Compare != "" {
		b.WriteString("\n" + render.Link(p.Compare, loc.T("ev.push.compare")))
	}
	return b.String(), nil
}

func shortSHA(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}
