package screens

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/faustyu/gh-notify-go/internal/events/render"
	"github.com/faustyu/gh-notify-go/internal/i18n"
	"github.com/faustyu/gh-notify-go/internal/storage"
	"github.com/faustyu/gh-notify-go/internal/tg/ui"
)

// AdminStore is everything the admin panel reads. Nothing in it returns a
// repository, a chat title or a login: the panel works from counts.
type AdminStore interface {
	AdminStats(ctx context.Context) (storage.AdminStats, error)
	RefLinks(ctx context.Context) ([]storage.RefLink, error)
	RefLink(ctx context.Context, id int64) (storage.RefLink, error)
	Broadcasts(ctx context.Context, limit int) ([]storage.Broadcast, error)
	Broadcast(ctx context.Context, id int64) (storage.Broadcast, error)
	BroadcastAudience(ctx context.Context) (int, error)
}

// RefStartPrefix is the /start payload prefix of a referral deep link.
const RefStartPrefix = "r_"

// RefURL is the deep link a referral code is shared as.
func RefURL(botUser, code string) string {
	return "https://t.me/" + botUser + "?start=" + RefStartPrefix + code
}

// num groups thousands with a narrow no-break space: 12 345 reads at a
// glance, 12345 does not.
func num(n int) string {
	s := strconv.Itoa(n)
	if len(s) <= 3 {
		return s
	}
	var b strings.Builder
	lead := len(s) % 3
	if lead > 0 {
		b.WriteString(s[:lead])
	}
	for i := lead; i < len(s); i += 3 {
		if b.Len() > 0 {
			b.WriteString(" ")
		}
		b.WriteString(s[i : i+3])
	}
	return b.String()
}

// pct is part as a share of whole, "—" when there is no whole to speak of.
func pct(part, whole int) string {
	if whole <= 0 {
		return "—"
	}
	return strconv.Itoa(part*100/whole) + "%"
}

// bar draws a ten-segment meter.
func bar(part, whole int) string {
	const width = 10
	filled := 0
	if whole > 0 {
		filled = min(width, (part*width+whole/2)/whole)
	}
	return strings.Repeat("▰", filled) + strings.Repeat("▱", width-filled)
}

func adminTitle(icon, fallback, title string) string {
	return render.Emoji(icon, fallback) + " <b>" + title + "</b>"
}

// refreshButton re-renders a screen in place, so tapping it any number of
// times leaves ◁ Назад pointing where it did.
func refreshButton(l *i18n.Localizer, screen string, params ui.Params) ui.Button {
	p := ui.Params{"screen": screen}
	for k, v := range params {
		p[k] = v
	}
	return ui.Button{Label: l.T("admin.btn_refresh"), Icon: render.EmojiRefresh,
		Screen: "a_refresh", Params: p}
}

// --- adm_home ----------------------------------------------------------------

type adminHome struct {
	store AdminStore
	loc   *i18n.Bundle
}

func NewAdminHome(store AdminStore, loc *i18n.Bundle) ui.Screen {
	return adminHome{store: store, loc: loc}
}

func (adminHome) Name() string { return "adm_home" }

func (a adminHome) Render(ctx context.Context, s ui.Session) (ui.View, error) {
	st, err := a.store.AdminStats(ctx)
	if err != nil {
		return ui.View{}, err
	}
	l := a.loc.Localizer(s.Lang)

	text := adminTitle(render.EmojiLockClosed, "🔐", l.T("admin.title")) + "\n\n" +
		render.Emoji(render.EmojiPeople, "👥") + " " +
		l.T("admin.users_line", "n", num(st.Users), "new", num(st.New24h)) + "\n" +
		render.Emoji(render.EmojiBolt, "⚡") + " " +
		l.T("admin.active_line", "n", num(st.Active24h))

	return ui.View{
		Text: text,
		Rows: [][]ui.Button{
			{
				{Label: l.T("admin.btn_stats"), Icon: render.EmojiStats, Screen: "adm_stats"},
				{Label: l.T("admin.btn_refs"), Icon: render.EmojiLink, Screen: "adm_refs"},
			},
			{{Label: l.T("admin.btn_broadcast"), Icon: render.EmojiMegaphone, Screen: "adm_bcs"}},
			{{Label: l.T("btn.home"), Icon: render.EmojiHouse, Screen: "home"}},
		},
	}, nil
}

// --- adm_stats ---------------------------------------------------------------

type adminStats struct {
	store AdminStore
	loc   *i18n.Bundle
}

func NewAdminStats(store AdminStore, loc *i18n.Bundle) ui.Screen {
	return adminStats{store: store, loc: loc}
}

