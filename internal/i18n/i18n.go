// Package i18n holds every user-visible string. Locales live in YAML files
// next to this code and are embedded into the binary; a missing key in a
// locale falls back to English, and a key missing everywhere renders as the
// key itself so the defect is visible rather than silent.
package i18n

import (
	"fmt"
	"io/fs"
	"log/slog"
	"strings"

	"gopkg.in/yaml.v3"
)

// Supported lists the shipped locales. The first entry is the fallback.
var Supported = []string{"en", "ru", "es", "de", "pt"}

// Default is the locale used when the requested language is unknown.
const Default = "en"

type Bundle struct {
	locales map[string]map[string]any
}

// New reads one YAML file per supported language from dir ("en.yaml", …).
func New(dir fs.FS) (*Bundle, error) {
	b := &Bundle{locales: map[string]map[string]any{}}
	for _, lang := range Supported {
		raw, err := fs.ReadFile(dir, lang+".yaml")
		if err != nil {
			return nil, fmt.Errorf("read locale %s: %w", lang, err)
		}
		var tree map[string]any
		if err := yaml.Unmarshal(raw, &tree); err != nil {
			return nil, fmt.Errorf("parse locale %s: %w", lang, err)
		}
		b.locales[lang] = tree
	}
	return b, nil
}

// Localizer returns a lightweight view of the bundle for one language.
// The zero-value language resolves to the default locale, so an unset
// Session.Lang renders in English rather than blowing up.
func (b *Bundle) Localizer(lang string) *Localizer {
	return &Localizer{b: b, lang: Normalize(lang)}
}

// LocaleTree exposes the parsed tree of one locale, mainly so tests can
// compare key sets across languages.
func (b *Bundle) LocaleTree(lang string) map[string]any {
	return b.locales[Normalize(lang)]
}

// Language names one selectable locale for the language picker screens.
type Language struct {
	Code string
	Name string
}

// Languages lists the supported locales with their native names, in the
// order they appear in the shipped locale files.
func (b *Bundle) Languages() []Language {
	out := make([]Language, 0, len(Supported))
	for _, code := range Supported {
		out = append(out, Language{Code: code, Name: b.Localizer(code).T("lang.name")})
	}
	return out
}

type Localizer struct {
	b    *Bundle
	lang string
}

// Lang reports the normalized language this localizer renders.
func (l *Localizer) Lang() string { return l.lang }

// T formats a message. args is a flat key/value list ("repo", "acme/app");
// the message references values as {repo}. When the key holds plural
// variants, the first numeric arg picks the form, so pass the counted
// number before any other numbers.
func (l *Localizer) T(key string, args ...any) string {
	value, ok := l.resolve(key)
	if !ok {
		slog.Warn("i18n: missing key", "key", key, "lang", l.lang)
		return key
	}
	if variants, isPlural := value.(map[string]any); isPlural {
		value = pickPlural(variants, l.lang, args)
	}
	return format(fmt.Sprint(value), args)
}

// DateTimeLayout is the locale's preferred short timestamp layout for
// Telegram messages.
func (l *Localizer) DateTimeLayout() string {
	if layout, ok := l.resolve("format.datetime"); ok {
		return fmt.Sprint(layout)
	}
	return "02.01 15:04"
}

// resolve walks the dotted key through the locale tree, then the fallback.
func (l *Localizer) resolve(key string) (any, bool) {
	if v, ok := lookup(l.b.locales[l.lang], key); ok {
		return v, true
	}
	return lookup(l.b.locales[Default], key)
}

func lookup(tree map[string]any, key string) (any, bool) {
	if tree == nil {
		return nil, false
	}
	parts := strings.Split(key, ".")
	var node any = tree
	for _, p := range parts {
		m, ok := node.(map[string]any)
		if !ok {
			return nil, false
		}
		node, ok = m[p]
		if !ok {
			return nil, false
		}
	}
	return node, true
}

// pickPlural selects a CLDR plural category for the first numeric argument.
func pickPlural(variants map[string]any, lang string, args []any) any {
	n := firstNumber(args)
	category := pluralCategory(lang, n)
	if v, ok := variants[category]; ok {
		return v
	}
	if v, ok := variants["other"]; ok {
		return v
	}
	// A locale that only defines one form covers every count.
	for _, v := range variants {
		return v
	}
	return ""
}

func firstNumber(args []any) int {
	for i := 0; i+1 < len(args); i += 2 {
		if n, ok := args[i+1].(int); ok {
			return n
		}
	}
	return 0
}

// pluralCategory knows the plural rules of the shipped locales. Anything
// else gets the simple English one/other split.
func pluralCategory(lang string, n int) string {
	switch lang {
	case "ru":
		mod10, mod100 := n%10, n%100
		switch {
		case mod10 == 1 && mod100 != 11:
			return "one"
		case mod10 >= 2 && mod10 <= 4 && (mod100 < 12 || mod100 > 14):
			return "few"
		default:
			return "many"
		}
	case "pt":
		if n >= 0 && n <= 1 {
			return "one"
		}
		return "other"
	default: // en, es, de
		if n == 1 {
			return "one"
		}
		return "other"
	}
}

// format substitutes {name} placeholders. Unknown placeholders are left in
// place so a mismatched message is easy to spot in the chat.
func format(message string, args []any) string {
	for i := 0; i+1 < len(args); i += 2 {
		name := fmt.Sprint(args[i])
		message = strings.ReplaceAll(message, "{"+name+"}", fmt.Sprint(args[i+1]))
	}
	return message
}

// Normalize maps a Telegram language_code ("pt-BR", "en-US") onto a shipped
// locale, or "" when none matches.
func Normalize(code string) string {
	base := strings.ToLower(strings.SplitN(strings.TrimSpace(code), "-", 2)[0])
	for _, lang := range Supported {
		if base == lang {
			return lang
		}
	}
	return Default
}
