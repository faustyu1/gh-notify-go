// Package ui is the screen engine. A screen is a pure function of session
// state; the engine owns navigation, callback indirection, and the back
// button, so individual screens never deal with any of it.
package ui

import (
	"context"
	"errors"
	"fmt"

	"github.com/faustyu/gh-notify-go/internal/i18n"
)

var ErrUnknownScreen = errors.New("unknown screen")

type Params map[string]string

type Session struct {
	UserID     int64
	TelegramID int64
	Params     Params
	Depth      int

	// Lang is the user's stored language; screens render their text through
	// it. The zero value falls back to the default locale.
	Lang string
}

// Button is either a navigation target (Screen set) or an external link
// (URL set).
type Button struct {
	Label  string
	Screen string
	Params Params
	URL    string
}

type View struct {
	Text string
	Rows [][]Button
}

type Screen interface {
	Name() string
	Render(ctx context.Context, s Session) (View, error)
}

// Guard authorizes a screen before it is opened. The engine calls it on every
// path to a screen — a fresh open, a deep link, the back button — so a screen
// cannot be reached by a user who is not allowed to see it, and a stale
// navigation frame cannot outlive the rights that created it.
type Guard func(ctx context.Context, telegramID int64, screen string, params Params) error

type Engine struct {
	nav     NavStore
	screens map[string]Screen
	loc     *i18n.Bundle
	guard   Guard
}

func NewEngine(nav NavStore, loc *i18n.Bundle) *Engine {
	return &Engine{nav: nav, screens: map[string]Screen{}, loc: loc}
}

// WithGuard installs the authorization hook. Without one every registered
// screen is open to every user, which is only ever right in tests.
func (e *Engine) WithGuard(g Guard) *Engine {
	e.guard = g
	return e
}

func (e *Engine) authorize(
	ctx context.Context, telegramID int64, screen string, params Params,
) error {
	if e.guard == nil {
		return nil
	}
	return e.guard(ctx, telegramID, screen, params)
}

func (e *Engine) Register(screens ...Screen) {
	for _, s := range screens {
		e.screens[s.Name()] = s
	}
}

// Open renders a screen in the user's language and records it on the
// navigation stack.
func (e *Engine) Open(
	ctx context.Context, userID, telegramID int64, screen string, params Params, lang string,
) (View, error) {
	if _, ok := e.screens[screen]; !ok {
		return View{}, fmt.Errorf("%w: %s", ErrUnknownScreen, screen)
	}
	// Authorizing before the push keeps a refused screen off the stack.
	if err := e.authorize(ctx, telegramID, screen, params); err != nil {
		return View{}, err
	}
	if err := e.nav.Push(ctx, userID, screen, params); err != nil {
		return View{}, err
	}
	return e.render(ctx, userID, telegramID, screen, params, lang)
}

// Back pops one frame and renders what is underneath.
func (e *Engine) Back(
	ctx context.Context, userID, telegramID int64, lang string,
) (View, error) {
	screen, params, err := e.nav.Pop(ctx, userID)
	if err != nil {
		return View{}, err
	}
	if err := e.authorize(ctx, telegramID, screen, params); err != nil {
		return View{}, err
	}
	return e.render(ctx, userID, telegramID, screen, params, lang)
}

// BackButtonLabel is plain Unicode: Telegram button labels cannot carry
// message entities, so premium emoji are impossible here by design.
func (e *Engine) BackButtonLabel(lang string) string {
	return e.loc.Localizer(lang).T("nav.back")
}

func (e *Engine) render(
	ctx context.Context, userID, telegramID int64, screen string, params Params, lang string,
) (View, error) {
	impl, ok := e.screens[screen]
	if !ok {
		return View{}, fmt.Errorf("%w: %s", ErrUnknownScreen, screen)
	}

	depth, err := e.nav.Depth(ctx, userID)
	if err != nil {
		return View{}, err
	}

	view, err := impl.Render(ctx, Session{
		UserID: userID, TelegramID: telegramID, Params: params, Depth: depth,
		Lang: lang,
	})
	if err != nil {
		return View{}, fmt.Errorf("render %s: %w", screen, err)
	}

	// The engine appends the back button rather than each screen doing it,
	// which is what guarantees it is present everywhere below the root.
	if depth > 1 {
		view.Rows = append(view.Rows, []Button{{
			Label: e.BackButtonLabel(lang), Screen: backScreen,
		}})
	}
	return view, nil
}

// backScreen is a reserved target the callback handler translates into a Back
// call rather than an Open.
const backScreen = "__back"

// IsBack reports whether a resolved screen name means "go back".
func IsBack(screen string) bool { return screen == backScreen }

// ActionKey stores a button's target and returns the opaque token to put in
// callback_data.
func (e *Engine) ActionKey(
	ctx context.Context, userID int64, screen string, params Params,
) (string, error) {
	key, err := newActionKey()
	if err != nil {
		return "", err
	}
	if err := e.nav.PutAction(ctx, userID, key, screen, params); err != nil {
		return "", err
	}
	return key, nil
}

func (e *Engine) Resolve(
	ctx context.Context, userID int64, key string,
) (string, Params, error) {
	return e.nav.GetAction(ctx, userID, key)
}
