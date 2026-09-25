package events

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/faustyu/gh-notify-go/internal/events/render"
	"github.com/faustyu/gh-notify-go/internal/forge"
	"github.com/faustyu/gh-notify-go/internal/i18n"
)

// GitLab payloads share their project and user blocks across kinds; the
// renderers below reuse the GitHub wording wherever the sentence is the same
// and carry their own keys (ev.gitlab.*) where GitLab names things
// differently — a merge request is not a pull request.

type glProject struct {
	PathWithNamespace string `json:"path_with_namespace"`
	WebURL            string `json:"web_url"`
}

type glUser struct {
	Name     string `json:"name"`
	Username string `json:"username"`
}

// glZeroSHA is what GitLab puts in before/after for a created or deleted ref.
const glZeroSHA = "0000000000000000000000000000000000000000"

func init() {
	Register(forge.KindGitLabPush, nil, renderGitLabPush)
	Register(forge.KindGitLabTagPush, nil, renderGitLabTagPush)
	Register(forge.KindGitLabMergeRequest,
		ActionFilter{"open", "close", "reopen", "merge", "approved"}, renderGitLabMergeRequest)
	Register(forge.KindGitLabIssue, ActionFilter{"open", "close", "reopen"}, renderGitLabIssue)
	Register(forge.KindGitLabNote, nil, renderGitLabNote)
	Register(forge.KindGitLabPipeline,
		ActionFilter{"success", "failed", "canceled"}, renderGitLabPipeline)
	Register(forge.KindGitLabRelease, ActionFilter{"create"}, renderGitLabRelease)
	Register(forge.KindGitLabWikiPage, nil, renderGitLabWikiPage)
	Register(forge.KindGitLabDeployment, ActionFilter{"success", "failed"}, renderGitLabDeployment)
}

// glHost is the instance's base URL, taken from the project URL so
// self-managed instances link to themselves rather than to gitlab.com.
func glHost(p glProject) string {
	return strings.TrimSuffix(strings.TrimSuffix(p.WebURL, p.PathWithNamespace), "/")
}

// glUserLink links a username to its profile on the project's instance.
func glUserLink(p glProject, username, name string) string {
	if username == "" {
		return render.Escape(name)
	}
	return render.Link(glHost(p)+"/"+username, username)
}

func glHeader(emoji string, p glProject) string {
	return emoji + " <b>" + render.Escape(p.PathWithNamespace) + "</b>\n"
}

type glPushPayload struct {
	Before       string    `json:"before"`
	After        string    `json:"after"`
	Ref          string    `json:"ref"`
	UserName     string    `json:"user_name"`
	UserUsername string    `json:"user_username"`
	Project      glProject `json:"project"`
	Commits      []struct {
		ID      string `json:"id"`
		Message string `json:"message"`
		URL     string `json:"url"`
		Author  struct {
			Name string `json:"name"`
		} `json:"author"`
	} `json:"commits"`
	TotalCommitsCount int `json:"total_commits_count"`
}

func renderGitLabPush(loc *i18n.Localizer, raw json.RawMessage) (string, error) {
	var p glPushPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return "", fmt.Errorf("parse gitlab push: %w", err)
	}

	branch := "<code>" + render.Escape(strings.TrimPrefix(p.Ref, "refs/heads/")) + "</code>"
	user := glUserLink(p.Project, p.UserUsername, p.UserName)

	// A deleted branch arrives as a push with no commits and a zero "after".
	if p.After == glZeroSHA {
		return glHeader(render.Emoji(render.EmojiTrash, "🗑"), p.Project) +
			loc.T("ev.ref.branch_deleted", "user", user, "ref", branch), nil
	}

	var b strings.Builder
	b.WriteString(glHeader(render.Emoji(render.EmojiUpload, "⬆"), p.Project))

	total := max(p.TotalCommitsCount, len(p.Commits))
	if p.Before == glZeroSHA {
		b.WriteString(loc.T("ev.ref.branch_created", "user", user, "ref", branch))
		if total > 0 {
			b.WriteString(" — " + loc.T("ev.push.commits", "n", total))
		}
	} else {
		b.WriteString(loc.T("ev.push.pushed",
			"user", user,
			"branch", branch,
			"commits", loc.T("ev.push.commits", "n", total),
		))
	}
	b.WriteString("\n")

	shown := p.Commits
	if len(shown) > maxCommitsListed {
		shown = shown[:maxCommitsListed]
	}
	if len(shown) > 0 {
		b.WriteString("\n<blockquote>")
	}
	for _, c := range shown {
		title, _, _ := strings.Cut(c.Message, "\n")
		author := ""
		if c.Author.Name != "" {
			author = " " + loc.T("ev.push.commit_author", "user", render.Escape(c.Author.Name))
		}
		b.WriteString("\n" + loc.T("ev.push.commit_line",
			"sha", render.Link(c.URL, shortSHA(c.ID)),
			"author", author,
			"title", render.Escape(render.Truncate(title, 72)),
		))
	}
	if len(shown) > 0 {
		b.WriteString("\n</blockquote>")
	}
	if omitted := total - len(shown); omitted > 0 {
		b.WriteString("\n" + loc.T("ev.push.omitted", "n", omitted))
	}

	if p.Before != glZeroSHA && p.Before != "" && p.Project.WebURL != "" {
		b.WriteString("\n" + render.Link(
			p.Project.WebURL+"/-/compare/"+p.Before+"..."+p.After, loc.T("ev.push.compare")))
	}
	return b.String(), nil
}

