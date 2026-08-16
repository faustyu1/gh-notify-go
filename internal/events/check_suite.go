package events

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/faustyu/gh-notify-go/internal/events/render"
	"github.com/faustyu/gh-notify-go/internal/i18n"
)

type checkSuitePayload struct {
	Action     string `json:"action"`
	CheckSuite struct {
		Conclusion string `json:"conclusion"`
		HTMLURL    string `json:"html_url"`
		HeadBranch string `json:"head_branch"`
	} `json:"check_suite"`
	Repo struct {
		FullName string `json:"full_name"`
	} `json:"repository"`
	Sender struct {
		Login   string `json:"login"`
		HTMLURL string `json:"html_url"`
	} `json:"sender"`
}

func init() {
	// Only completions matter; "requested"/"rerequested" are scheduling noise.
	Register("check_suite", ActionFilter{"completed"}, renderCheckSuite)
}

func renderCheckSuite(loc *i18n.Localizer, raw json.RawMessage) (string, error) {
	var p checkSuitePayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return "", fmt.Errorf("parse check_suite: %w", err)
	}

	emoji, verdict := render.Emoji(render.EmojiCode, "⚙"), loc.T("ev.verdict.finished")
	switch p.CheckSuite.Conclusion {
	case "success":
		emoji, verdict = render.Emoji(render.EmojiCheck, "✅"), loc.T("ev.verdict.success")
	case "failure", "timed_out", "cancelled":
		emoji, verdict = render.Emoji(render.EmojiCross, "❌"), loc.T("ev.verdict.failure",
			"conclusion", p.CheckSuite.Conclusion)
	}

	var b strings.Builder
	b.WriteString(emoji)
	b.WriteString(" <b>")
	b.WriteString(render.Escape(p.Repo.FullName))
	b.WriteString("</b>\n")
	b.WriteString(loc.T("ev.check_suite.line",
		"verdict", verdict,
		"branch", render.Escape(p.CheckSuite.HeadBranch),
	))
	b.WriteString("\n\n")
	b.WriteString(render.Link(p.CheckSuite.HTMLURL, loc.T("ev.detail")))
	return b.String(), nil
}
