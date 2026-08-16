package httpapi

import (
	"net/http"

	"github.com/faustyu/gh-notify-go/internal/ghapp"
	"github.com/faustyu/gh-notify-go/internal/storage"
)

// NewSetupHandler receives GitHub's post-install redirect and bounces the
// user back into Telegram. It intentionally does not create the installation
// row: the authenticated `installation` webhook does that, and this redirect
// is not authenticated.
func NewSetupHandler(
	_ *storage.Store, _ *ghapp.TokenSource, botUsername string,
) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		installationID := r.URL.Query().Get("installation_id")
		if installationID == "" {
			http.Error(w, "missing installation_id", http.StatusBadRequest)
			return
		}
		http.Redirect(w, r,
			"https://t.me/"+botUsername+"?start=installed_"+installationID,
			http.StatusFound)
	})
}
