package events

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/faustyu/gh-notify-go/internal/events/render"
	"github.com/faustyu/gh-notify-go/internal/i18n"
)

type pullRequestPayload struct {
	Action      string `json:"action"`
	Number      int    `json:"number"`
	PullRequest struct {
		HTMLURL      string `json:"html_url"`
		Title        string `json:"title"`
		Body         string `json:"body"`
		Merged       bool   `json:"merged"`
		Draft        bool   `json:"draft"`
		Additions    int    `json:"additions"`
		Deletions    int    `json:"deletions"`
		ChangedFiles int    `json:"changed_files"`
		Base         struct {
			Ref string `json:"ref"`
		} `json:"base"`
		Head struct {
			Ref string `json:"ref"`
		} `json:"head"`
		User struct {
			Login   string `json:"login"`
			HTMLURL string `json:"html_url"`
		} `json:"user"`
	} `json:"pull_request"`
	Repo struct {
		FullName string `json:"full_name"`
	} `json:"repository"`
}

func init() {
	Register("pull_request",
		ActionFilter{"opened", "closed", "reopened", "ready_for_review"},
		renderPullRequest)
}

func renderPullRequest(loc *i18n.Localizer, raw json.RawMessage) (string, error) {
	var p pullRequestPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return "", fmt.Errorf("parse pull_request: %w", err)
	}

	emoji, key := pullRequestHeadline(p.Action, p.PullRequest.Merged, p.PullRequest.Draft)

	var b strings.Builder
	b.WriteString(emoji)
	b.WriteString(" <b>")
	b.WriteString(render.Escape(p.Repo.FullName))
	b.WriteString("</b>\n")
	b.WriteString(loc.T(key,
		"user", render.Link(p.PullRequest.User.HTMLURL, p.PullRequest.User.Login),
		"link", render.Link(p.PullRequest.HTMLURL, fmt.Sprintf("#%d", p.Number)),
	))
	b.WriteString("\n")

	// The whole summary — title, branch pair, diffstat — reads as one unit,
	// so it collapses into a quote block like the push commit list.
	b.WriteString("\n<blockquote>\n")
	b.WriteString("<b>" + render.Escape(render.Truncate(p.PullRequest.Title, 120)) + "</b>\n")
	b.WriteString(loc.T("ev.pull_request.ref_pair",
		"head", render.Escape(p.PullRequest.Head.Ref),
		"base", render.Escape(p.PullRequest.Base.Ref),
	))
	b.WriteString("\n")
	if p.PullRequest.ChangedFiles > 0 {
		b.WriteString(render.Emoji(render.EmojiFile, "📁"))
		b.WriteString(loc.T("ev.pull_request.diffstat",
			"files", p.PullRequest.ChangedFiles,
			"additions", p.PullRequest.Additions,
			"deletions", p.PullRequest.Deletions,
		))
		b.WriteString("\n")
	}
	b.WriteString("</blockquote>")

	if body := strings.TrimSpace(p.PullRequest.Body); body != "" {
		b.WriteString("\n" + render.Markdown(body, 500))
	}
	return b.String(), nil
}

// pullRequestHeadline picks the icon and message key. "closed" splits into
// merged and rejected, which are very different outcomes and must not look
// alike.
func pullRequestHeadline(action string, merged, draft bool) (string, string) {
	switch {
	case action == "closed" && merged:
		return render.Emoji(render.EmojiCheck, "✅"), "ev.pull_request.merged"
	case action == "closed":
		return render.Emoji(render.EmojiCross, "❌"), "ev.pull_request.closed"
	case action == "reopened":
		return render.Emoji(render.EmojiLockOpen, "🔓"), "ev.pull_request.reopened"
	case action == "ready_for_review":
		return render.Emoji(render.EmojiEye, "👁"), "ev.pull_request.ready_for_review"
	case draft:
		return render.Emoji(render.EmojiPencil, "🖋"), "ev.pull_request.draft"
	default:
		return render.Emoji(render.EmojiWrite, "✍"), "ev.pull_request.opened"
	}
}
