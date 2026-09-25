package storage

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// RefLink is one referral link with its funnel. Every number is an aggregate:
// the admin sees how many people a placement brought, never who they are or
// what they connected.
type RefLink struct {
	ID        int64
	Code      string
	Name      string
	CreatedAt time.Time

	// Starts counts every /start carrying the code, returning users included.
	Starts int
	// Users counts the people seen for the first time through this link.
	Users int
	// Connected counts those of them who installed the GitHub App.
	Connected int
	// Active counts those of them who connected a repository to a chat.
	Active int
}

// refCodeAlphabet avoids look-alike characters: codes get read off ad
// mock-ups and retyped by hand.
const refCodeAlphabet = "abcdefghjkmnpqrstuvwxyz23456789"

func newRefCode() (string, error) {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate ref code: %w", err)
	}
	for i, b := range buf {
		buf[i] = refCodeAlphabet[int(b)%len(refCodeAlphabet)]
	}
	return string(buf), nil
}

// CreateRefLink stores a new link under a fresh random code.
func (s *Store) CreateRefLink(ctx context.Context, userID int64, name string) (RefLink, error) {
	code, err := newRefCode()
	if err != nil {
		return RefLink{}, err
	}
	link := RefLink{Code: code, Name: name}
	err = s.pool.QueryRow(ctx, `
		INSERT INTO ref_links (code, name, created_by_user_id) VALUES ($1, $2, $3)
		RETURNING id, created_at`, code, name, userID).Scan(&link.ID, &link.CreatedAt)
	if err != nil {
		return RefLink{}, fmt.Errorf("create ref link: %w", err)
	}
	return link, nil
}

const refLinkSelect = `
	SELECT l.id, l.code, l.name, l.created_at, l.starts,
	       count(u.id),
	       count(u.id) FILTER (WHERE EXISTS (
	           SELECT 1 FROM installations i WHERE i.user_id = u.id)),
	       count(u.id) FILTER (WHERE EXISTS (
	           SELECT 1 FROM integrations g WHERE g.created_by_user_id = u.id))
	FROM ref_links l
	LEFT JOIN users u ON u.ref_link_id = l.id`

func scanRefLink(row pgx.Row) (RefLink, error) {
	var l RefLink
	err := row.Scan(&l.ID, &l.Code, &l.Name, &l.CreatedAt, &l.Starts,
		&l.Users, &l.Connected, &l.Active)
	return l, err
}

