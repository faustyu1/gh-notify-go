// Package render builds Telegram-HTML fragments. Telegram accepts a small
// tag subset, so everything here emits only b, i, code, pre, a and tg-emoji.
package render

import (
	"fmt"
	"regexp"
	"strings"
)

var escaper = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")

// Escape makes arbitrary text safe to place inside Telegram HTML.
func Escape(s string) string { return escaper.Replace(s) }

// Link builds an anchor, escaping the URL and the label independently.
func Link(url, text string) string {
	return fmt.Sprintf(`<a href="%s">%s</a>`, Escape(url), Escape(text))
}

// Truncate cuts on a rune boundary and appends an ellipsis. Cutting by byte
// would split multi-byte characters and produce invalid UTF-8 in the message.
func Truncate(s string, limit int) string {
	runes := []rune(s)
	if len(runes) <= limit {
		return s
	}
	return strings.TrimRight(string(runes[:limit]), " ") + "…"
}

// Emoji wraps a premium emoji id together with the Unicode character clients
// without custom-emoji support will render instead.
func Emoji(id, fallback string) string {
	return fmt.Sprintf(`<tg-emoji emoji-id="%s">%s</tg-emoji>`, id, fallback)
}

var emojiTagRe = regexp.MustCompile(`<tg-emoji emoji-id="[^"]*">(.*?)</tg-emoji>`)

// Strip replaces every tg-emoji tag with its fallback text. The sender calls
// this once when Telegram rejects custom emoji entities, so the message still
// goes out looking plain instead of not going out at all.
func Strip(html string) string {
	return emojiTagRe.ReplaceAllString(html, "$1")
}