func (adminStats) Name() string { return "adm_stats" }

func (a adminStats) Render(ctx context.Context, s ui.Session) (ui.View, error) {
	st, err := a.store.AdminStats(ctx)
	if err != nil {
		return ui.View{}, err
	}
	l := a.loc.Localizer(s.Lang)

	var b strings.Builder
	b.WriteString(adminTitle(render.EmojiStats, "📊", l.T("admin.stats.title")))
	b.WriteString("\n<i>" + l.T("admin.stats.privacy") + "</i>\n\n")

	b.WriteString(render.Emoji(render.EmojiPeople, "👥") + " <b>" + l.T("admin.stats.users") + "</b>\n")
	b.WriteString(l.T("admin.stats.total", "n", num(st.Users)) + "\n")
	b.WriteString(l.T("admin.stats.new", "d", num(st.New24h), "w", num(st.New7d), "m", num(st.New30d)) + "\n")
	b.WriteString(l.T("admin.stats.active", "d", num(st.Active24h), "w", num(st.Active7d), "m", num(st.Active30d)) + "\n")
	b.WriteString(l.T("admin.stats.from_refs", "n", num(st.FromRefs)) + "\n")
	b.WriteString(l.T("admin.stats.blocked", "n", num(st.Blocked)) + "\n\n")

	b.WriteString(render.Emoji(render.EmojiChart, "📈") + " <b>" + l.T("admin.stats.funnel") + "</b>\n")
	b.WriteString("<code>" + bar(st.WithGitHub, st.Users) + "</code> " +
		l.T("admin.stats.with_github", "n", num(st.WithGitHub), "pct", pct(st.WithGitHub, st.Users)) + "\n")
	b.WriteString("<code>" + bar(st.WithIntegration, st.Users) + "</code> " +
		l.T("admin.stats.with_integration", "n", num(st.WithIntegration), "pct", pct(st.WithIntegration, st.Users)) + "\n\n")

	b.WriteString(render.Emoji(render.EmojiBox, "📦") + " <b>" + l.T("admin.stats.volume") + "</b>\n")
	b.WriteString(l.T("admin.stats.volume_line", "installs", num(st.Installations),
		"chats", num(st.Chats), "integrations", num(st.Integrations)) + "\n\n")

	b.WriteString(render.Emoji(render.EmojiUpload, "📨") + " <b>" + l.T("admin.stats.delivery") + "</b>\n")
	b.WriteString(l.T("admin.stats.delivery_line", "pending", num(st.QueuePending),
		"sent", num(st.SentLastHour), "failed", num(st.Failed7d)))

	if len(st.Languages) > 0 {
		b.WriteString("\n\n" + render.Emoji(render.EmojiLanguage, "🌐") + " <b>" + l.T("admin.stats.languages") + "</b>\n")
		for _, lc := range st.Languages {
			b.WriteString(fmt.Sprintf("<code>%-2s %s %4s</code> %s\n",
				render.Escape(lc.Lang), bar(lc.Users, st.Users), pct(lc.Users, st.Users), num(lc.Users)))
		}
	}

	return ui.View{
		Text: strings.TrimRight(b.String(), "\n"),
		Rows: [][]ui.Button{{refreshButton(l, "adm_stats", nil)}},
	}, nil
}

// --- adm_refs ----------------------------------------------------------------

type adminRefs struct {
	store AdminStore
	loc   *i18n.Bundle
}

func NewAdminRefs(store AdminStore, loc *i18n.Bundle) ui.Screen {
	return adminRefs{store: store, loc: loc}
}

func (adminRefs) Name() string { return "adm_refs" }

func (a adminRefs) Render(ctx context.Context, s ui.Session) (ui.View, error) {
	links, err := a.store.RefLinks(ctx)
	if err != nil {
		return ui.View{}, err
	}
	l := a.loc.Localizer(s.Lang)

	text := adminTitle(render.EmojiLink, "🔗", l.T("admin.refs.title")) + "\n\n"
	if len(links) == 0 {
		text += l.T("admin.refs.empty")
	} else {
		users := 0
		for _, link := range links {
			users += link.Users
		}
		text += l.T("admin.refs.hint") + "\n\n" +
			l.T("admin.refs.summary", "links", num(len(links)), "users", num(users))
	}

	rows := make([][]ui.Button, 0, len(links)+1)
	for _, link := range links {
		rows = append(rows, []ui.Button{{
			Label:  l.T("admin.refs.entry", "name", link.Name, "n", num(link.Users)),
			Icon:   render.EmojiTag,
			Screen: "adm_ref",
			Params: ui.Params{"ref": strconv.FormatInt(link.ID, 10)},
		}})
	}
	rows = append(rows, []ui.Button{{Label: l.T("admin.refs.new"),
		Icon: render.EmojiPlus, Screen: "adm_ref_new"}})

	return ui.View{Text: text, Rows: rows}, nil
}

