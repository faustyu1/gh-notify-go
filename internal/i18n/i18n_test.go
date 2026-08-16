package i18n_test

import (
	"maps"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/faustyu/gh-notify-go/internal/i18n"
)

func bundle(t *testing.T) *i18n.Bundle {
	t.Helper()
	b, err := i18n.NewBundle()
	require.NoError(t, err)
	return b
}

func TestAllLocalesLoad(t *testing.T) {
	require.Len(t, bundle(t).Languages(), len(i18n.Supported))
}

// pluralCategories tells a plural-variant map apart from a namespace map.
var pluralCategories = map[string]bool{
	"zero": true, "one": true, "two": true, "few": true, "many": true, "other": true,
}

func isPluralMap(m map[string]any) bool {
	for key := range m {
		if pluralCategories[key] {
			return true
		}
	}
	return false
}

// leaves flattens a locale tree into dotted paths. A plural map counts as
// one logical leaf keyed by its parent path, its value the concatenation of
// every variant — the variant sets legitimately differ across languages.
func leaves(tree map[string]any, prefix string) map[string]string {
	out := map[string]string{}
	for key, value := range tree {
		path := key
		if prefix != "" {
			path = prefix + "." + key
		}
		if nested, ok := value.(map[string]any); ok {
			if isPluralMap(nested) {
				var variants []string
				for _, v := range nested {
					variants = append(variants, v.(string))
				}
				sort.Strings(variants)
				out[path] = strings.Join(variants, "\x00")
				continue
			}
			maps.Copy(out, leaves(nested, path))
			continue
		}
		out[path] = value.(string)
	}
	return out
}

// TestEveryLocaleCoversTheBaseKeyset is the guard that keeps the five locale
// files from drifting apart: a key added to en.yaml must appear in all of
// them, or the missing-key fallback would silently serve English.
func TestEveryLocaleCoversTheBaseKeyset(t *testing.T) {
	b := bundle(t)

	base := leaves(b.LocaleTree(i18n.Default), "")
	for _, lang := range i18n.Supported {
		got := leaves(b.LocaleTree(lang), "")
		for key := range base {
			require.Containsf(t, got, key, "%s is missing key %s", lang, key)
		}
		for key := range got {
			require.Containsf(t, base, key, "%s has key %s unknown to %s",
				lang, key, i18n.Default)
		}
	}
}

// TestPlaceholdersMatchAcrossLocales catches a translation that drops or
// renames a {placeholder}, which would leak literal braces into the chat.
func TestPlaceholdersMatchAcrossLocales(t *testing.T) {
	b := bundle(t)

	ph := func(s string) []string {
		seen := map[string]struct{}{}
		for _, part := range strings.SplitAfter(s, "}") {
			if i := strings.Index(part, "{"); i >= 0 {
				seen[part[i:]] = struct{}{}
			}
		}
		out := make([]string, 0, len(seen))
		for p := range seen {
			out = append(out, p)
		}
		sort.Strings(out)
		return out
	}

	base := leaves(b.LocaleTree(i18n.Default), "")
	for _, lang := range i18n.Supported {
		got := leaves(b.LocaleTree(lang), "")
		for key, value := range base {
			if key == "format.datetime" {
				continue // a Go time layout, not a message
			}
			require.Equalf(t, ph(value), ph(got[key]),
				"%s: placeholders of %s differ", lang, key)
		}
	}
}

func TestRussianPlurals(t *testing.T) {
	l := bundle(t).Localizer("ru")

	require.Equal(t, "1 коммит", l.T("ev.push.commits", "n", 1))
	require.Equal(t, "2 коммита", l.T("ev.push.commits", "n", 2))
	require.Equal(t, "5 коммитов", l.T("ev.push.commits", "n", 5))
	require.Equal(t, "11 коммитов", l.T("ev.push.commits", "n", 11))
	require.Equal(t, "21 коммит", l.T("ev.push.commits", "n", 21))
}

func TestSimplePlurals(t *testing.T) {
	b := bundle(t)

	require.Equal(t, "1 commit", b.Localizer("en").T("ev.push.commits", "n", 1))
	require.Equal(t, "3 commits", b.Localizer("en").T("ev.push.commits", "n", 3))
	require.Equal(t, "+1 estrela: a, b", b.Localizer("pt").T("ev.star.multi", "n", 1, "actors", "a, b"))
	require.Equal(t, "+2 estrelas: a, b", b.Localizer("pt").T("ev.star.multi", "n", 2, "actors", "a, b"))
}

func TestFallbackToEnglishAndKey(t *testing.T) {
	b := bundle(t)

	// A well-formed key resolves in every locale.
	require.NotEmpty(t, b.Localizer("de").T("nav.back"))

	// An unknown key renders as itself: visible in the chat, greppable in
	// the logs, never an empty string.
	require.Equal(t, "no.such.key", b.Localizer("ru").T("no.such.key"))
}

func TestNormalize(t *testing.T) {
	require.Equal(t, "en", i18n.Normalize(""))
	require.Equal(t, "ru", i18n.Normalize("ru"))
	require.Equal(t, "pt", i18n.Normalize("pt-BR"))
	require.Equal(t, "en", i18n.Normalize("zh-CN"))
}
