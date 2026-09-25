// Package forge describes the code hosts the bot takes events from. Each
// host is a Provider: what it is called, how its deliveries authenticate, how
// its payloads name their actor, branch and labels, and — for hosts connected
// by a hand-made webhook rather than an App — how a delivery is parsed into
// the envelope the ingest path routes on.
//
// Everything that used to branch on "is this GitLab?" asks the provider
// instead, so a new host is one file here plus its renderers.
package forge

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
)

// AuthMode is how a provider's deliveries prove where they come from.
type AuthMode int

const (
	// AuthApp is the GitHub App: one signing secret for the whole bot.
	AuthApp AuthMode = iota
	// AuthToken is a per-connection secret sent as a header. The token alone
	// both authenticates the delivery and names its connection, so every
	// connection shares one URL.
	AuthToken
	// AuthSignature is an HMAC over the body with a per-connection secret.
	// The signature cannot say which connection it is for, so the
	// connection's id travels in the URL.
	AuthSignature
)

// Envelope is the subset of a webhook delivery the router needs. Kind is
// empty for an event the bot does not deliver; the project is still worth
// remembering then, because it proves the webhook works.
type Envelope struct {
	DeliveryID  string
	Kind        string
	Action      string
	ProjectID   int64
	ProjectPath string
	ProjectURL  string
	Raw         json.RawMessage
}

// Subjects are what ignore rules match against. Missing subjects are empty
// and never match.
type Subjects struct {
	Author string
	Branch string
	Labels []string
	Action string
}

// Provider is one code host.
type Provider interface {
	// ID is what the installations table stores.
	ID() string
	// Name is how the host is called in the interface.
	Name() string
	// KindPrefix marks the event kinds this host's payloads render as. Hosts
	// that share a payload format share a prefix.
	KindPrefix() string
	Auth() AuthMode
	Subjects(kind string, raw json.RawMessage) Subjects
}

// Hook is a provider whose connections are webhooks the user adds by hand:
// the bot mints a secret, the user pastes it into the host's webhook form,
// and repositories are learned from the deliveries.
type Hook interface {
	Provider
	// Token extracts the connection token from an AuthToken delivery.
	Token(h http.Header) string
	// Verify checks an AuthSignature delivery against its connection's
	// secret.
	Verify(secret string, h http.Header, body []byte) bool
	Parse(h http.Header, body []byte) (Envelope, error)
}

var providers = []Provider{GitHub, GitLab, Gitea, Forgejo, GitVerse}

// All lists every provider in the order the interface offers them.
func All() []Provider { return providers }

// Get finds a provider by its stored id.
func Get(id string) (Provider, bool) {
	for _, p := range providers {
		if p.ID() == id {
			return p, true
		}
	}
	return nil, false
}

// Hooks lists the providers connected by a webhook.
func Hooks() []Hook {
	var out []Hook
	for _, p := range providers {
		if h, ok := p.(Hook); ok {
			out = append(out, h)
		}
	}
	return out
}

// HookFor finds a webhook provider by its stored id.
func HookFor(id string) (Hook, bool) {
	p, ok := Get(id)
	if !ok {
		return nil, false
	}
	h, ok := p.(Hook)
	return h, ok
}

// Name is the display name of a stored provider id, the id itself when the
// provider is unknown.
func Name(id string) string {
	if p, ok := Get(id); ok {
		return p.Name()
	}
	return id
}

// PrefixOf returns the provider prefix a kind carries; GitHub kinds carry
// none.
func PrefixOf(kind string) string {
	for _, p := range providers {
		if pre := p.KindPrefix(); pre != "" && strings.HasPrefix(kind, pre) {
			return pre
		}
	}
	return ""
}

// ForKind returns the provider whose payload format a kind renders. Hosts
// that share a format are interchangeable here.
func ForKind(kind string) Provider {
	pre := PrefixOf(kind)
	for _, p := range providers {
		if p.KindPrefix() == pre {
			return p
		}
	}
	return GitHub
}

// Label is how a kind is shown on a toggle: the prefix only separates the
// providers internally, and an integration never sees another provider's
// kinds.
func Label(kind string) string {
	return strings.TrimPrefix(kind, PrefixOf(kind))
}

// WebhookPath is where a connection's host posts its deliveries.
func WebhookPath(h Hook, installationID int64) string {
	path := "/hook/" + h.ID()
	if h.Auth() == AuthSignature {
		path += "/" + strconv.FormatInt(installationID, 10)
	}
	return path
}

// bodyID is the delivery id of last resort: the body's hash, so a manual
// resend is still recognised as the same delivery.
func bodyID(provider string, body []byte) string {
	sum := sha256.Sum256(body)
	return provider + ":sha256:" + hex.EncodeToString(sum[:])
}

// validHMAC reports whether sig is the hex HMAC-SHA256 of body under secret.
func validHMAC(secret string, body []byte, sig string) bool {
	if secret == "" || sig == "" {
		return false
	}
	want, err := hex.DecodeString(sig)
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hmac.Equal(mac.Sum(nil), want)
}

func trimRef(ref string) string {
	return strings.TrimPrefix(strings.TrimPrefix(ref, "refs/heads/"), "refs/tags/")
}
