package events

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/faustyu/gh-notify-go/internal/events/render"
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

func renderPullRequest(raw json.RawMessage) (string, error) {
	var p pullRequestPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return "", fmt.Errorf("parse pull_request: %w", err)
	}

	emoji, verb := pullRequestHeadline(p.Action, p.PullRequest.Merged, p.PullRequest.Draft)

	var b strings.Builder
	b.WriteString(emoji)
	b.WriteString(" <b>")
	b.WriteString(render.Escape(p.Repo.FullName))
	b.WriteString("</b>\n")
	b.WriteString(fmt.Sprintf("%s %s пул-реквест %s\n",
		render.Link(p.PullRequest.User.HTMLURL, p.PullRequest.User.Login),
		verb,
		render.Link(p.PullRequest.HTMLURL, fmt.Sprintf("#%d", p.Number)),
	))

	// The whole summary — title, branch pair, diffstat — reads as one unit,
	// so it collapses into a quote block like the push commit list.
	b.WriteString("\n<blockquote>\n")
	b.WriteString("<b>" + render.Escape(render.Truncate(p.PullRequest.Title, 120)) + "</b>\n")
	b.WriteString(fmt.Sprintf("<code>%s</code> → <code>%s</code>\n",
		render.Escape(p.PullRequest.Head.Ref),
		render.Escape(p.PullRequest.Base.Ref),
	))
	if p.PullRequest.ChangedFiles > 0 {
		b.WriteString(fmt.Sprintf("%s +%d −%d в %d файлах\n",
			render.Emoji(render.EmojiFile, "📁"),
			p.PullRequest.Additions,
			p.PullRequest.Deletions,
			p.PullRequest.ChangedFiles,
		))
	}
	b.WriteString("</blockquote>")

	if body := strings.TrimSpace(p.PullRequest.Body); body != "" {
		b.WriteString("\n" + render.Markdown(body, 500))
	}
	return b.String(), nil
}

// pullRequestHeadline picks the icon and verb. "closed" splits into merged
// and rejected, which are very different outcomes and must not look alike.
func pullRequestHeadline(action string, merged, draft bool) (string, string) {
	switch {
	case action == "closed" && merged:
		return render.Emoji(render.EmojiCheck, "✅"), "вмержил"
	case action == "closed":
		return render.Emoji(render.EmojiCross, "❌"), "закрыл"
	case action == "reopened":
		return render.Emoji(render.EmojiLockOpen, "🔓"), "переоткрыл"
	case action == "ready_for_review":
		return render.Emoji(render.EmojiEye, "👁"), "снял черновик с"
	case draft:
		return render.Emoji(render.EmojiPencil, "🖋"), "открыл черновик"
	default:
		return render.Emoji(render.EmojiWrite, "✍"), "открыл"
	}
}
