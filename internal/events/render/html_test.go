package render_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/faustyu/gh-notify-go/internal/events/render"
)

func TestEscapeCoversTelegramSpecialCharacters(t *testing.T) {
	require.Equal(t, "&lt;b&gt;x&lt;/b&gt; &amp; y", render.Escape("<b>x</b> & y"))
}

func TestLinkEscapesBothPartsSeparately(t *testing.T) {
	got := render.Link("https://x.dev/a?b=1&c=2", "pull <request>")
	require.Equal(t,
		`<a href="https://x.dev/a?b=1&amp;c=2">pull &lt;request&gt;</a>`, got)
}

func TestTruncateLeavesShortStringsAlone(t *testing.T) {
	require.Equal(t, "short", render.Truncate("short", 10))
}

func TestTruncateCutsOnRuneBoundary(t *testing.T) {
	// Cyrillic is two bytes per rune; a byte-based cut would corrupt it.
	got := render.Truncate("привет мир", 6)
	require.Equal(t, "привет…", got)
	require.True(t, strings.HasSuffix(got, "…"))
}

func TestEmojiWrapsIDWithUnicodeFallback(t *testing.T) {
	require.Equal(t,
		`<tg-emoji emoji-id="5870982283724328568">⚙</tg-emoji>`,
		render.Emoji(render.EmojiSettings, "⚙"))
}

func TestStripRemovesOnlyEmojiTags(t *testing.T) {
	in := `<b>hi</b> <tg-emoji emoji-id="123">⚙</tg-emoji> <a href="u">l</a>`
	require.Equal(t, `<b>hi</b> ⚙ <a href="u">l</a>`, render.Strip(in))
}

func TestEmojiConstantsAreDistinct(t *testing.T) {
	ids := []string{
		render.EmojiSettings, render.EmojiProfile, render.EmojiPeople,
		render.EmojiFile, render.EmojiChart, render.EmojiStats,
		render.EmojiHouse, render.EmojiLockClosed, render.EmojiLockOpen,
		render.EmojiMegaphone, render.EmojiCheck, render.EmojiCross,
		render.EmojiPencil, render.EmojiTrash, render.EmojiLink,
		render.EmojiInfo, render.EmojiBot, render.EmojiEye,
		render.EmojiBell, render.EmojiClock, render.EmojiParty,
		render.EmojiWrite, render.EmojiMedia, render.EmojiCalendar,
		render.EmojiTag, render.EmojiCode, render.EmojiLoading,
	}
	seen := make(map[string]bool, len(ids))
	for _, id := range ids {
		require.NotEmpty(t, id)
		require.Falsef(t, seen[id], "emoji id %s used twice", id)
		seen[id] = true
	}
}
