package events

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/faustyu/gh-notify-go/internal/events/render"
	"github.com/faustyu/gh-notify-go/internal/i18n"
)

type pullRequestReviewPayload struct {
	Action string `json:"action"`
	Number int    `json:"number"`
	Review struct {
		State string `json:"state"`
		Body  string `json:"body"`
	} `json:"review"`
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
	Register("pull_request_review", ActionFilter{"submitted"}, renderPullRequestReview)
}

func renderPullRequestReview(loc *i18n.Localizer, raw json.RawMessage) (string, error) {
	var p pullRequestReviewPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return "", fmt.Errorf("parse pull_request_review: %w", err)
	}

	emoji, key := reviewHeadline(p.Review.State)

	var b strings.Builder
	b.WriteString(emoji)
	b.WriteString(" <b>")
	b.WriteString(render.Escape(p.Repo.FullName))
	b.WriteString("</b>\n")
	b.WriteString(loc.T(key,
		"user", render.Link(p.Sender.HTMLURL, p.Sender.Login),
		"link", render.Link(p.PullRequest.HTMLURL, fmt.Sprintf("#%d «%s»",
			p.Number, render.Truncate(p.PullRequest.Title, 60))),
	))
	b.WriteString("\n")
	if body := strings.TrimSpace(p.Review.Body); body != "" {
		b.WriteString("\n" + render.Markdown(body, 300))
	}
	return b.String(), nil
}

// reviewHeadline splits "submitted" into approve / request changes / comment,
// which are very different signals for the author.
func reviewHeadline(state string) (string, string) {
	switch state {
	case "approved":
		return render.Emoji(render.EmojiCheck, "✅"), "ev.pull_request_review.approved"
	case "changes_requested":
		return render.Emoji(render.EmojiCross, "❌"), "ev.pull_request_review.changes_requested"
	default:
		return render.Emoji(render.EmojiWrite, "✍"), "ev.pull_request_review.commented"
	}
}