// RefLinks lists every link, newest first.
func (s *Store) RefLinks(ctx context.Context) ([]RefLink, error) {
	rows, err := s.pool.Query(ctx, refLinkSelect+`
		GROUP BY l.id ORDER BY l.created_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("list ref links: %w", err)
	}
	defer rows.Close()

	var out []RefLink
	for rows.Next() {
		l, err := scanRefLink(rows)
		if err != nil {
			return nil, fmt.Errorf("scan ref link: %w", err)
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

func (s *Store) RefLink(ctx context.Context, id int64) (RefLink, error) {
	l, err := scanRefLink(s.pool.QueryRow(ctx, refLinkSelect+`
		WHERE l.id = $1 GROUP BY l.id`, id))
	if err != nil {
		return RefLink{}, fmt.Errorf("load ref link: %w", err)
	}
	return l, nil
}

// DeleteRefLink removes the link; the users it brought stay, unattributed.
func (s *Store) DeleteRefLink(ctx context.Context, id int64) error {
	if _, err := s.pool.Exec(ctx, `DELETE FROM ref_links WHERE id = $1`, id); err != nil {
		return fmt.Errorf("delete ref link: %w", err)
	}
	return nil
}

// LanguageCount is one row of the interface-language breakdown.
type LanguageCount struct {
	Lang  string
	Users int
}

// AdminStats is the bot-wide picture for the owner. It is built from counts
// only: no repository names, chat titles, logins or ids leave this query.
type AdminStats struct {
	Users, New24h, New7d, New30d       int
	Active24h, Active7d, Active30d     int
	Blocked, FromRefs                  int
	WithGitHub, WithIntegration        int
	Installations, Chats, Integrations int
	QueuePending, SentLastHour         int
	Failed7d                           int
	Languages                          []LanguageCount
}

func (s *Store) AdminStats(ctx context.Context) (AdminStats, error) {
	var st AdminStats
	err := s.pool.QueryRow(ctx, `
		SELECT
			count(*),
			count(*) FILTER (WHERE created_at   > now() - interval '24 hours'),
			count(*) FILTER (WHERE created_at   > now() - interval '7 days'),
			count(*) FILTER (WHERE created_at   > now() - interval '30 days'),
			count(*) FILTER (WHERE last_seen_at > now() - interval '24 hours'),
			count(*) FILTER (WHERE last_seen_at > now() - interval '7 days'),
			count(*) FILTER (WHERE last_seen_at > now() - interval '30 days'),
			count(*) FILTER (WHERE blocked_at IS NOT NULL),
			count(*) FILTER (WHERE ref_link_id IS NOT NULL),
			count(*) FILTER (WHERE EXISTS (
				SELECT 1 FROM installations i WHERE i.user_id = users.id)),
			count(*) FILTER (WHERE EXISTS (
				SELECT 1 FROM integrations g WHERE g.created_by_user_id = users.id)),
			(SELECT count(*) FROM installations WHERE provider = 'github'),
			(SELECT count(*) FROM chats),
			(SELECT count(*) FROM integrations),
			(SELECT count(*) FROM outbox WHERE status = 'pending'),
			(SELECT count(*) FROM outbox
			 WHERE status = 'sent' AND sent_at > now() - interval '1 hour'),
			(SELECT count(*) FROM outbox
			 WHERE status = 'failed' AND created_at > now() - interval '7 days')
		FROM users`).Scan(
		&st.Users, &st.New24h, &st.New7d, &st.New30d,
		&st.Active24h, &st.Active7d, &st.Active30d,
		&st.Blocked, &st.FromRefs, &st.WithGitHub, &st.WithIntegration,
		&st.Installations, &st.Chats, &st.Integrations,
		&st.QueuePending, &st.SentLastHour, &st.Failed7d)
	if err != nil {
		return AdminStats{}, fmt.Errorf("admin stats: %w", err)
	}

	rows, err := s.pool.Query(ctx, `
		SELECT language, count(*) FROM users GROUP BY language ORDER BY count(*) DESC`)
	if err != nil {
		return AdminStats{}, fmt.Errorf("admin language stats: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var lc LanguageCount
		if err := rows.Scan(&lc.Lang, &lc.Users); err != nil {
			return AdminStats{}, fmt.Errorf("scan language stats: %w", err)
		}
		st.Languages = append(st.Languages, lc)
	}
	return st, rows.Err()
}

// Broadcast statuses. A draft waits for confirmation, running is picked up
// by the broadcaster, done and cancelled are final.
const (
	BroadcastDraft     = "draft"
	BroadcastRunning   = "running"
	BroadcastDone      = "done"
	BroadcastCancelled = "cancelled"
)

var ErrBroadcastNotFound = errors.New("broadcast not found")

type Broadcast struct {
	ID              int64
	SourceChatID    int64
	SourceMessageID int
	Status          string
	Total           int
	Sent            int
	Failed          int
	LastUserID      int64
	CreatedAt       time.Time
	StartedAt       *time.Time
	FinishedAt      *time.Time

	// The admin who created it, for the completion notice.
	AdminTelegramID int64
	AdminLang       string
}

const broadcastSelect = `
	SELECT b.id, b.source_chat_id, b.source_message_id, b.status, b.total,
	       b.sent, b.failed, b.last_user_id, b.created_at, b.started_at,
	       b.finished_at, coalesce(u.telegram_id, 0), coalesce(u.language, '')
	FROM broadcasts b
	LEFT JOIN users u ON u.id = b.created_by_user_id`

func scanBroadcast(row pgx.Row) (Broadcast, error) {
	var b Broadcast
	err := row.Scan(&b.ID, &b.SourceChatID, &b.SourceMessageID, &b.Status,
		&b.Total, &b.Sent, &b.Failed, &b.LastUserID, &b.CreatedAt,
		&b.StartedAt, &b.FinishedAt, &b.AdminTelegramID, &b.AdminLang)
	if errors.Is(err, pgx.ErrNoRows) {
		return Broadcast{}, ErrBroadcastNotFound
	}
	return b, err
}

// CreateBroadcast records a draft pointing at the message to copy.
func (s *Store) CreateBroadcast(
	ctx context.Context, userID, sourceChatID int64, sourceMessageID int,
) (int64, error) {
	var id int64
	err := s.pool.QueryRow(ctx, `
		INSERT INTO broadcasts (created_by_user_id, source_chat_id, source_message_id)
		VALUES ($1, $2, $3) RETURNING id`, userID, sourceChatID, sourceMessageID).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("create broadcast: %w", err)
	}
	return id, nil
}

func (s *Store) Broadcast(ctx context.Context, id int64) (Broadcast, error) {
	b, err := scanBroadcast(s.pool.QueryRow(ctx, broadcastSelect+` WHERE b.id = $1`, id))
	if err != nil {
		return Broadcast{}, fmt.Errorf("load broadcast: %w", err)
	}
	return b, nil
}

// Broadcasts lists the most recent broadcasts, newest first.
func (s *Store) Broadcasts(ctx context.Context, limit int) ([]Broadcast, error) {
	rows, err := s.pool.Query(ctx, broadcastSelect+`
		ORDER BY b.id DESC LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("list broadcasts: %w", err)
	}
	defer rows.Close()

	var out []Broadcast
	for rows.Next() {
		b, err := scanBroadcast(rows)
		if err != nil {
			return nil, fmt.Errorf("scan broadcast: %w", err)
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// BroadcastAudience counts who a broadcast started now would reach.
func (s *Store) BroadcastAudience(ctx context.Context) (int, error) {
	var n int
	if err := s.pool.QueryRow(ctx,
		`SELECT count(*) FROM users WHERE blocked_at IS NULL`).Scan(&n); err != nil {
		return 0, fmt.Errorf("broadcast audience: %w", err)
	}
	return n, nil
}

// StartBroadcast moves a draft to running. Starting anything else is a
// no-op, so a double tap on "send" cannot queue the message twice.
func (s *Store) StartBroadcast(ctx context.Context, id int64) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE broadcasts
		SET status = 'running', started_at = now(),
		    total = (SELECT count(*) FROM users WHERE blocked_at IS NULL)
		WHERE id = $1 AND status = 'draft'`, id)
	if err != nil {
		return fmt.Errorf("start broadcast: %w", err)
	}
	return nil
}

// CancelBroadcast stops a draft or a running broadcast. Messages already
// delivered stay delivered.
func (s *Store) CancelBroadcast(ctx context.Context, id int64) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE broadcasts SET status = 'cancelled', finished_at = now()
		WHERE id = $1 AND status IN ('draft', 'running')`, id)
	if err != nil {
		return fmt.Errorf("cancel broadcast: %w", err)
	}
	return nil
}

// NextRunningBroadcast returns the oldest running broadcast, if any.
func (s *Store) NextRunningBroadcast(ctx context.Context) (Broadcast, bool, error) {
	b, err := scanBroadcast(s.pool.QueryRow(ctx, broadcastSelect+`
		WHERE b.status = 'running' ORDER BY b.id LIMIT 1`))
	if errors.Is(err, ErrBroadcastNotFound) {
		return Broadcast{}, false, nil
	}
	if err != nil {
		return Broadcast{}, false, fmt.Errorf("next broadcast: %w", err)
	}
	return b, true, nil
}

// BroadcastTarget is one recipient.
type BroadcastTarget struct {
	UserID     int64
	TelegramID int64
}

// BroadcastTargets pages through reachable users in id order after the
// given id.
func (s *Store) BroadcastTargets(
	ctx context.Context, afterUserID int64, limit int,
) ([]BroadcastTarget, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, telegram_id FROM users
		WHERE id > $1 AND blocked_at IS NULL
		ORDER BY id LIMIT $2`, afterUserID, limit)
	if err != nil {
		return nil, fmt.Errorf("broadcast targets: %w", err)
	}
	defer rows.Close()

	var out []BroadcastTarget
	for rows.Next() {
		var t BroadcastTarget
		if err := rows.Scan(&t.UserID, &t.TelegramID); err != nil {
			return nil, fmt.Errorf("scan broadcast target: %w", err)
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// AdvanceBroadcast records one processed batch. It reports false when the
// broadcast is no longer running — cancelled from the admin screen — so the
// sender stops instead of carrying on.
func (s *Store) AdvanceBroadcast(
	ctx context.Context, id, lastUserID int64, sent, failed int,
) (bool, error) {
	tag, err := s.pool.Exec(ctx, `
		UPDATE broadcasts
		SET last_user_id = $2, sent = sent + $3, failed = failed + $4
		WHERE id = $1 AND status = 'running'`, id, lastUserID, sent, failed)
	if err != nil {
		return false, fmt.Errorf("advance broadcast: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// FinishBroadcast marks a running broadcast done and returns its final
// numbers; false when it was cancelled in the meantime.
func (s *Store) FinishBroadcast(ctx context.Context, id int64) (Broadcast, bool, error) {
	tag, err := s.pool.Exec(ctx, `
		UPDATE broadcasts SET status = 'done', finished_at = now()
		WHERE id = $1 AND status = 'running'`, id)
	if err != nil {
		return Broadcast{}, false, fmt.Errorf("finish broadcast: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return Broadcast{}, false, nil
	}
	b, err := s.Broadcast(ctx, id)
	return b, err == nil, err
}

// MarkUserBlocked records that the user blocked the bot, which keeps them
// out of later broadcasts until they come back.
func (s *Store) MarkUserBlocked(ctx context.Context, userID int64) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE users SET blocked_at = now() WHERE id = $1`, userID)
	if err != nil {
		return fmt.Errorf("mark user blocked: %w", err)
	}
	return nil
}
