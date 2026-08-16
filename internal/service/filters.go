package service

import (
	"encoding/json"
	"path"
	"strings"
)

// ignoreFilter is one stored ignore rule. Kind selects what the pattern is
// matched against: author, branch or label.
type ignoreFilter struct {
	Kind    string
	Pattern string
}

// eventSubjects extracts the three matchable subjects from a raw payload.
// Missing subjects are empty strings and never match.
func eventSubjects(kind string, raw json.RawMessage) (author, branch, label string) {
	var p struct {
		Ref      string `json:"ref"`
		Action   string `json:"action"`
		Repo     struct {
			FullName string `json:"full_name"`
		} `json:"repository"`
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
		} `json:"pull_request"`
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

	label = p.Label.Name
	return author, branch, label
}

// filterIgnored reports whether any rule suppresses this payload. Matching is
// case-insensitive; "*" wildcards work, a bare pattern means equality.
func filterIgnored(kind string, raw json.RawMessage, filters []ignoreFilter) bool {
	author, branch, label := eventSubjects(kind, raw)

	for _, f := range filters {
		var subject string
		switch f.Kind {
		case "author":
			subject = author
		case "branch":
			subject = branch
		case "label":
			subject = label
		default:
			continue
		}
		if subject == "" {
			continue
		}
		if globMatch(f.Pattern, subject) {
			return true
		}
	}
	return false
}

func globMatch(pattern, subject string) bool {
	pattern = strings.ToLower(pattern)
	subject = strings.ToLower(subject)
	if !strings.ContainsAny(pattern, "*?[") {
		return pattern == subject
	}
	// path.Match errors only on malformed patterns; a malformed rule simply
	// never matches rather than breaking delivery.
	matched, err := path.Match(pattern, subject)
	return err == nil && matched
}
