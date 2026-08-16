package httpapi

import (
	"context"
	"log/slog"
	"net/http"
	"strconv"
)

// SetupClaimer ties an installation to the Telegram user whose install link
// carried them to GitHub (state = users.id).
type SetupClaimer interface {
	ClaimInstallation(ctx context.Context, installationID, userID int64) error
}

// NewSetupHandler receives GitHub's post-install redirect and bounces the
// user back into Telegram. When the redirect carries a state that matches a
// known user, the installation is claimed for them first; a claim failure is
// logged, not shown — the user still lands in the bot.
func NewSetupHandler(claimer SetupClaimer, botUsername string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		installationID, _ := strconv.ParseInt(r.URL.Query().Get("installation_id"), 10, 64)
		if installationID == 0 {
			http.Error(w, "missing installation_id", http.StatusBadRequest)
			return
		}

		if claimer != nil {
			if state, _ := strconv.ParseInt(r.URL.Query().Get("state"), 10, 64); state != 0 {
				if err := claimer.ClaimInstallation(r.Context(), installationID, state); err != nil {
					slog.Warn("claim installation", "installation", installationID, "error", err)
				}
			}
		}

		http.Redirect(w, r,
			"https://t.me/"+botUsername+"?start=installed_"+strconv.FormatInt(installationID, 10),
			http.StatusFound)
	})
}
