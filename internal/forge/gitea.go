package forge

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// Gitea, its fork Forgejo and GitVerse (built on Gitea) send the same
// payloads, modelled on GitHub's. They share the gt_ kinds and differ only in
// their name, their header names and how a delivery authenticates.
const (
	KindGiteaPush              = "gt_push"
	KindGiteaCreate            = "gt_create"
	KindGiteaDelete            = "gt_delete"
	KindGiteaPullRequest       = "gt_pull_request"
	KindGiteaPullRequestReview = "gt_pull_request_review"
	KindGiteaIssues            = "gt_issues"
	KindGiteaIssueComment      = "gt_issue_comment"
	KindGiteaRelease           = "gt_release"
	KindGiteaWiki              = "gt_wiki"
)

// giteaKinds maps the event header onto ours. Gitea names review events
// after their verdict, and newer versions spell them with "review" in
// between.
var giteaKinds = map[string]string{
	"push":                         KindGiteaPush,
	"create":                       KindGiteaCreate,
	"delete":                       KindGiteaDelete,
	"pull_request":                 KindGiteaPullRequest,
	"pull_request_approved":        KindGiteaPullRequestReview,
	"pull_request_rejected":        KindGiteaPullRequestReview,
	"pull_request_comment":         KindGiteaPullRequestReview,
	"pull_request_review_approved": KindGiteaPullRequestReview,
	"pull_request_review_rejected": KindGiteaPullRequestReview,
	"pull_request_review_comment":  KindGiteaPullRequestReview,
	"pull_request_review":          KindGiteaPullRequestReview,
	"issues":                       KindGiteaIssues,
	"issue_comment":                KindGiteaIssueComment,
	"release":                      KindGiteaRelease,
	"wiki":                         KindGiteaWiki,
}

// Every Gitea-family host sends its own header set, and Gitea adds GitHub's
// for compatibility. The first one present wins.
var (
	giteaEventHeaders = []string{
		"X-Gitea-Event", "X-Forgejo-Event", "X-Gitverse-Event", "X-Gogs-Event", "X-GitHub-Event",
	}
	giteaDeliveryHeaders = []string{
		"X-Gitea-Delivery", "X-Forgejo-Delivery", "X-Gitverse-Delivery",
		"X-Gogs-Delivery", "X-GitHub-Delivery",
	}
	giteaSignatureHeaders = []string{
		"X-Gitea-Signature", "X-Forgejo-Signature", "X-Gogs-Signature",
	}
)

var (
	// Gitea is connected by a repository or organization webhook signed with
	// its secret.
	Gitea Hook = giteaFamily{id: "gitea", name: "Gitea", auth: AuthSignature}
	// Forgejo, and Codeberg on it, sign their deliveries like Gitea does.
	Forgejo Hook = giteaFamily{id: "forgejo", name: "Forgejo", auth: AuthSignature}
	// GitVerse sends no signature: its webhooks carry an Authorization header
	// instead, which works like GitLab's token.
	GitVerse Hook = giteaFamily{id: "gitverse", name: "GitVerse", auth: AuthToken}
)

type giteaFamily struct {
	id, name string
	auth     AuthMode
}

func (g giteaFamily) ID() string       { return g.id }
func (g giteaFamily) Name() string     { return g.name }
func (giteaFamily) KindPrefix() string { return "gt_" }
func (g giteaFamily) Auth() AuthMode   { return g.auth }

// Token accepts the Authorization header with or without a scheme: GitVerse
// sends whatever the user typed into the field.
func (giteaFamily) Token(h http.Header) string {
	v := strings.TrimSpace(h.Get("Authorization"))
	if scheme, rest, ok := strings.Cut(v, " "); ok {
		switch strings.ToLower(scheme) {
		case "bearer", "token":
			return strings.TrimSpace(rest)
		}
	}
	return v
}

func (giteaFamily) Verify(secret string, h http.Header, body []byte) bool {
	for _, name := range giteaSignatureHeaders {
		if sig := h.Get(name); sig != "" {
			return validHMAC(secret, body, sig)
		}
	}
	// Gitea also signs in GitHub's format.
	if sig, ok := strings.CutPrefix(h.Get("X-Hub-Signature-256"), "sha256="); ok {
		return validHMAC(secret, body, sig)
	}
	return false
}

