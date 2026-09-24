package events

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/faustyu/gh-notify-go/internal/events/render"
	"github.com/faustyu/gh-notify-go/internal/i18n"
)

type issueCommentPayload struct {
	Action string `json:"action"`
	Issue  struct {
		Number  int    `json:"number"`
		HTMLURL string `json:"html_url"`
		Title   string `json:"title"`
		// Present when the "issue" is a pull request: GitHub delivers PR
		// conversation comments as issue_comment too.
		PullRequest *struct{} `json:"pull_request"`
	} `json:"issue"`
	Comment struct {
		Body string `json:"body"`
	} `json:"comment"`
	Repo struct {
		FullName string `json:"full_name"`
	} `json:"repository"`
	Sender struct {
		Login   string `json:"login"`
		HTMLURL string `json:"html_url"`
	} `json:"sender"`
}

func init() {
	Register("issue_comment", ActionFilter{"created"}, renderIssueComment)
}

func renderIssueComment(loc *i18n.Localizer, raw json.RawMessage) (string, error) {
	var p issueCommentPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return "", fmt.Errorf("parse issue_comment: %w", err)
	}

	var b strings.Builder
	b.WriteString(render.Emoji(render.EmojiMegaphone, "💬"))
	b.WriteString(" <b>")
	b.WriteString(render.Escape(p.Repo.FullName))
	b.WriteString("</b>\n")
	key := "ev.issue_comment.line"
	if p.Issue.PullRequest != nil {
		key = "ev.issue_comment.pr_line"
	}
	b.WriteString(loc.T(key,
		"user", render.Link(p.Sender.HTMLURL, p.Sender.Login),
		"link", render.Link(p.Issue.HTMLURL, fmt.Sprintf("#%d «%s»",
			p.Issue.Number, render.Truncate(p.Issue.Title, 60))),
	))
	b.WriteString("\n\n")
	b.WriteString(render.Markdown(p.Comment.Body, 300))
	return b.String(), nil
}
