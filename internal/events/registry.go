// Package events turns GitHub webhook payloads into Telegram HTML. Each
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
)

type Kind string

// Renderer turns a raw payload into a ready Telegram HTML message.
type Renderer func(raw json.RawMessage) (string, error)

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

func Render(kind Kind, raw json.RawMessage) (string, error) {
	mu.RLock()
	reg, ok := registry[kind]
	mu.RUnlock()

	if !ok {
		return "", fmt.Errorf("%w: %s", ErrUnknownKind, kind)
	}
	return reg.render(raw)
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
