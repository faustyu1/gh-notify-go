package events

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/faustyu/gh-notify-go/internal/events/render"
)

type prReviewCommentPayload struct {
	Action  string `json:"action"`
	Number  int    `json:"number"`
	Comment struct {
		Body    string `json:"body"`
		HTMLURL string `json:"html_url"`
		Path    string `json:"path"`
	} `json:"comment"`
	PullRequest struct {
		HTMLURL string `json:"html_url"`
		Title   string `json:"title"`
	} `json:"pull_request"`
	Repo struct {
		FullName string `json:"full_name"`
	} `json:"repository"`
	Sender struct {
		Login   string `json:"login"`
		HTMLURL string `json:"html_url"`
	} `json:"sender"`
}

func init() {
	Register("pull_request_review_comment", ActionFilter{"created"}, renderPRReviewComment)
}

func renderPRReviewComment(raw json.RawMessage) (string, error) {
	var p prReviewCommentPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return "", fmt.Errorf("parse pull_request_review_comment: %w", err)
	}

	var b strings.Builder
	b.WriteString(render.Emoji(render.EmojiWrite, "✍"))
	b.WriteString(" <b>")
	b.WriteString(render.Escape(p.Repo.FullName))
	b.WriteString("</b>\n")
	b.WriteString(fmt.Sprintf("%s оставил комментарий к коду в PR %s\n",
		render.Link(p.Sender.HTMLURL, p.Sender.Login),
		render.Link(p.Comment.HTMLURL, fmt.Sprintf("#%d «%s»",
			p.Number, render.Truncate(p.PullRequest.Title, 60))),
	))
	b.WriteString("<code>" + render.Escape(p.Comment.Path) + "</code>\n\n")
	b.WriteString(render.Markdown(p.Comment.Body, 300))
	return b.String(), nil
}
