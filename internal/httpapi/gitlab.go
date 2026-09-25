package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"

	"github.com/faustyu/gh-notify-go/internal/gitlab"
	"github.com/faustyu/gh-notify-go/internal/service"
	"github.com/faustyu/gh-notify-go/internal/storage"
)

// GitLabTokens authenticates a GitLab delivery by its secret token.
type GitLabTokens interface {
	GitLabInstallationByToken(ctx context.Context, token string) (int64, error)
}

// GitLabIngest is the ingest step a GitLab delivery feeds.
type GitLabIngest interface {
	HandleGitLab(ctx context.Context, installationID int64, env gitlab.Envelope) (service.Result, error)
}

// NewGitLabWebhookHandler serves every GitLab connection on one URL: the
// X-Gitlab-Token header both authenticates the delivery and names the
// connection it belongs to.
func NewGitLabWebhookHandler(tokens GitLabTokens, ingest GitLabIngest) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		// Token first: nothing else touches unauthenticated input.
		installationID, err := tokens.GitLabInstallationByToken(r.Context(),
			r.Header.Get("X-Gitlab-Token"))
		if err != nil {
			if errors.Is(err, storage.ErrUnknownWebhookToken) {
				http.Error(w, "bad token", http.StatusUnauthorized)
				return
			}
			slog.Error("gitlab token lookup failed", "error", err)
			http.Error(w, "lookup failed", http.StatusInternalServerError)
			return
		}

		body, err := io.ReadAll(io.LimitReader(r.Body, maxBodyBytes))
		if err != nil {
			http.Error(w, "read body", http.StatusBadRequest)
			return
		}

		env, err := gitlab.ParseEnvelope(r.Header, body)
		if err != nil {
			http.Error(w, "bad payload", http.StatusBadRequest)
			return
		}

		result, err := ingest.HandleGitLab(r.Context(), installationID, env)
		if err != nil {
			slog.Error("gitlab ingest failed", "delivery", env.DeliveryID,
				"kind", env.Kind, "error", err)
			http.Error(w, "ingest failed", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(result)
	})
}
