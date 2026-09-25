// Package events turns webhook payloads into Telegram HTML. Each
// event type lives in its own file and registers itself in init(), so adding
// a type is one new file and no edits elsewhere.
package events

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"sync"

	"github.com/faustyu/gh-notify-go/internal/forge"
	"github.com/faustyu/gh-notify-go/internal/i18n"
)

type Kind string

// Renderer turns a raw payload into a ready Telegram HTML message in the
// recipient's language.
type Renderer func(loc *i18n.Localizer, raw json.RawMessage) (string, error)

// ActionFilter lists the payload actions worth sending. An empty filter
// means the event has no action field, or every action is worth sending.
type ActionFilter []string

var ErrUnknownKind = errors.New("unknown event kind")

type registration struct {
	filter ActionFilter
	render Renderer
}

var (
	mu       sync.RWMutex
	registry = map[Kind]registration{}
)

// Register wires one event type. Registering the same kind twice panics,
// because it means two files disagree about who owns the type.
func Register(kind Kind, filter ActionFilter, r Renderer) {
	mu.Lock()
	defer mu.Unlock()

	if _, exists := registry[kind]; exists {
		panic(fmt.Sprintf("events: kind %q registered twice", kind))
	}
	registry[kind] = registration{filter: filter, render: r}
}

// Wanted reports whether this kind+action should produce a message at all.
// The ingest path calls it before touching the database.
func Wanted(kind Kind, action string) bool {
	mu.RLock()
	defer mu.RUnlock()

	reg, ok := registry[kind]
	if !ok {
		return false
	}
	if len(reg.filter) == 0 {
		return true
	}
	return slices.Contains(reg.filter, action)
}

func Render(kind Kind, loc *i18n.Localizer, raw json.RawMessage) (string, error) {
	mu.RLock()
	reg, ok := registry[kind]
	mu.RUnlock()

	if !ok {
		return "", fmt.Errorf("%w: %s", ErrUnknownKind, kind)
	}
	return reg.render(loc, raw)
}

// Kinds returns every registered kind in a stable order, used to build the
// event-toggle screen.
func Kinds() []Kind {
	mu.RLock()
	defer mu.RUnlock()

	out := make([]Kind, 0, len(registry))
	for k := range registry {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// KindsFor returns the kinds an integration from this provider can receive,
// in the same stable order as Kinds: the ones carrying the prefix of the
// provider's payload format.
func KindsFor(provider string) []Kind {
	prefix := ""
	if p, ok := forge.Get(provider); ok {
		prefix = p.KindPrefix()
	}
	all := Kinds()
	out := make([]Kind, 0, len(all))
	for _, k := range all {
		if forge.PrefixOf(string(k)) == prefix {
			out = append(out, k)
		}
	}
	return out
}

// Label is how a kind is shown on a toggle.
func Label(kind Kind) string {
	return forge.Label(string(kind))
}

// ImportantKinds is what the «Только важное» preset keeps on.
var ImportantKinds = map[Kind]bool{
	"pull_request": true, "issues": true, "issue_comment": true,
	"pull_request_review": true, "release": true, "workflow_run": true,

	"gl_merge_request": true, "gl_issue": true, "gl_note": true,
	"gl_pipeline": true, "gl_release": true,

	"gt_pull_request": true, "gt_issues": true, "gt_issue_comment": true,
	"gt_pull_request_review": true, "gt_release": true,
}

// PresetEnabled reports whether a named preset keeps a kind enabled.
func PresetEnabled(preset string, kind Kind) bool {
	switch preset {
	case "none":
		return false
	case "important":
		return ImportantKinds[kind]
	default: // "all" and anything unknown
		return true
	}
}
