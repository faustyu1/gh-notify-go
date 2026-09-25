package events

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/faustyu/gh-notify-go/internal/forge"
	"github.com/faustyu/gh-notify-go/internal/i18n"
)

// Gitea, Forgejo and GitVerse payloads copy GitHub's closely: most gt_ kinds
// render with the GitHub renderer as is, and the rest after renaming the few
// fields Gitea names differently. The wording is GitHub's too, since a pull
// request is called a pull request on every one of them.

func init() {
	Register(forge.KindGiteaPush, nil, renderGiteaPush)
	Register(forge.KindGiteaCreate, nil, renderRefCreated)
	Register(forge.KindGiteaDelete, nil, renderRefDeleted)
	Register(forge.KindGiteaPullRequest,
		ActionFilter{"opened", "closed", "reopened"}, renderPullRequest)
	Register(forge.KindGiteaPullRequestReview, nil, renderGiteaReview)
	Register(forge.KindGiteaIssues, ActionFilter{"opened", "closed", "reopened"}, renderIssues)
	Register(forge.KindGiteaIssueComment, ActionFilter{"created"}, renderIssueComment)
	Register(forge.KindGiteaRelease, ActionFilter{"published"}, renderRelease)
	Register(forge.KindGiteaWiki, nil, renderGiteaWiki)
}

// patched decodes a payload into a field map so single fields can be renamed
// or added before a GitHub renderer reads it.
func patched(raw json.RawMessage, edit func(map[string]json.RawMessage) error) (json.RawMessage, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return nil, err
	}
	if err := edit(fields); err != nil {
		return nil, err
	}
	return json.Marshal(fields)
}

// renderGiteaPush is the GitHub push, whose compare link Gitea calls
// compare_url.
func renderGiteaPush(loc *i18n.Localizer, raw json.RawMessage) (string, error) {
	out, err := patched(raw, func(f map[string]json.RawMessage) error {
		if _, ok := f["compare"]; !ok {
			f["compare"] = f["compare_url"]
		}
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("parse gitea push: %w", err)
	}
	return renderPush(loc, out)
}

// renderGiteaReview is the GitHub review. Gitea puts the verdict in the
// review's type and its text in content, where GitHub has state and body.
func renderGiteaReview(loc *i18n.Localizer, raw json.RawMessage) (string, error) {
	out, err := patched(raw, func(f map[string]json.RawMessage) error {
		var review struct {
			Type    string `json:"type"`
			Content string `json:"content"`
		}
		if r, ok := f["review"]; ok {
			if err := json.Unmarshal(r, &review); err != nil {
				return err
			}
		}
		state := "commented"
		switch {
		case strings.HasSuffix(review.Type, "approved"):
			state = "approved"
		case strings.HasSuffix(review.Type, "rejected"):
			state = "changes_requested"
		}
		encoded, err := json.Marshal(map[string]string{"state": state, "body": review.Content})
		if err != nil {
			return err
		}
		f["review"] = encoded
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("parse gitea review: %w", err)
	}
	return renderPullRequestReview(loc, out)
}

// renderGiteaWiki is the GitHub gollum event, which lists the pages it
// touched; Gitea sends one page per delivery.
func renderGiteaWiki(loc *i18n.Localizer, raw json.RawMessage) (string, error) {
	var p struct {
		Action string          `json:"action"`
		Page   string          `json:"page"`
		Repo   json.RawMessage `json:"repository"`
		Sender json.RawMessage `json:"sender"`
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return "", fmt.Errorf("parse gitea wiki: %w", err)
	}
	var repo struct {
		HTMLURL string `json:"html_url"`
	}
	_ = json.Unmarshal(p.Repo, &repo)

	out, err := json.Marshal(map[string]any{
		"pages": []map[string]string{{
			"page_name": p.Page,
			"action":    p.Action,
			"html_url":  strings.TrimRight(repo.HTMLURL, "/") + "/wiki/" + url.PathEscape(p.Page),
		}},
		"repository": p.Repo,
		"sender":     p.Sender,
	})
	if err != nil {
		return "", fmt.Errorf("encode gitea wiki: %w", err)
	}
	return renderGollum(loc, out)
}
