package render_test

import (
	"strings"
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

func TestMarkdownLimitKeepsTagsWhole(t *testing.T) {
	// Markup does not count towards GitHub's body length, so a body full of
	// links renders far longer than its source. The cut must still land
	// between tags: Telegram rejects the whole message otherwise.
	src := ""
	for range 30 {
		src += "[fix](https://github.com/acme/repo/pull/12345) ok\n\n"
	}

	got := render.Markdown(src, 500)

	require.LessOrEqual(t, len([]rune(got)), 500)
	require.Equal(t, strings.Count(got, "<a "), strings.Count(got, "</a>"),
		"anchor cut in half: %q", got)
}

func TestMarkdownLimitTooSmallForMarkupDropsIt(t *testing.T) {
	// No cut wide enough to hold the anchor fits, so the body degrades to
	// plain text rather than going out as half a tag.
	got := render.Markdown("[fix](https://github.com/acme/repo/pull/1)", 5)

	require.LessOrEqual(t, len([]rune(got)), 5)
	require.NotContains(t, got, "<")
}
