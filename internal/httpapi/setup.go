package httpapi

import (
	"context"
	"log/slog"
	"net/http"
	"strconv"
)

// SetupClaimer ties an installation to a Telegram user.
type SetupClaimer interface {
	ClaimInstallation(ctx context.Context, installationID, userID int64) error
}

// StateStore spends the single-use token the install link carried as GitHub's
// `state`. It is what identifies the user: the redirect itself is
// unauthenticated and anyone can call it with any installation id.
type StateStore interface {
	TakeInstallState(ctx context.Context, token string) (int64, error)
}

// NewSetupHandler receives GitHub's post-install redirect and bounces the
// user back into Telegram. A valid state claims the installation for its
// user first; a bad state or a failed claim is logged, not shown — the user
// still lands in the bot, where the accounts screen tells the truth about
// what is connected.
func NewSetupHandler(claimer SetupClaimer, states StateStore, botUsername string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		installationID, _ := strconv.ParseInt(r.URL.Query().Get("installation_id"), 10, 64)
		if installationID == 0 {
			http.Error(w, "missing installation_id", http.StatusBadRequest)
			return
		}

		if claimer != nil && states != nil {
			claim(r, claimer, states, installationID)
		}

		http.Redirect(w, r,
			"https://t.me/"+botUsername+"?start=installed_"+strconv.FormatInt(installationID, 10),
			http.StatusFound)
	})
}

func claim(r *http.Request, claimer SetupClaimer, states StateStore, installationID int64) {
	token := r.URL.Query().Get("state")
	if token == "" {
		return
	}

	userID, err := states.TakeInstallState(r.Context(), token)
	if err != nil {
		slog.Warn("setup state rejected", "installation", installationID, "error", err)
		return
	}
	if err := claimer.ClaimInstallation(r.Context(), installationID, userID); err != nil {
		slog.Warn("claim installation", "installation", installationID,
			"user", userID, "error", err)
	}
}