// --- adm_ref -----------------------------------------------------------------

type adminRef struct {
	store   AdminStore
	botUser string
	loc     *i18n.Bundle
}

func NewAdminRef(store AdminStore, botUser string, loc *i18n.Bundle) ui.Screen {
	return adminRef{store: store, botUser: botUser, loc: loc}
}

func (adminRef) Name() string { return "adm_ref" }

func (a adminRef) Render(ctx context.Context, s ui.Session) (ui.View, error) {
	id, _ := strconv.ParseInt(s.Params["ref"], 10, 64)
	link, err := a.store.RefLink(ctx, id)
	if err != nil {
		return ui.View{}, err
	}
	l := a.loc.Localizer(s.Lang)
	deepLink := RefURL(a.botUser, link.Code)
	params := ui.Params{"ref": s.Params["ref"]}

	// Deleting is one tap away from a screen people open to copy a link, so
	// it asks first — in place, without a separate screen on the stack.
	if s.Params["confirm"] == "1" {
		return ui.View{
			Text: render.Emoji(render.EmojiWarning, "⚠️") + " <b>" +
				render.Escape(link.Name) + "</b>\n\n" + l.T("admin.ref.confirm"),
			Rows: [][]ui.Button{{
				{Label: l.T("admin.ref.confirm_yes"), Icon: render.EmojiTrash,
					Screen: "adm_ref_del", Params: params},
				{Label: l.T("admin.ref.cancel"), Icon: render.EmojiCross,
					Screen: "a_refresh", Params: ui.Params{"screen": "adm_ref", "ref": s.Params["ref"]}},
			}},
		}, nil
	}

	text := adminTitle(render.EmojiTag, "🏷", render.Escape(link.Name)) + "\n\n" +
		render.Emoji(render.EmojiLink, "🔗") + " <code>" + render.Escape(deepLink) + "</code>\n" +
		render.Emoji(render.EmojiCalendar, "📅") + " " +
		l.T("admin.ref.created", "time", link.CreatedAt.Local().Format(l.DateTimeLayout())) + "\n\n" +
		render.Emoji(render.EmojiChart, "📈") + " <b>" + l.T("admin.ref.funnel") + "</b>\n" +
		l.T("admin.ref.starts", "n", num(link.Starts)) + "\n" +
		"<code>" + bar(link.Users, link.Starts) + "</code> " +
		l.T("admin.ref.users", "n", num(link.Users), "pct", pct(link.Users, link.Starts)) + "\n" +
		"<code>" + bar(link.Connected, link.Users) + "</code> " +
		l.T("admin.ref.connected", "n", num(link.Connected), "pct", pct(link.Connected, link.Users)) + "\n" +
		"<code>" + bar(link.Active, link.Users) + "</code> " +
		l.T("admin.ref.active", "n", num(link.Active), "pct", pct(link.Active, link.Users))

	confirm := ui.Params{"screen": "adm_ref", "ref": s.Params["ref"], "confirm": "1"}
	return ui.View{
		Text: text,
		Rows: [][]ui.Button{
			{{Label: l.T("admin.ref.share"), Icon: render.EmojiForward,
				URL: "https://t.me/share/url?url=" + url.QueryEscape(deepLink)}},
			{
				refreshButton(l, "adm_ref", params),
				{Label: l.T("admin.ref.delete"), Icon: render.EmojiTrash,
					Screen: "a_refresh", Params: confirm},
			},
		},
	}, nil
}

// --- adm_bcs -----------------------------------------------------------------

type adminBroadcasts struct {
	store AdminStore
	loc   *i18n.Bundle
}

func NewAdminBroadcasts(store AdminStore, loc *i18n.Bundle) ui.Screen {
	return adminBroadcasts{store: store, loc: loc}
}

func (adminBroadcasts) Name() string { return "adm_bcs" }

func broadcastStatusIcon(status string) (string, string) {
	switch status {
	case storage.BroadcastRunning:
		return render.EmojiLoading, "⏳"
	case storage.BroadcastDone:
		return render.EmojiCheck, "✅"
	case storage.BroadcastCancelled:
		return render.EmojiCross, "❌"
	default:
		return render.EmojiPencil, "✏"
	}
}

