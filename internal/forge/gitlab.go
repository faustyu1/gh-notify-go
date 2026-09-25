package forge

import (
	"encoding/json"
	"fmt"
	"net/http"
)

// GitLab kinds carry a gl_ prefix: a GitLab payload has nothing in common
// with the GitHub one of a similar name, so they render and toggle
// separately.
const (
	KindGitLabPush         = "gl_push"
	KindGitLabTagPush      = "gl_tag_push"
	KindGitLabMergeRequest = "gl_merge_request"
	KindGitLabIssue        = "gl_issue"
	KindGitLabNote         = "gl_note"
	KindGitLabPipeline     = "gl_pipeline"
	KindGitLabRelease      = "gl_release"
	KindGitLabWikiPage     = "gl_wiki_page"
	KindGitLabDeployment   = "gl_deployment"
)

// gitlabKinds maps GitLab's object_kind onto ours. Anything else (job
// events, feature flags, emoji reactions…) is not delivered.
var gitlabKinds = map[string]string{
	"push":          KindGitLabPush,
	"tag_push":      KindGitLabTagPush,
	"merge_request": KindGitLabMergeRequest,
	"issue":         KindGitLabIssue,
	"note":          KindGitLabNote,
	"pipeline":      KindGitLabPipeline,
	"release":       KindGitLabRelease,
	"wiki_page":     KindGitLabWikiPage,
	"deployment":    KindGitLabDeployment,
}

// GitLab is connected by a project or group webhook authenticated by its
// secret token.
var GitLab Hook = gitlab{}

type gitlab struct{}

func (gitlab) ID() string         { return "gitlab" }
func (gitlab) Name() string       { return "GitLab" }
func (gitlab) KindPrefix() string { return "gl_" }
func (gitlab) Auth() AuthMode     { return AuthToken }

func (gitlab) Token(h http.Header) string { return h.Get("X-Gitlab-Token") }

func (gitlab) Verify(string, http.Header, []byte) bool { return false }

type gitlabShape struct {
	ObjectKind string `json:"object_kind"`
	// push and tag_push carry the id at the top level, release and
	// deployment only inside project.
	ProjectID int64 `json:"project_id"`
	Project   *struct {
		ID                int64  `json:"id"`
		PathWithNamespace string `json:"path_with_namespace"`
		WebURL            string `json:"web_url"`
	} `json:"project"`
	Action           string `json:"action"`
	Status           string `json:"status"`
	ObjectAttributes struct {
		Action string `json:"action"`
		Status string `json:"status"`
	} `json:"object_attributes"`
}

// Parse decodes a delivery. The delivery id comes from GitLab's headers when
// it sends one, and from the body's hash otherwise.
func (gitlab) Parse(header http.Header, body []byte) (Envelope, error) {
	var shape gitlabShape
	if err := json.Unmarshal(body, &shape); err != nil {
		return Envelope{}, fmt.Errorf("parse envelope: %w", err)
	}

	env := Envelope{
		DeliveryID: gitlabDeliveryID(header, body),
		Kind:       gitlabKinds[shape.ObjectKind],
		ProjectID:  shape.ProjectID,
		Raw:        json.RawMessage(body),
	}
	if shape.Project != nil {
		if shape.Project.ID != 0 {
			env.ProjectID = shape.Project.ID
		}
		env.ProjectPath = shape.Project.PathWithNamespace
		env.ProjectURL = shape.Project.WebURL
	}
	if env.ProjectID == 0 || env.ProjectPath == "" {
		return Envelope{}, fmt.Errorf("parse envelope: no project")
	}

	// What the action filter and the "action" ignore rule match against.
	switch env.Kind {
	case KindGitLabMergeRequest, KindGitLabIssue, KindGitLabWikiPage:
		env.Action = shape.ObjectAttributes.Action
	case KindGitLabPipeline:
		env.Action = shape.ObjectAttributes.Status
	case KindGitLabDeployment:
		env.Action = shape.Status
	case KindGitLabRelease:
		env.Action = shape.Action
	}
	return env, nil
}

func gitlabDeliveryID(header http.Header, body []byte) string {
	for _, name := range []string{"X-Gitlab-Event-UUID", "Idempotency-Key"} {
		if v := header.Get(name); v != "" {
			return "gitlab:" + v
		}
	}
	return bodyID("gitlab", body)
}

// Subjects reads GitLab payloads, which name the same things differently: the
// actor is user (or user_username on a push), a merge request's branch is its
// source branch, a pipeline's is its ref.
func (gitlab) Subjects(kind string, raw json.RawMessage) Subjects {
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

	s := Subjects{Author: p.User.Username}
	if s.Author == "" {
		s.Author = p.UserUsername
	}

	switch {
	case p.Ref != "":
		s.Branch = trimRef(p.Ref)
	case p.ObjectAttributes.SourceBranch != "":
		s.Branch = p.ObjectAttributes.SourceBranch
	case p.ObjectAttributes.Ref != "":
		s.Branch = p.ObjectAttributes.Ref
	case p.MergeRequest.SourceBranch != "":
		s.Branch = p.MergeRequest.SourceBranch
	}

	for _, l := range p.Labels {
		s.Labels = append(s.Labels, l.Title)
	}

	switch kind {
	case KindGitLabPipeline:
		s.Action = p.ObjectAttributes.Status
	case KindGitLabDeployment:
		s.Action = p.Status
	case KindGitLabRelease:
		s.Action = p.Action
	default:
		s.Action = p.ObjectAttributes.Action
	}
	return s
}
