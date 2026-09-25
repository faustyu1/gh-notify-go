package service

import (
	"encoding/json"
	"regexp"
	"strings"

	"github.com/faustyu/gh-notify-go/internal/forge"
)

// ignoreFilter is one stored ignore rule. Kind selects what the pattern is
// matched against: author, branch, label or action.
type ignoreFilter struct {
	Kind    string
	Pattern string
}

// eventSubjects extracts the matchable subjects from a raw payload, read the
// way the kind's provider names them. Authors, branches and actions are
// single values; a payload carries many labels, so labels come back as a
// list. Missing subjects are empty and never match.
func eventSubjects(kind string, raw json.RawMessage) (author, branch string, labels []string, action string) {
	s := forge.ForKind(kind).Subjects(kind, raw)
	return s.Author, s.Branch, s.Labels, s.Action
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
