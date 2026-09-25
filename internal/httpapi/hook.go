package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/faustyu/gh-notify-go/internal/forge"
	"github.com/faustyu/gh-notify-go/internal/service"
	"github.com/faustyu/gh-notify-go/internal/storage"
)

// HookAuth resolves a webhook delivery to the connection it belongs to.
type HookAuth interface {
	InstallationByToken(ctx context.Context, provider, token string) (int64, error)
	WebhookSecret(ctx context.Context, provider string, installationID int64) (string, error)
}

// HookIngest is the ingest step a webhook delivery feeds.
type HookIngest interface {
	HandleHook(ctx context.Context, installationID int64, env forge.Envelope) (service.Result, error)
}

// MountHooks serves every webhook-connected provider:
//
//	/hook/{provider}               token providers: the token names the connection
//	/hook/{provider}/{connection}  signing providers: the URL names it
//
// /gl/webhook stays as the GitLab URL existing webhooks were set up with.
func MountHooks(mux *http.ServeMux, auth HookAuth, ingest HookIngest) {
	h := NewHookHandler(auth, ingest)
	mux.Handle("/hook/{provider}", h)
	mux.Handle("/hook/{provider}/{connection}", h)
	mux.Handle("/gl/webhook", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.SetPathValue("provider", forge.GitLab.ID())
		h.ServeHTTP(w, r)
	}))
}

// NewHookHandler authenticates a delivery the way its provider signs it, and
// hands the parsed envelope to ingest.
func NewHookHandler(auth HookAuth, ingest HookIngest) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hook, ok := forge.HookFor(r.PathValue("provider"))
		if !ok {
			http.NotFound(w, r)
			return
		}
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		var (
			installationID int64
			body           []byte
			err            error
		)
		switch hook.Auth() {
		case forge.AuthToken:
			// Token first: nothing else touches unauthenticated input.
			installationID, err = auth.InstallationByToken(r.Context(), hook.ID(), hook.Token(r.Header))
			if err != nil {
				refuse(w, hook, err)
				return
			}
			if body, err = io.ReadAll(io.LimitReader(r.Body, maxBodyBytes)); err != nil {
				http.Error(w, "read body", http.StatusBadRequest)
				return
			}
		case forge.AuthSignature:
			installationID, err = strconv.ParseInt(r.PathValue("connection"), 10, 64)
			if err != nil {
				http.NotFound(w, r)
				return
			}
			secret, err := auth.WebhookSecret(r.Context(), hook.ID(), installationID)
			if err != nil {
				refuse(w, hook, err)
				return
			}
			if body, err = io.ReadAll(io.LimitReader(r.Body, maxBodyBytes)); err != nil {
				http.Error(w, "read body", http.StatusBadRequest)
				return
			}
			// Signature first: nothing else touches unauthenticated input.
			if !hook.Verify(secret, r.Header, body) {
				http.Error(w, "bad signature", http.StatusUnauthorized)
				return
			}
		default:
			http.NotFound(w, r)
			return
		}

		env, err := hook.Parse(r.Header, body)
		if err != nil {
			http.Error(w, "bad payload", http.StatusBadRequest)
			return
		}

		result, err := ingest.HandleHook(r.Context(), installationID, env)
		if err != nil {
			slog.Error("webhook ingest failed", "provider", hook.ID(),
				"delivery", env.DeliveryID, "kind", env.Kind, "error", err)
			http.Error(w, "ingest failed", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(result)
	})
}

func refuse(w http.ResponseWriter, hook forge.Hook, err error) {
	if errors.Is(err, storage.ErrUnknownWebhookToken) {
		http.Error(w, "unknown connection", http.StatusUnauthorized)
		return
	}
	slog.Error("webhook connection lookup failed", "provider", hook.ID(), "error", err)
	http.Error(w, "lookup failed", http.StatusInternalServerError)
}