func renderGitLabTagPush(loc *i18n.Localizer, raw json.RawMessage) (string, error) {
	var p glPushPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return "", fmt.Errorf("parse gitlab tag_push: %w", err)
	}

	tag := strings.TrimPrefix(p.Ref, "refs/tags/")
	user := glUserLink(p.Project, p.UserUsername, p.UserName)
	if p.After == glZeroSHA {
		return glHeader(render.Emoji(render.EmojiTrash, "🗑"), p.Project) +
			loc.T("ev.ref.tag_deleted", "user", user,
				"ref", "<code>"+render.Escape(tag)+"</code>"), nil
	}
	ref := "<code>" + render.Escape(tag) + "</code>"
	if p.Project.WebURL != "" {
		ref = render.Link(p.Project.WebURL+"/-/tags/"+tag, tag)
	}
	return glHeader(render.Emoji(render.EmojiTag, "🏷"), p.Project) +
		loc.T("ev.ref.tag_created", "user", user, "ref", ref), nil
}

type glMergeRequestPayload struct {
	User             glUser    `json:"user"`
	Project          glProject `json:"project"`
	ObjectAttributes struct {
		IID          int    `json:"iid"`
		Title        string `json:"title"`
		Description  string `json:"description"`
		URL          string `json:"url"`
		Action       string `json:"action"`
		SourceBranch string `json:"source_branch"`
		TargetBranch string `json:"target_branch"`
		Draft        bool   `json:"draft"`
		WIP          bool   `json:"work_in_progress"`
	} `json:"object_attributes"`
}

func renderGitLabMergeRequest(loc *i18n.Localizer, raw json.RawMessage) (string, error) {
	var p glMergeRequestPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return "", fmt.Errorf("parse gitlab merge_request: %w", err)
	}
	mr := p.ObjectAttributes

	emoji, key := render.Emoji(render.EmojiWrite, "✍"), "ev.gitlab.merge_request.opened"
	switch {
	case mr.Action == "merge":
		emoji, key = render.Emoji(render.EmojiCheck, "✅"), "ev.gitlab.merge_request.merged"
	case mr.Action == "close":
		emoji, key = render.Emoji(render.EmojiCross, "❌"), "ev.gitlab.merge_request.closed"
	case mr.Action == "reopen":
		emoji, key = render.Emoji(render.EmojiLockOpen, "🔓"), "ev.gitlab.merge_request.reopened"
	case mr.Action == "approved":
		emoji, key = render.Emoji(render.EmojiCheck, "✅"), "ev.gitlab.merge_request.approved"
	case mr.Draft || mr.WIP:
		emoji, key = render.Emoji(render.EmojiPencil, "🖋"), "ev.gitlab.merge_request.draft"
	}

	var b strings.Builder
	b.WriteString(glHeader(emoji, p.Project))
	b.WriteString(loc.T(key,
		"user", glUserLink(p.Project, p.User.Username, p.User.Name),
		"link", render.Link(mr.URL, fmt.Sprintf("!%d", mr.IID)),
	))
	b.WriteString("\n\n<blockquote>\n")
	b.WriteString("<b>" + render.Escape(render.Truncate(mr.Title, 120)) + "</b>\n")
	b.WriteString(loc.T("ev.pull_request.ref_pair",
		"head", render.Escape(mr.SourceBranch),
		"base", render.Escape(mr.TargetBranch),
	))
	b.WriteString("\n</blockquote>")
	return b.String(), nil
}

