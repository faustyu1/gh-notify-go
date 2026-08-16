package events_test

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/faustyu/gh-notify-go/internal/events"
)

// -update rewrites the golden files. Review the diff before committing:
// a golden test only protects you if the expected output is read by a human
// at least once.
var update = flag.Bool("update", false, "rewrite golden files")

// assertGolden renders testdata/<name>.json and compares it to
// testdata/<name>.golden.html.
func assertGolden(t *testing.T, kind events.Kind, name string) {
	t.Helper()

	payload, err := os.ReadFile(filepath.Join("testdata", name+".json"))
	require.NoError(t, err)

	got, err := events.Render(kind, json.RawMessage(payload))
	require.NoError(t, err)

	goldenPath := filepath.Join("testdata", name+".golden.html")
	if *update {
		require.NoError(t, os.WriteFile(goldenPath, []byte(got), 0o644))
		return
	}

	want, err := os.ReadFile(goldenPath)
	require.NoError(t, err)
	require.Equal(t, string(want), got)
}

func TestPushGolden(t *testing.T) {
	assertGolden(t, "push", "push")
}
