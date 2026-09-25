package service

import (
	"encoding/json"
	"regexp"
	"strings"
)

// ignoreFilter is one stored ignore rule. Kind selects what the pattern is
// matched against: author, branch, label or action.
type ignoreFilter struct {
	Kind    string
	Pattern string
}

// eventSubjects extracts the matchable subjects from a raw payload. Authors,
// branches and actions are single values; a payload carries many labels, so
// labels come back as a list. Missing subjects are empty and never match.
func eventSubjects(kind string, raw json.RawMessage) (author, branch string, labels []string, action string) {
	if strings.HasPrefix(kind, "gl_") {
		return gitlabSubjects(kind, raw)
	}

	var p struct {
		Ref    string `json:"ref"`
		Action string `json:"action"`
		Sender struct {
			Login string `json:"login"`
		} `json:"sender"`
		Pusher struct {
			Name string `json:"name"`
		} `json:"pusher"`
		Label struct {
			Name string `json:"name"`
		} `json:"label"`
		PullRequest struct {
			Head struct {
				Ref string `json:"ref"`
			} `json:"head"`
			Labels []struct {
				Name string `json:"name"`
			} `json:"labels"`
		} `json:"pull_request"`
		Issue struct {
			Labels []struct {
				Name string `json:"name"`
			} `json:"labels"`
		} `json:"issue"`
	}
	_ = json.Unmarshal(raw, &p)

	if p.Sender.Login != "" {
		author = p.Sender.Login
	} else {
		author = p.Pusher.Name
	}

	switch {
	case p.Ref != "":
		branch = strings.TrimPrefix(strings.TrimPrefix(p.Ref, "refs/heads/"), "refs/tags/")
	case p.PullRequest.Head.Ref != "":
		branch = p.PullRequest.Head.Ref
	}

	// The top-level label field belongs to the "labeled"/"unlabeled" events;
	// issues and pull requests carry their labels in a list.
	if p.Label.Name != "" {
		labels = append(labels, p.Label.Name)
	}
	for _, l := range p.PullRequest.Labels {
		labels = append(labels, l.Name)
	}
	for _, l := range p.Issue.Labels {
		labels = append(labels, l.Name)
	}

	return author, branch, labels, p.Action
}

// gitlabSubjects is eventSubjects for GitLab payloads, which name the same
// things differently: the actor is user (or user_username on a push), a
// merge request's branch is its source branch, a pipeline's is its ref.
func gitlabSubjects(kind string, raw json.RawMessage) (author, branch string, labels []string, action string) {
	var p struct {
		Ref          string `json:"ref"`
		UserUsername string `json:"user_username"`
		Action       string `json:"action"`
		Status       string `json:"status"`
		User         struct {
			Username string `json:"username"`
		} `json:"user"`
		Labels []struct {
			Title string `json:"title"`
		} `json:"labels"`
		ObjectAttributes struct {
			Action       string `json:"action"`
			Status       string `json:"status"`
			Ref          string `json:"ref"`
			SourceBranch string `json:"source_branch"`
		} `json:"object_attributes"`
		MergeRequest struct {
			SourceBranch string `json:"source_branch"`
		} `json:"merge_request"`
	}
	_ = json.Unmarshal(raw, &p)

	author = p.User.Username
	if author == "" {
		author = p.UserUsername
	}

	switch {
	case p.Ref != "":
		branch = strings.TrimPrefix(strings.TrimPrefix(p.Ref, "refs/heads/"), "refs/tags/")
	case p.ObjectAttributes.SourceBranch != "":
		branch = p.ObjectAttributes.SourceBranch
	case p.ObjectAttributes.Ref != "":
		branch = p.ObjectAttributes.Ref
	case p.MergeRequest.SourceBranch != "":
		branch = p.MergeRequest.SourceBranch
	}

	for _, l := range p.Labels {
		labels = append(labels, l.Title)
	}

	switch kind {
	case "gl_pipeline":
		action = p.ObjectAttributes.Status
	case "gl_deployment":
		action = p.Status
	case "gl_release":
		action = p.Action
	default:
		action = p.ObjectAttributes.Action
	}
	return author, branch, labels, action
}

// filterIgnored reports whether any rule suppresses this payload. Matching is
// case-insensitive; "*" matches any run of characters (including "/"), "?"
// matches a single character, and a bare pattern means equality.
func filterIgnored(kind string, raw json.RawMessage, filters []ignoreFilter) bool {
	author, branch, labels, action := eventSubjects(kind, raw)

	for _, f := range filters {
		switch f.Kind {
		case "author":
			if author != "" && globMatch(f.Pattern, author) {
				return true
			}
		case "branch":
			if branch != "" && globMatch(f.Pattern, branch) {
				return true
			}
		case "label":
			for _, label := range labels {
				if label != "" && globMatch(f.Pattern, label) {
					return true
				}
			}
		case "action":
			if action != "" && globMatch(f.Pattern, action) {
				return true
			}
		}
	}
	return false
}

// globMatch matches a pattern against a subject without treating "/" as a
// separator: a branch name like "renovate/deps" is one string, so "renovate*"
// must reach across the slash. A malformed pattern simply never matches
// rather than breaking delivery.
func globMatch(pattern, subject string) bool {
	pattern = strings.ToLower(pattern)
	subject = strings.ToLower(subject)
	if !strings.ContainsAny(pattern, "*?") {
		return pattern == subject
	}

	var b strings.Builder
	b.WriteString("^")
	for _, r := range pattern {
		switch r {
		case '*':
			b.WriteString(".*")
		case '?':
			b.WriteString(".")
		default:
			b.WriteString(regexp.QuoteMeta(string(r)))
		}
	}
	b.WriteString("$")

	matched, err := regexp.MatchString(b.String(), subject)
	return err == nil && matched
}
