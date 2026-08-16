// Package ui is the screen engine. A screen is a pure function of session
// state; the engine owns navigation, callback indirection, and the back
// button, so individual screens never deal with any of it.
package ui

import (
	"context"
	"errors"
	"fmt"
)

var ErrUnknownScreen = errors.New("unknown screen")

// BackButtonLabel is plain Unicode: Telegram button labels cannot carry
// message entities, so premium emoji are impossible here by design.
const BackButtonLabel = "◁ Назад"

type Params map[string]string

type Session struct {
	UserID     int64
	TelegramID int64
	Params     Params
	Depth      int
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

type Engine struct {
	nav     NavStore
	screens map[string]Screen
}

func NewEngine(nav NavStore) *Engine {
	return &Engine{nav: nav, screens: map[string]Screen{}}
}

func (e *Engine) Register(screens ...Screen) {
	for _, s := range screens {
		e.screens[s.Name()] = s
	}
}

// Open renders a screen and records it on the navigation stack.
func (e *Engine) Open(
	ctx context.Context, userID, telegramID int64, screen string, params Params,
) (View, error) {
	if _, ok := e.screens[screen]; !ok {
		return View{}, fmt.Errorf("%w: %s", ErrUnknownScreen, screen)
	}
	if err := e.nav.Push(ctx, userID, screen, params); err != nil {
		return View{}, err
	}
	return e.render(ctx, userID, telegramID, screen, params)
}

// Back pops one frame and renders what is underneath.
func (e *Engine) Back(ctx context.Context, userID, telegramID int64) (View, error) {
	screen, params, err := e.nav.Pop(ctx, userID)
	if err != nil {
		return View{}, err
	}
	return e.render(ctx, userID, telegramID, screen, params)
}

func (e *Engine) render(
	ctx context.Context, userID, telegramID int64, screen string, params Params,
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
	})
	if err != nil {
		return View{}, fmt.Errorf("render %s: %w", screen, err)
	}

	// The engine appends the back button rather than each screen doing it,
	// which is what guarantees it is present everywhere below the root.
	if depth > 1 {
		view.Rows = append(view.Rows, []Button{{Label: BackButtonLabel, Screen: backScreen}})
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
