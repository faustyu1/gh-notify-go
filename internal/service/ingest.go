// Package service holds application logic that sits between transport and
// storage.
package service

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

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
	store         *storage.Store
	queue         *outbox.Queue
	starDebounce  time.Duration
	starCooldown  time.Duration
}

func NewIngest(store *storage.Store, queue *outbox.Queue, starDebounce, starCooldown time.Duration) *Ingest {
	return &Ingest{
		store:        store,
		queue:        queue,
		starDebounce: starDebounce,
		starCooldown: starCooldown,
	}
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

	// star.deleted is a cancellation signal for the debounce window, not a
	// message; it is consumed before the generic Wanted check.
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
	Action string `json:"action"`
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

// handleStar implements the three anti-spam layers: the net-effect debounce
// (a pending row per integration, pushed out with every star), the per-actor
// cooldown, and coalescing (every actor in the window shares one row, so the
// message says "+N звёзд" instead of N messages).
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

	for _, integration := range integrations {
		if p.Action == "deleted" {
			if err := i.store.StarPendingRemoveActor(ctx, integration.ID, p.Sender.Login); err != nil {
				return result, err
			}
			if err := i.store.ClearStarNotified(ctx, integration.ID, p.Sender.Login); err != nil {
				return result, err
			}
			continue
		}
		if p.Action != "created" {
			continue
		}

		enabled, err := i.store.EventEnabled(ctx, integration.ID, "star")
		if err != nil {
			return result, err
		}
		if !enabled {
			result.Skipped++
			continue
		}

		// The slow cycle: unstar in the morning, star in the evening. One
		// notification per cooldown window per actor is enough.
		cooldown, err := i.store.StarCooldownActive(ctx, integration.ID, p.Sender.Login, i.starCooldown)
		if err != nil {
			return result, err
		}
		if cooldown {
			result.Skipped++
			continue
		}

		if err := i.store.MarkStarNotified(ctx, integration.ID, p.Sender.Login); err != nil {
			return result, err
		}
		if err := i.store.StarPendingAddActor(ctx,
			integration.ChatID, integration.ID, p.Sender.Login,
			p.Repo.FullName, p.Repo.StargazersCount, i.starDebounce); err != nil {
			return result, err
		}
		result.Enqueued++
	}
	return result, nil
}
