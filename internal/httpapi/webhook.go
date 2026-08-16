// Package httpapi exposes the GitHub-facing HTTP surface.
package httpapi

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"

	"github.com/faustyu/gh-notify-go/internal/ghapp"
	"github.com/faustyu/gh-notify-go/internal/service"
)

// maxBodyBytes bounds what we will read from an unauthenticated request.
// GitHub payloads are well under this.
const maxBodyBytes = 8 << 20

func NewWebhookHandler(secret string, ingest *service.Ingest) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		body, err := io.ReadAll(io.LimitReader(r.Body, maxBodyBytes))
		if err != nil {
			http.Error(w, "read body", http.StatusBadRequest)
			return
		}

		// Signature first: nothing else touches unauthenticated input.
		if err := ghapp.VerifySignature(secret, body, r.Header.Get("X-Hub-Signature-256")); err != nil {
			http.Error(w, "bad signature", http.StatusUnauthorized)
			return
		}

		kind := r.Header.Get("X-GitHub-Event")
		delivery := r.Header.Get("X-GitHub-Delivery")
		if kind == "" || delivery == "" {
			http.Error(w, "missing github headers", http.StatusBadRequest)
			return
		}

		env, err := ghapp.ParseEnvelope(kind, delivery, body)
		if err != nil {
			http.Error(w, "bad payload", http.StatusBadRequest)
			return
		}

		result, err := ingest.Handle(r.Context(), env)
		if err != nil {
			// 500 makes GitHub retry, which is what we want: the delivery
			// is not lost, it is redelivered once we are healthy again.
			slog.Error("ingest failed", "delivery", delivery, "kind", kind, "error", err)
			http.Error(w, "ingest failed", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(result)
	})
}
