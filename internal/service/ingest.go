// Package service holds application logic that sits between transport and
// storage.
package service

import (
	"context"
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
