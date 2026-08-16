// Package ghapp holds everything specific to GitHub App authentication and
// the App-level webhook.
package ghapp

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

var ErrBadSignature = errors.New("invalid webhook signature")

// VerifySignature checks the X-Hub-Signature-256 header. The comparison is
// constant time so a forged signature cannot be discovered byte by byte.
func VerifySignature(secret string, body []byte, header string) error {
	const prefix = "sha256="
	if !strings.HasPrefix(header, prefix) {
		return ErrBadSignature
	}
	want, err := hex.DecodeString(strings.TrimPrefix(header, prefix))
	if err != nil {
		return ErrBadSignature
	}

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	if !hmac.Equal(mac.Sum(nil), want) {
		return ErrBadSignature
	}
	return nil
}

// Envelope is the subset of every webhook payload the router needs. The full
// payload stays in Raw and is decoded later by the per-event parser.
type Envelope struct {
	DeliveryID     string
	Kind           string
	Action         string
	RepoGitHubID   int64
	RepoFullName   string
	InstallationID int64
	Raw            json.RawMessage
}

type envelopeShape struct {
	Action     string `json:"action"`
	Repository *struct {
		ID       int64  `json:"id"`
		FullName string `json:"full_name"`
	} `json:"repository"`
	Installation *struct {
		ID int64 `json:"id"`
	} `json:"installation"`
}

func ParseEnvelope(kind, deliveryID string, body []byte) (Envelope, error) {
	var shape envelopeShape
	if err := json.Unmarshal(body, &shape); err != nil {
		return Envelope{}, fmt.Errorf("parse envelope: %w", err)
	}

	env := Envelope{
		DeliveryID: deliveryID,
		Kind:       kind,
		Action:     shape.Action,
		Raw:        json.RawMessage(body),
	}
	if shape.Repository != nil {
		env.RepoGitHubID = shape.Repository.ID
		env.RepoFullName = shape.Repository.FullName
	}
	if shape.Installation != nil {
		env.InstallationID = shape.Installation.ID
	}
	return env, nil
}
