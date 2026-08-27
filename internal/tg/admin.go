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
	ownerID int64
	expires time.Time
}

func NewAdminChecker(api AdminAPI, ttl time.Duration) *AdminChecker {
	return &AdminChecker{api: api, ttl: ttl, cache: map[int64]adminEntry{}}
}

func (a *AdminChecker) IsAdmin(
	ctx context.Context, telegramChatID, telegramUserID int64,
) (bool, error) {
	entry, err := a.entry(ctx, telegramChatID)
	if err != nil {
		return false, err
	}
	_, isAdmin := entry.ids[telegramUserID]
	return isAdmin, nil
}

// IsOwner narrows the question to the one admin Telegram calls the chat's
// creator. Ownership is what lets a group keep control of an integration its
// author is no longer around to remove — an ordinary admin cannot touch
// somebody else's.
func (a *AdminChecker) IsOwner(
	ctx context.Context, telegramChatID, telegramUserID int64,
) (bool, error) {
	entry, err := a.entry(ctx, telegramChatID)
	if err != nil {
		return false, err
	}
	return entry.ownerID != 0 && entry.ownerID == telegramUserID, nil
}

// entry returns a fresh-enough administrator list, fetching it at most once
// per chat per TTL however many questions are asked of it.
func (a *AdminChecker) entry(ctx context.Context, telegramChatID int64) (adminEntry, error) {
	a.mu.Lock()
	entry, ok := a.cache[telegramChatID]
	a.mu.Unlock()

	if ok && time.Now().Before(entry.expires) {
		return entry, nil
	}

	members, err := a.api.GetChatAdministrators(ctx,
		&telego.GetChatAdministratorsParams{ChatID: telego.ChatID{ID: telegramChatID}})
	if err != nil {
		return adminEntry{}, fmt.Errorf("get chat administrators: %w", err)
	}

	ids := make(map[int64]struct{}, len(members))
	var ownerID int64
	for _, member := range members {
		ids[member.MemberUser().ID] = struct{}{}
		if member.MemberStatus() == telego.MemberStatusCreator {
			ownerID = member.MemberUser().ID
		}
	}
	entry = adminEntry{ids: ids, ownerID: ownerID, expires: time.Now().Add(a.ttl)}

	a.mu.Lock()
	a.cache[telegramChatID] = entry
	a.mu.Unlock()
	return entry, nil
}