type glIssuePayload struct {
	User             glUser    `json:"user"`
	Project          glProject `json:"project"`
	ObjectAttributes struct {
		IID         int    `json:"iid"`
		Title       string `json:"title"`
		Description string `json:"description"`
		URL         string `json:"url"`
		Action      string `json:"action"`
	} `json:"object_attributes"`
}

func renderGitLabIssue(loc *i18n.Localizer, raw json.RawMessage) (string, error) {
	var p glIssuePayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return "", fmt.Errorf("parse gitlab issue: %w", err)
	}
	issue := p.ObjectAttributes

	// GitHub's issue wording fits as is; only the action names differ.
	action := map[string]string{"open": "opened", "close": "closed", "reopen": "reopened"}[issue.Action]
	emoji, key := issuesHeadline(action)

	var b strings.Builder
	b.WriteString(glHeader(emoji, p.Project))
	b.WriteString(loc.T(key,
		"user", glUserLink(p.Project, p.User.Username, p.User.Name),
		"link", render.Link(issue.URL, fmt.Sprintf("#%d", issue.IID)),
	))
	b.WriteString("\n\n<b>" + render.Escape(render.Truncate(issue.Title, 120)) + "</b>")
	if action == "opened" {
		if body := strings.TrimSpace(issue.Description); body != "" {
			b.WriteString("\n\n" + render.Markdown(body, 500))
		}
	}
	return b.String(), nil
}

type glNotePayload struct {
	User             glUser    `json:"user"`
	Project          glProject `json:"project"`
	ObjectAttributes struct {
		Note         string `json:"note"`
		NoteableType string `json:"noteable_type"`
		URL          string `json:"url"`
	} `json:"object_attributes"`
	MergeRequest *struct {
		IID   int    `json:"iid"`
		Title string `json:"title"`
	} `json:"merge_request"`
	Issue *struct {
		IID   int    `json:"iid"`
		Title string `json:"title"`
	} `json:"issue"`
	Commit *struct {
		ID string `json:"id"`
	} `json:"commit"`
	Snippet *struct {
		Title string `json:"title"`
	} `json:"snippet"`
}

func renderGitLabNote(loc *i18n.Localizer, raw json.RawMessage) (string, error) {
	var p glNotePayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return "", fmt.Errorf("parse gitlab note: %w", err)
	}
	note := p.ObjectAttributes
	user := glUserLink(p.Project, p.User.Username, p.User.Name)

	var line string
	switch {
	case note.NoteableType == "MergeRequest" && p.MergeRequest != nil:
		line = loc.T("ev.gitlab.note.merge_request", "user", user,
			"link", render.Link(note.URL, fmt.Sprintf("!%d «%s»",
				p.MergeRequest.IID, render.Truncate(p.MergeRequest.Title, 60))))
	case note.NoteableType == "Issue" && p.Issue != nil:
		line = loc.T("ev.issue_comment.line", "user", user,
			"link", render.Link(note.URL, fmt.Sprintf("#%d «%s»",
				p.Issue.IID, render.Truncate(p.Issue.Title, 60))))
	case note.NoteableType == "Commit" && p.Commit != nil:
		line = loc.T("ev.commit_comment.line", "user", user,
			"sha", render.Link(note.URL, shortSHA(p.Commit.ID)))
	default:
		title := ""
		if p.Snippet != nil {
			title = p.Snippet.Title
		}
		line = loc.T("ev.gitlab.note.snippet", "user", user,
			"link", render.Link(note.URL, render.Truncate(title, 60)))
	}

	return glHeader(render.Emoji(render.EmojiMegaphone, "💬"), p.Project) + line +
		"\n\n" + render.Markdown(note.Note, 300), nil
}

type glPipelinePayload struct {
	User             glUser    `json:"user"`
	Project          glProject `json:"project"`
	ObjectAttributes struct {
		ID     int64  `json:"id"`
		Ref    string `json:"ref"`
		Status string `json:"status"`
		URL    string `json:"url"`
	} `json:"object_attributes"`
	Commit struct {
		Message string `json:"message"`
		URL     string `json:"url"`
	} `json:"commit"`
}

