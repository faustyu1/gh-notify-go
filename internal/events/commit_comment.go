package events

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/faustyu/gh-notify-go/internal/events/render"
)

type commitCommentPayload struct {
	Action  string `json:"action"`
	Comment struct {
		CommitID string `json:"commit_id"`
		HTMLURL  string `json:"html_url"`
		Body     string `json:"body"`
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
	Register("commit_comment", ActionFilter{"created"}, renderCommitComment)
}

func renderCommitComment(raw json.RawMessage) (string, error) {
	var p commitCommentPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return "", fmt.Errorf("parse commit_comment: %w", err)
	}

	var b strings.Builder
	b.WriteString(render.Emoji(render.EmojiMegaphone, "💬"))
	b.WriteString(" <b>")
	b.WriteString(render.Escape(p.Repo.FullName))
	b.WriteString("</b>\n")
	b.WriteString(fmt.Sprintf("%s прокомментировал коммит %s\n\n",
		render.Link(p.Sender.HTMLURL, p.Sender.Login),
		render.Link(p.Comment.HTMLURL, shortSHA(p.Comment.CommitID)),
	))
	b.WriteString(render.Markdown(p.Comment.Body, 300))
	return b.String(), nil
}
