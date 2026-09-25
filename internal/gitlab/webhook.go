// Package gitlab holds what is specific to GitLab webhooks: turning a
// delivery into the envelope the ingest path routes on.
package gitlab

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
)

// Event kinds carry a gl_ prefix: a GitLab payload has nothing in common with
// the GitHub one of a similar name, so they render and toggle separately.
const (
	KindPush         = "gl_push"
	KindTagPush      = "gl_tag_push"
	KindMergeRequest = "gl_merge_request"
	KindIssue        = "gl_issue"
	KindNote         = "gl_note"
	KindPipeline     = "gl_pipeline"
	KindRelease      = "gl_release"
	KindWikiPage     = "gl_wiki_page"
	KindDeployment   = "gl_deployment"
)

// kinds maps GitLab's object_kind onto ours. Anything else (job events,
// feature flags, emoji reactions…) is not delivered.
var kinds = map[string]string{
	"push":          KindPush,
	"tag_push":      KindTagPush,
	"merge_request": KindMergeRequest,
	"issue":         KindIssue,
	"note":          KindNote,
	"pipeline":      KindPipeline,
	"release":       KindRelease,
	"wiki_page":     KindWikiPage,
	"deployment":    KindDeployment,
}

// Envelope is the subset of a GitLab delivery the router needs. Kind is empty
// for an object kind the bot does not deliver; the project is still worth
// remembering then, because it proves the webhook works.
type Envelope struct {
	DeliveryID  string
	Kind        string
	Action      string
	ProjectID   int64
	ProjectPath string
	ProjectURL  string
	Raw         json.RawMessage
}

type envelopeShape struct {
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

// ParseEnvelope decodes a delivery. The delivery id comes from GitLab's
// headers when it sends one, and from the body's hash otherwise, so a manual
// resend is still recognised as the same delivery.
func ParseEnvelope(header http.Header, body []byte) (Envelope, error) {
	var shape envelopeShape
	if err := json.Unmarshal(body, &shape); err != nil {
		return Envelope{}, fmt.Errorf("parse envelope: %w", err)
	}

	env := Envelope{
		DeliveryID: deliveryID(header, body),
		Kind:       kinds[shape.ObjectKind],
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
	case KindMergeRequest, KindIssue, KindWikiPage:
		env.Action = shape.ObjectAttributes.Action
	case KindPipeline:
		env.Action = shape.ObjectAttributes.Status
	case KindDeployment:
		env.Action = shape.Status
	case KindRelease:
		env.Action = shape.Action
	}
	return env, nil
}

func deliveryID(header http.Header, body []byte) string {
	for _, name := range []string{"X-Gitlab-Event-UUID", "Idempotency-Key"} {
		if v := header.Get(name); v != "" {
			return "gitlab:" + v
		}
	}
	sum := sha256.Sum256(body)
	return "gitlab:sha256:" + hex.EncodeToString(sum[:])
}
