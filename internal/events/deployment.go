package events

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/faustyu/gh-notify-go/internal/events/render"
)

// deployment fires when a deploy starts; deployment_status when it finishes.
// Together they read as "deploying… → done/failed" in the chat.
type deploymentPayload struct {
	Deployment struct {
		Environment string `json:"environment"`
		HTMLURL     string `json:"url"`
	} `json:"deployment"`
	Repo struct {
		FullName string `json:"full_name"`
	} `json:"repository"`
	Sender struct {
		Login   string `json:"login"`
		HTMLURL string `json:"html_url"`
	} `json:"sender"`
}

type deploymentStatusPayload struct {
	DeploymentStatus struct {
		State     string `json:"state"`
		TargetURL string `json:"target_url"`
	} `json:"deployment_status"`
	Deployment struct {
		Environment string `json:"environment"`
	} `json:"deployment"`
	Repo struct {
		FullName string `json:"full_name"`
	} `json:"repository"`
	Sender struct {
		Login   string `json:"login"`
		HTMLURL string `json:"html_url"`
	} `json:"sender"`
}

func init() {
	Register("deployment", nil, renderDeployment)
	Register("deployment_status", nil, renderDeploymentStatus)
}

func renderDeployment(raw json.RawMessage) (string, error) {
	var p deploymentPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return "", fmt.Errorf("parse deployment: %w", err)
	}
	return fmt.Sprintf("%s <b>%s</b>\n%s начал деплой в <code>%s</code>",
		render.Emoji(render.EmojiUpload, "🚀"),
		render.Escape(p.Repo.FullName),
		render.Link(p.Sender.HTMLURL, p.Sender.Login),
		render.Escape(p.Deployment.Environment),
	), nil
}

func renderDeploymentStatus(raw json.RawMessage) (string, error) {
	var p deploymentStatusPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return "", fmt.Errorf("parse deployment_status: %w", err)
	}

	emoji, verdict := render.Emoji(render.EmojiLoading, "⏳"), p.DeploymentStatus.State
	switch p.DeploymentStatus.State {
	case "success":
		emoji, verdict = render.Emoji(render.EmojiCheck, "✅"), "задеплоено"
	case "failure", "error":
		emoji, verdict = render.Emoji(render.EmojiCross, "❌"), "деплой упал"
	}

	var b strings.Builder
	b.WriteString(emoji)
	b.WriteString(" <b>")
	b.WriteString(render.Escape(p.Repo.FullName))
	b.WriteString("</b>\n")
	b.WriteString(verdict + " в <code>" + render.Escape(p.Deployment.Environment) + "</code>")
	if p.DeploymentStatus.TargetURL != "" {
		b.WriteString("\n" + render.Link(p.DeploymentStatus.TargetURL, "Отчёт"))
	}
	return b.String(), nil
}
