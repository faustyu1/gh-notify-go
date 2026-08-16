package events

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/faustyu/gh-notify-go/internal/events/render"
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

func renderCheckSuite(raw json.RawMessage) (string, error) {
	var p checkSuitePayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return "", fmt.Errorf("parse check_suite: %w", err)
	}

	emoji, verdict := render.Emoji(render.EmojiCode, "⚙"), "завершился"
	switch p.CheckSuite.Conclusion {
	case "success":
		emoji, verdict = render.Emoji(render.EmojiCheck, "✅"), "прошёл"
	case "failure", "timed_out", "cancelled":
		emoji, verdict = render.Emoji(render.EmojiCross, "❌"), "упал ("+p.CheckSuite.Conclusion+")"
	}

	var b strings.Builder
	b.WriteString(emoji)
	b.WriteString(" <b>")
	b.WriteString(render.Escape(p.Repo.FullName))
	b.WriteString("</b>\n")
	b.WriteString(fmt.Sprintf("Чек-сьют %s · <code>%s</code>\n\n%s",
		verdict,
		render.Escape(p.CheckSuite.HeadBranch),
		render.Link(p.CheckSuite.HTMLURL, "Подробнее"),
	))
	return b.String(), nil
}
