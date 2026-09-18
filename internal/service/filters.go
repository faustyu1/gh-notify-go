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