func renderGitLabPipeline(loc *i18n.Localizer, raw json.RawMessage) (string, error) {
	var p glPipelinePayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return "", fmt.Errorf("parse gitlab pipeline: %w", err)
	}
	pl := p.ObjectAttributes

	emoji, verdict := "⚙", loc.T("ev.verdict.finished")
	switch pl.Status {
	case "success":
		emoji, verdict = "✅", loc.T("ev.verdict.success")
	case "failed", "canceled":
		emoji, verdict = "❌", loc.T("ev.gitlab.pipeline."+pl.Status)
	}

	url := pl.URL
	if url == "" && p.Project.WebURL != "" {
		url = fmt.Sprintf("%s/-/pipelines/%d", p.Project.WebURL, pl.ID)
	}
	title, _, _ := strings.Cut(p.Commit.Message, "\n")

	var b strings.Builder
	b.WriteString(glHeader(render.Emoji(render.EmojiCode, emoji), p.Project))
	b.WriteString(loc.T("ev.gitlab.pipeline.line",
		"id", pl.ID,
		"verdict", verdict,
		"branch", render.Escape(pl.Ref),
	))
	if p.User.Username != "" {
		b.WriteString("\n" + loc.T("ev.workflow_run.by",
			"user", render.Link(glHost(p.Project)+"/"+p.User.Username, "@"+p.User.Username)))
	}
	if title != "" {
		b.WriteString("\n\n" + render.Link(p.Commit.URL, render.Truncate(title, 72)))
	}
	b.WriteString("\n" + render.Link(url, loc.T("ev.gitlab.pipeline.view")))
	return b.String(), nil
}

type glReleasePayload struct {
	Name        string    `json:"name"`
	Tag         string    `json:"tag"`
	Description string    `json:"description"`
	URL         string    `json:"url"`
	Project     glProject `json:"project"`
}

func renderGitLabRelease(loc *i18n.Localizer, raw json.RawMessage) (string, error) {
	var p glReleasePayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return "", fmt.Errorf("parse gitlab release: %w", err)
	}

	title := p.Name
	if title == "" {
		title = p.Tag
	}

	// A release hook carries no author, so the sentence has no subject.
	var b strings.Builder
	b.WriteString(glHeader(render.Emoji(render.EmojiParty, "🚀"), p.Project))
	b.WriteString(loc.T("ev.gitlab.release.line",
		"link", render.Link(p.URL, render.Truncate(title, 80))))
	b.WriteString("\n")
	if body := strings.TrimSpace(p.Description); body != "" {
		b.WriteString("\n" + render.Markdown(body, 300))
	}
	return b.String(), nil
}

type glWikiPagePayload struct {
	User             glUser    `json:"user"`
	Project          glProject `json:"project"`
	ObjectAttributes struct {
		Title  string `json:"title"`
		URL    string `json:"url"`
		Action string `json:"action"`
	} `json:"object_attributes"`
}

func renderGitLabWikiPage(loc *i18n.Localizer, raw json.RawMessage) (string, error) {
	var p glWikiPagePayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return "", fmt.Errorf("parse gitlab wiki_page: %w", err)
	}
	page := p.ObjectAttributes

	// Worded like GitHub's gollum event, whose actions are created/edited.
	action := map[string]string{"create": "created", "update": "edited", "delete": "deleted"}[page.Action]
	if action == "" {
		action = page.Action
	}

	return glHeader(render.Emoji(render.EmojiFont, "📖"), p.Project) +
		loc.T("ev.gollum.line", "user", glUserLink(p.Project, p.User.Username, p.User.Name)) +
		"\n• " + render.Link(page.URL, render.Truncate(loc.T("ev.gollum.page",
		"name", page.Title, "action", action), 80)), nil
}

type glDeploymentPayload struct {
	Status        string    `json:"status"`
	Environment   string    `json:"environment"`
	DeployableURL string    `json:"deployable_url"`
	User          glUser    `json:"user"`
	Project       glProject `json:"project"`
}

func renderGitLabDeployment(loc *i18n.Localizer, raw json.RawMessage) (string, error) {
	var p glDeploymentPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return "", fmt.Errorf("parse gitlab deployment: %w", err)
	}

	env := render.Escape(p.Environment)
	emoji, verdict := render.Emoji(render.EmojiCheck, "✅"),
		loc.T("ev.deployment_status.success", "env", env)
	if p.Status != "success" {
		emoji, verdict = render.Emoji(render.EmojiCross, "❌"),
			loc.T("ev.deployment_status.failure", "env", env)
	}

	var b strings.Builder
	b.WriteString(glHeader(emoji, p.Project))
	b.WriteString(verdict)
	if p.User.Username != "" {
		b.WriteString("\n" + loc.T("ev.workflow_run.by",
			"user", render.Link(glHost(p.Project)+"/"+p.User.Username, "@"+p.User.Username)))
	}
	if p.DeployableURL != "" {
		b.WriteString("\n" + render.Link(p.DeployableURL, loc.T("ev.report")))
	}
	return b.String(), nil
}