func (a adminBroadcasts) Render(ctx context.Context, s ui.Session) (ui.View, error) {
	audience, err := a.store.BroadcastAudience(ctx)
	if err != nil {
		return ui.View{}, err
	}
	recent, err := a.store.Broadcasts(ctx, 5)
	if err != nil {
		return ui.View{}, err
	}
	l := a.loc.Localizer(s.Lang)

	text := adminTitle(render.EmojiMegaphone, "📣", l.T("admin.broadcasts.title")) + "\n\n" +
		l.T("admin.broadcasts.hint") + "\n\n" +
		render.Emoji(render.EmojiPeople, "👥") + " " +
		l.T("admin.broadcasts.audience", "n", num(audience))
	if len(recent) > 0 {
		text += "\n\n" + l.T("admin.broadcasts.history")
	}

	rows := [][]ui.Button{{{Label: l.T("admin.broadcasts.new"),
		Icon: render.EmojiWrite, Screen: "adm_bc_new"}}}
	for _, bc := range recent {
		icon, _ := broadcastStatusIcon(bc.Status)
		rows = append(rows, []ui.Button{{
			Label: l.T("admin.broadcasts.entry", "id", bc.ID,
				"status", l.T("admin.broadcast.status."+bc.Status),
				"sent", num(bc.Sent), "total", num(bc.Total)),
			Icon:   icon,
			Screen: "adm_bc",
			Params: ui.Params{"bc": strconv.FormatInt(bc.ID, 10)},
		}})
	}
	return ui.View{Text: text, Rows: rows}, nil
}

// --- adm_bc ------------------------------------------------------------------

type adminBroadcast struct {
	store AdminStore
	loc   *i18n.Bundle
}

func NewAdminBroadcast(store AdminStore, loc *i18n.Bundle) ui.Screen {
	return adminBroadcast{store: store, loc: loc}
}

func (adminBroadcast) Name() string { return "adm_bc" }

func (a adminBroadcast) Render(ctx context.Context, s ui.Session) (ui.View, error) {
	id, _ := strconv.ParseInt(s.Params["bc"], 10, 64)
	bc, err := a.store.Broadcast(ctx, id)
	if err != nil {
		return ui.View{}, err
	}
	l := a.loc.Localizer(s.Lang)
	params := ui.Params{"bc": s.Params["bc"]}
	icon, fallback := broadcastStatusIcon(bc.Status)

	text := adminTitle(render.EmojiMegaphone, "📣", l.T("admin.broadcast.title", "id", bc.ID)) +
		"\n" + render.Emoji(icon, fallback) + " " + l.T("admin.broadcast.status."+bc.Status) + "\n\n"

	if bc.Status == storage.BroadcastDraft {
		audience, err := a.store.BroadcastAudience(ctx)
		if err != nil {
			return ui.View{}, err
		}
		text += l.T("admin.broadcast.draft", "n", num(audience))
		return ui.View{
			Text: text,
			Rows: [][]ui.Button{{
				{Label: l.T("admin.broadcast.send"), Icon: render.EmojiUpload,
					Screen: "adm_bc_send", Params: params},
				{Label: l.T("admin.broadcast.cancel"), Icon: render.EmojiCross,
					Screen: "adm_bc_cancel", Params: params},
			}},
		}, nil
	}

	done := bc.Sent + bc.Failed
	text += "<code>" + bar(done, bc.Total) + "</code> " + pct(done, bc.Total) +
		" · " + num(done) + "/" + num(bc.Total) + "\n\n" +
		render.Emoji(render.EmojiCheck, "✅") + " " + l.T("admin.broadcast.sent", "n", num(bc.Sent)) + "\n" +
		render.Emoji(render.EmojiCross, "❌") + " " + l.T("admin.broadcast.failed", "n", num(bc.Failed))
	if bc.StartedAt != nil {
		text += "\n\n" + render.Emoji(render.EmojiClock, "🕒") + " " +
			l.T("admin.broadcast.started", "time", bc.StartedAt.Local().Format(l.DateTimeLayout()))
	}
	if bc.FinishedAt != nil {
		text += "\n" + render.Emoji(render.EmojiClock, "🕒") + " " +
			l.T("admin.broadcast.finished", "time", bc.FinishedAt.Local().Format(l.DateTimeLayout()))
	}

	var rows [][]ui.Button
	if bc.Status == storage.BroadcastRunning {
		rows = append(rows, []ui.Button{
			refreshButton(l, "adm_bc", params),
			{Label: l.T("admin.broadcast.stop"), Icon: render.EmojiBlocked,
				Screen: "adm_bc_cancel", Params: params},
		})
	}
	return ui.View{Text: text, Rows: rows}, nil
}
