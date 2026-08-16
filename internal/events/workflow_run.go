package events

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/faustyu/gh-notify-go/internal/events/render"
	"github.com/faustyu/gh-notify-go/internal/i18n"
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

func renderWorkflowRun(loc *i18n.Localizer, raw json.RawMessage) (string, error) {
	var p workflowRunPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return "", fmt.Errorf("parse workflow_run: %w", err)
	}

	emoji, verdict := "⚙", loc.T("ev.verdict.finished")
	if p.WorkflowRun.Conclusion == "success" {
		emoji, verdict = "✅", loc.T("ev.verdict.success")
	} else if p.WorkflowRun.Conclusion != "" {
		emoji, verdict = "❌", loc.T("ev.verdict.failure",
			"conclusion", p.WorkflowRun.Conclusion)
	}

	title, _, _ := strings.Cut(p.WorkflowRun.HeadCommit.Message, "\n")

	var b strings.Builder
	b.WriteString(render.Emoji(render.EmojiCode, emoji))
	b.WriteString(" <b>")
	b.WriteString(render.Escape(p.Repo.FullName))
	b.WriteString("</b>\n")
	b.WriteString(loc.T("ev.workflow_run.line",
		"name", render.Escape(p.WorkflowRun.Name),
		"verdict", verdict,
		"branch", render.Escape(p.WorkflowRun.HeadBranch),
	))
	b.WriteString("\n")
	b.WriteString(loc.T("ev.workflow_run.by",
		"user", render.Link(p.Sender.HTMLURL, "@"+p.Sender.Login),
	))
	b.WriteString("\n\n")
	b.WriteString(render.Link(p.WorkflowRun.HTMLURL, render.Truncate(title, 72)))
	b.WriteString("\n")
	b.WriteString(render.Link(p.WorkflowRun.HTMLURL, loc.T("ev.workflow_run.view_run")))
	return b.String(), nil
}