type giteaShape struct {
	Action      string          `json:"action"`
	Ref         string          `json:"ref"`
	RefType     string          `json:"ref_type"`
	Commits     json.RawMessage `json:"commits"`
	PullRequest json.RawMessage `json:"pull_request"`
	Issue       json.RawMessage `json:"issue"`
	Comment     json.RawMessage `json:"comment"`
	Release     json.RawMessage `json:"release"`
	Review      *struct {
		Type string `json:"type"`
	} `json:"review"`
	Page       string `json:"page"`
	Repository *struct {
		ID       int64  `json:"id"`
		FullName string `json:"full_name"`
		HTMLURL  string `json:"html_url"`
	} `json:"repository"`
}

func (g giteaFamily) Parse(header http.Header, body []byte) (Envelope, error) {
	var shape giteaShape
	if err := json.Unmarshal(body, &shape); err != nil {
		return Envelope{}, fmt.Errorf("parse envelope: %w", err)
	}
	if shape.Repository == nil || shape.Repository.ID == 0 || shape.Repository.FullName == "" {
		return Envelope{}, fmt.Errorf("parse envelope: no repository")
	}

	event := firstHeader(header, giteaEventHeaders)
	if event == "" {
		event = guessGiteaEvent(shape)
	}

	env := Envelope{
		DeliveryID:  g.deliveryID(header, body),
		Kind:        giteaKinds[event],
		ProjectID:   shape.Repository.ID,
		ProjectPath: shape.Repository.FullName,
		ProjectURL:  shape.Repository.HTMLURL,
		Raw:         json.RawMessage(body),
	}
	switch env.Kind {
	case KindGiteaPush, KindGiteaCreate, KindGiteaDelete:
	case KindGiteaPullRequestReview:
		env.Action = reviewVerdict(shape)
	default:
		env.Action = shape.Action
	}
	return env, nil
}

// guessGiteaEvent names the event of a delivery that came without an event
// header, from the fields only that event carries. A ref deletion looks like
// a creation, so it is read as one.
func guessGiteaEvent(s giteaShape) string {
	switch {
	case len(s.Commits) > 0 && s.Ref != "":
		return "push"
	case s.RefType != "":
		return "create"
	case s.Review != nil && len(s.PullRequest) > 0:
		return "pull_request_review"
	case len(s.PullRequest) > 0 && string(s.PullRequest) != "null":
		return "pull_request"
	case len(s.Comment) > 0 && len(s.Issue) > 0:
		return "issue_comment"
	case len(s.Issue) > 0:
		return "issues"
	case len(s.Release) > 0:
		return "release"
	case s.Page != "":
		return "wiki"
	}
	return ""
}

// reviewVerdict is the action of a review: Gitea sends "reviewed" for every
// one and puts the verdict in the review's type.
func reviewVerdict(s giteaShape) string {
	if s.Review == nil {
		return s.Action
	}
	switch t := s.Review.Type; {
	case strings.HasSuffix(t, "approved"):
		return "approved"
	case strings.HasSuffix(t, "rejected"):
		return "rejected"
	case strings.HasSuffix(t, "comment"):
		return "comment"
	}
	return s.Action
}

func (g giteaFamily) deliveryID(header http.Header, body []byte) string {
	if v := firstHeader(header, giteaDeliveryHeaders); v != "" {
		return g.id + ":" + v
	}
	return bodyID(g.id, body)
}

func (giteaFamily) Subjects(kind string, raw json.RawMessage) Subjects {
	s := githubSubjects(raw)
	if kind == KindGiteaPullRequestReview {
		var shape giteaShape
		_ = json.Unmarshal(raw, &shape)
		s.Action = reviewVerdict(shape)
	}
	return s
}

func firstHeader(h http.Header, names []string) string {
	for _, name := range names {
		if v := h.Get(name); v != "" {
			return v
		}
	}
	return ""
}
