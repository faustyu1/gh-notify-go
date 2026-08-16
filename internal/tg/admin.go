package tg

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/mymmrac/telego"
)

type AdminAPI interface {
	GetChatAdministrators(
		ctx context.Context, params *telego.GetChatAdministratorsParams,
	) ([]telego.ChatMember, error)
}

// AdminChecker answers "may this user manage this chat" with a short cache.
// The cache is deliberately short: a demoted admin must lose access quickly,
// but a burst of button taps should not become a burst of API calls.
type AdminChecker struct {
	api AdminAPI
	ttl time.Duration

	mu    sync.Mutex
	cache map[int64]adminEntry
}

type adminEntry struct {
	ids     map[int64]struct{}
	expires time.Time
}

func NewAdminChecker(api AdminAPI, ttl time.Duration) *AdminChecker {
	return &AdminChecker{api: api, ttl: ttl, cache: map[int64]adminEntry{}}
}

func (a *AdminChecker) IsAdmin(
	ctx context.Context, telegramChatID, telegramUserID int64,
) (bool, error) {
	a.mu.Lock()
	entry, ok := a.cache[telegramChatID]
	a.mu.Unlock()

	if !ok || time.Now().After(entry.expires) {
		members, err := a.api.GetChatAdministrators(ctx,
			&telego.GetChatAdministratorsParams{ChatID: telego.ChatID{ID: telegramChatID}})
		if err != nil {
			return false, fmt.Errorf("get chat administrators: %w", err)
		}

		ids := make(map[int64]struct{}, len(members))
		for _, member := range members {
			ids[member.MemberUser().ID] = struct{}{}
		}
		entry = adminEntry{ids: ids, expires: time.Now().Add(a.ttl)}

		a.mu.Lock()
		a.cache[telegramChatID] = entry
		a.mu.Unlock()
	}

	_, isAdmin := entry.ids[telegramUserID]
	return isAdmin, nil
}
