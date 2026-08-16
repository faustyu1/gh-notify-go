package events_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/faustyu/gh-notify-go/internal/events"
)

func TestRenderUnknownKindIsAnError(t *testing.T) {
	_, err := events.Render("no_such_event", json.RawMessage(`{}`))
	require.ErrorIs(t, err, events.ErrUnknownKind)
}

func TestWantedHonoursActionFilter(t *testing.T) {
	// push registers with an empty filter: every delivery is wanted.
	require.True(t, events.Wanted("push", ""))
	require.True(t, events.Wanted("push", "anything"))
}

func TestWantedRejectsUnknownKind(t *testing.T) {
	require.False(t, events.Wanted("no_such_event", "opened"))
}

func TestKindsIncludesRegisteredEvents(t *testing.T) {
	require.Contains(t, events.Kinds(), events.Kind("push"))
}
