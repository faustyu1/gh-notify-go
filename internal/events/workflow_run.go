package events

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/faustyu/gh-notify-go/internal/events/render"
)

type workflowRunPayload struct {
	Action      string `json:"action"`
	WorkflowRun struct {
		Name       string `json:"name"`
		HTMLURL    string `json:"html_url"`
		Conclusion string `json:"conclusion"`
		HeadBranch string `json:"head_branch"`
		HeadCommit struct {
			Message string `json:"message"`
		} `json:"head_commit"`
	} `json:"workflow_run"`
	Repo struct {
		FullName string `json:"full_name"`
	} `json:"repository"`
	Sender struct {
		Login   string `json:"login"`
		HTMLURL string `json:"html_url"`
	} `json:"sender"`
}

func init() {
	Register("workflow_run", ActionFilter{"completed"}, renderWorkflowRun)
}

func renderWorkflowRun(raw json.RawMessage) (string, error) {
	var p workflowRunPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return "", fmt.Errorf("parse workflow_run: %w", err)
	}

	emoji, verdict := "⚙", "завершился"
	if p.WorkflowRun.Conclusion == "success" {
		emoji, verdict = "✅", "прошёл"
	} else if p.WorkflowRun.Conclusion != "" {
		emoji, verdict = "❌", "упал ("+p.WorkflowRun.Conclusion+")"
	}

	title, _, _ := strings.Cut(p.WorkflowRun.HeadCommit.Message, "\n")

	var b strings.Builder
	b.WriteString(render.Emoji(render.EmojiCode, emoji))
	b.WriteString(" <b>")
	b.WriteString(render.Escape(p.Repo.FullName))
	b.WriteString("</b>\n")
	b.WriteString(fmt.Sprintf("Workflow «%s» — %s · <code>%s</code>\n",
		render.Escape(p.WorkflowRun.Name), verdict,
		render.Escape(p.WorkflowRun.HeadBranch),
	))
	b.WriteString(fmt.Sprintf("By %s\n\n",
		render.Link(p.Sender.HTMLURL, "@"+p.Sender.Login),
	))
	b.WriteString(fmt.Sprintf("%s\n%s",
		render.Link(p.WorkflowRun.HTMLURL, render.Truncate(title, 72)),
		render.Link(p.WorkflowRun.HTMLURL, "View run"),
	))
	return b.String(), nil
}
