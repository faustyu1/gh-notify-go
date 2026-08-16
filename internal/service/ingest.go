// Package service holds application logic that sits between transport and
// storage.
package service

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/faustyu/gh-notify-go/internal/events"
	"github.com/faustyu/gh-notify-go/internal/ghapp"
	"github.com/faustyu/gh-notify-go/internal/outbox"
	"github.com/faustyu/gh-notify-go/internal/storage"
)

// Result is returned to the webhook endpoint and logged. It exists so an
// operator can tell "nobody subscribed" apart from "we dropped it".
type Result struct {
	Matched   int
	Enqueued  int
	Skipped   int
	Duplicate bool
}

type Ingest struct {
	store *storage.Store
	queue *outbox.Queue
}

func NewIngest(store *storage.Store, queue *outbox.Queue) *Ingest {
	return &Ingest{store: store, queue: queue}
}

// Handle fans one webhook out to every subscribed chat. It only writes to the
// database — no Telegram call happens on this path, so GitHub gets its 200
// regardless of Telegram's health.
func (i *Ingest) Handle(ctx context.Context, env ghapp.Envelope) (Result, error) {
	var result Result

	// The installation lifecycle is not a notification; it maintains the
	// installations table itself and never reaches the outbox.
	if env.Kind == "installation" {
		fresh, err := i.queue.MarkDelivered(ctx, env.DeliveryID)
		if err != nil {
			return result, fmt.Errorf("dedup delivery: %w", err)
		}
		if !fresh {
			result.Duplicate = true
			return result, nil
		}
		return result, i.handleInstallation(ctx, env)
	}

	// star has its own one-notification-per-user rule, independent of the
	// action registry.
	if env.Kind == "star" {
		fresh, err := i.queue.MarkDelivered(ctx, env.DeliveryID)
		if err != nil {
			return result, fmt.Errorf("dedup delivery: %w", err)
		}
		if !fresh {
			result.Duplicate = true
			return result, nil
		}
		return i.handleStar(ctx, env)
	}

	// Checked before any database work: most deliveries are actions nobody
	// wants, and this keeps them from costing a query.
	if !events.Wanted(events.Kind(env.Kind), env.Action) {
		return result, nil
	}

	fresh, err := i.queue.MarkDelivered(ctx, env.DeliveryID)
	if err != nil {
		return result, fmt.Errorf("dedup delivery: %w", err)
	}
	if !fresh {
		result.Duplicate = true
		return result, nil
	}

	integrations, err := i.store.IntegrationsForRepo(ctx, env.RepoGitHubID, env.InstallationID)
	if err != nil {
		return result, fmt.Errorf("find integrations: %w", err)
	}
	result.Matched = len(integrations)

	for _, integration := range integrations {
		enabled, err := i.store.EventEnabled(ctx, integration.ID, env.Kind)
		if err != nil {
			return result, fmt.Errorf("check event setting: %w", err)
		}
		if !enabled {
			result.Skipped++
			continue
		}

		rules, err := i.store.FiltersForIntegration(ctx, integration.ID)
		if err != nil {
			return result, fmt.Errorf("load filters: %w", err)
		}
		if len(rules) > 0 {
			var converted []ignoreFilter
			for _, r := range rules {
				converted = append(converted, ignoreFilter{Kind: r.Kind, Pattern: r.Value})
			}
			if filterIgnored(env.Kind, env.Raw, converted) {
				result.Skipped++
				continue
			}
		}

		if _, err := i.queue.Enqueue(ctx, outbox.Row{
			ChatID:        integration.ChatID,
			IntegrationID: integration.ID,
			Kind:          env.Kind,
			Payload:       env.Raw,
		}); err != nil {
			return result, fmt.Errorf("enqueue for integration %d: %w", integration.ID, err)
		}
		result.Enqueued++
	}
	return result, nil
}

type installationPayload struct {
	Action       string `json:"action"`
	Installation struct {
		ID      int64 `json:"id"`
		Account struct {
			Login string `json:"login"`
			Type  string `json:"type"`
		} `json:"account"`
	} `json:"installation"`
}

func (i *Ingest) handleInstallation(ctx context.Context, env ghapp.Envelope) error {
	var p installationPayload
	if err := json.Unmarshal(env.Raw, &p); err != nil {
		return fmt.Errorf("parse installation: %w", err)
	}

	switch p.Action {
	case "created", "new_permissions_accepted":
		return i.store.RegisterInstallation(ctx,
			p.Installation.ID, p.Installation.Account.Login, p.Installation.Account.Type)
	case "suspend":
		return i.store.SetInstallationSuspended(ctx, p.Installation.ID, true)
	case "unsuspend":
		return i.store.SetInstallationSuspended(ctx, p.Installation.ID, false)
	case "deleted":
		return i.store.DeleteInstallation(ctx, p.Installation.ID)
	}
	return nil
}

// starOutPayload mirrors the star renderer's payload shape.
type starOutPayload struct {
	RepoFullName string   `json:"repo_full_name"`
	Actors       []string `json:"actors"`
	TotalStars   int      `json:"total_stars"`
}

type starEnvelope struct {
	Action string `json:"action"`
	Sender struct {
		Login string `json:"login"`
	} `json:"sender"`
	Repo struct {
		FullName        string `json:"full_name"`
		StargazersCount int    `json:"stargazers_count"`
	} `json:"repository"`
}

// handleStar implements the one rule that matters: a user gets exactly one
// star notification per repository, ever. The first star is delivered at
// once; every later star — including the whole star/unstar toggle dance — is
// silent.
func (i *Ingest) handleStar(ctx context.Context, env ghapp.Envelope) (Result, error) {
	var result Result

	var p starEnvelope
	if err := json.Unmarshal(env.Raw, &p); err != nil {
		return result, fmt.Errorf("parse star: %w", err)
	}

	integrations, err := i.store.IntegrationsForRepo(ctx, env.RepoGitHubID, env.InstallationID)
	if err != nil {
		return result, fmt.Errorf("find integrations: %w", err)
	}
	result.Matched = len(integrations)

	if p.Action != "created" {
		return result, nil // deleted: nothing to cancel, nothing to say
	}

	for _, integration := range integrations {
		enabled, err := i.store.EventEnabled(ctx, integration.ID, "star")
		if err != nil {
			return result, err
		}
		if !enabled {
			result.Skipped++
			continue
		}

		notified, err := i.store.StarActorNotified(ctx, integration.ID, p.Sender.Login)
		if err != nil {
			return result, err
		}
		if notified {
			result.Skipped++
			continue
		}

		if err := i.store.MarkStarNotified(ctx, integration.ID, p.Sender.Login); err != nil {
			return result, err
		}
		payload, err := json.Marshal(starOutPayload{
			RepoFullName: p.Repo.FullName,
			Actors:       []string{p.Sender.Login},
			TotalStars:   p.Repo.StargazersCount,
		})
		if err != nil {
			return result, fmt.Errorf("encode star: %w", err)
		}
		if _, err := i.queue.Enqueue(ctx, outbox.Row{
			ChatID:        integration.ChatID,
			IntegrationID: integration.ID,
			Kind:          "star",
			Payload:       payload,
		}); err != nil {
			return result, err
		}
		result.Enqueued++
	}
	return result, nil
}
