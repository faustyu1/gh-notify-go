package render_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/faustyu/gh-notify-go/internal/events/render"
)

func TestMarkdownHeadingsEmphasisLinks(t *testing.T) {
	got := render.Markdown("## Fix **crash**\nSee [docs](https://x.dev/a?b=1&c=2).", 0)
	require.Equal(t,
		"<b>Fix <b>crash</b></b>\nSee <a href=\"https://x.dev/a?b=1&amp;c=2\">docs</a>.",
		got)
}

func TestMarkdownCodeAndLists(t *testing.T) {
	got := render.Markdown("Run `make test`:\n\n- one\n- two <three>", 0)
	require.Equal(t,
		"Run <code>make test</code>:\n• one\n• two &lt;three&gt;",
		got)
}

func TestMarkdownFencedCodeBlock(t *testing.T) {
	got := render.Markdown("```go\nfmt.Println(\"x\")\n```", 0)
	require.Equal(t,
		"<pre>fmt.Println(\"x\")\n</pre>", // only & < > need escaping inside pre
		got)
}

func TestMarkdownLimitsOutput(t *testing.T) {
	long := ""
	for range 200 {
		long += "word "
	}
	got := render.Markdown(long, 100)
	require.LessOrEqual(t, len([]rune(got)), 100)
}

func TestMarkdownPlainTextUtils(t *testing.T) {
	// Plain prose must come out escaped and intact.
	got := render.Markdown("Steps to reproduce: a < b & c", 0)
	require.Equal(t, "Steps to reproduce: a &lt; b &amp; c", got)
}
