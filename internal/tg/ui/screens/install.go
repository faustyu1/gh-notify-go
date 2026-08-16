package screens

import (
	"context"
	"fmt"
	"time"

	"github.com/faustyu/gh-notify-go/internal/events/render"
	"github.com/faustyu/gh-notify-go/internal/i18n"
	"github.com/faustyu/gh-notify-go/internal/tg/ui"
)

// InstallStates mints the single-use token the install link carries through
// GitHub and back to the setup redirect.
type InstallStates interface {
	NewInstallState(ctx context.Context, userID int64, ttl time.Duration) (string, error)
}

// installStateTTL is long enough to pick repositories on GitHub and short
// enough that an abandoned link stops being a way in.
const installStateTTL = time.Hour

type install struct {
	slug      string
	publicURL string
	states    InstallStates
	loc       *i18n.Bundle
}

func NewInstall(slug, publicURL string, states InstallStates, loc *i18n.Bundle) ui.Screen {
	return install{slug: slug, publicURL: publicURL, states: states, loc: loc}
}

func (i install) Name() string { return "install" }

func (i install) Render(ctx context.Context, s ui.Session) (ui.View, error) {
	l := i.loc.Localizer(s.Lang)

	// state is an unguessable single-use token, not the user id: the setup
	// redirect is unauthenticated, so whatever it carries has to be
	// something only this user could have been given.
	state, err := i.states.NewInstallState(ctx, s.UserID, installStateTTL)
	if err != nil {
		return ui.View{}, err
	}
	url := fmt.Sprintf("https://github.com/apps/%s/installations/new?state=%s",
		i.slug, state)

	text := render.Emoji(render.EmojiLink, "🔗") + " <b>" + l.T("install.title") + "</b>\n\n" +
		l.T("install.body")

	return ui.View{
		Text: text,
		Rows: [][]ui.Button{
			{{Label: l.T("btn.install_app"), URL: url}},
			{{Label: l.T("btn.installed"), Screen: "accounts"}},
		},
	}, nil
}
