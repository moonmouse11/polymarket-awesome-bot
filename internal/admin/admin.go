// Package admin answers admin commands sent to the bot in a private chat
// (/status, /health_check) and delivers health alerts to the admins.
// Messages from anyone else are ignored.
package admin

import (
	"context"
	"errors"
	"fmt"
	"html"
	"strconv"
	"strings"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"github.com/rs/zerolog"

	"github.com/moonmouse11/polymarket-awesome-bot/internal/db"
	"github.com/moonmouse11/polymarket-awesome-bot/internal/health"
)

// Store is MongoDB (implemented by *db.MongoDB).
type Store interface {
	Stats(ctx context.Context, since time.Time) (db.Stats, error)
	Ping(ctx context.Context) error
}

// AlertLister lists the problems happening now (implemented by *health.Monitor).
type AlertLister interface {
	Active() []string
}

type noAlerts struct{}

func (noAlerts) Active() []string { return nil }

const help = `<b>Admin commands</b>
/status — queues, awesome markets, last 24 h
/health_check — MongoDB, updater, Telegram, active alerts`

// ParseAdminIDs reads TELEGRAM_ADMIN_IDS: Telegram user ids separated by commas.
func ParseAdminIDs(s string) ([]int64, error) {
	var ids []int64
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		id, err := strconv.ParseInt(part, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid TELEGRAM_ADMIN_IDS %q: %w", s, err)
		}
		ids = append(ids, id)
	}
	return ids, nil
}

// Commands handles messages to the bot.
type Commands struct {
	admins map[int64]bool
	store  Store
	health *health.Registry
	alerts AlertLister
	log    zerolog.Logger
	now    func() time.Time
}

func New(admins []int64, store Store, reg *health.Registry, log zerolog.Logger) *Commands {
	c := &Commands{
		admins: map[int64]bool{},
		store:  store,
		health: reg,
		alerts: noAlerts{},
		log:    log.With().Str("component", "admin").Logger(),
		now:    time.Now,
	}
	for _, id := range admins {
		c.admins[id] = true
	}
	return c
}

// WithAlerts adds the active alerts to /health_check. Call before Listen.
func (c *Commands) WithAlerts(a AlertLister) *Commands {
	c.alerts = a
	return c
}

// Handle is a bot.HandlerFunc for every incoming update.
func (c *Commands) Handle(ctx context.Context, b *bot.Bot, upd *models.Update) {
	msg := upd.Message
	if msg == nil || msg.From == nil || msg.Chat.Type != models.ChatTypePrivate {
		return
	}
	cmd := command(msg.Text)
	if cmd == "" {
		return
	}
	if !c.admins[msg.From.ID] {
		// Silence for strangers. /start is logged so the owner can find
		// their id for TELEGRAM_ADMIN_IDS.
		if cmd == "/start" {
			c.log.Info().Int64("user_id", msg.From.ID).Str("username", msg.From.Username).
				Msg("Telegram /start from a non-admin; add user_id to TELEGRAM_ADMIN_IDS to allow admin commands")
		}
		return
	}

	_, err := b.SendMessage(ctx, &bot.SendMessageParams{
		ChatID:    msg.Chat.ID,
		Text:      c.Answer(ctx, cmd),
		ParseMode: models.ParseModeHTML,
	})
	if err != nil {
		c.log.Warn().Err(err).Str("command", cmd).Msg("Replying to an admin command failed")
	}
}

// command returns "/status" for "/status", "/status@bot_name" or "/status x".
func command(text string) string {
	if !strings.HasPrefix(text, "/") {
		return ""
	}
	cmd := strings.Fields(text)[0]
	if i := strings.Index(cmd, "@"); i >= 0 {
		cmd = cmd[:i]
	}
	return strings.ToLower(cmd)
}

// Answer builds the reply to an admin command.
func (c *Commands) Answer(ctx context.Context, cmd string) string {
	switch cmd {
	case "/status":
		return c.status(ctx)
	case "/health_check":
		return c.healthCheck(ctx)
	default: // /start, /help, anything else
		return help
	}
}

func (c *Commands) status(ctx context.Context) string {
	now := c.now()
	h := c.health.Snapshot()
	var b strings.Builder
	b.WriteString("📊 <b>Status</b>\n\n")

	st, err := c.store.Stats(ctx, now.Add(-24*time.Hour))
	if err != nil {
		fmt.Fprintf(&b, "🔴 MongoDB: %s\n\n", short(err))
	} else {
		fmt.Fprintf(&b, "<b>Queue</b>\nTo delete: %s · To send: %s · To edit: %s\n\n",
			num(st.PendingDeletes), num(st.PendingSends), num(st.PendingEdits))
		fmt.Fprintf(&b, "<b>Awesome markets</b>\nTotal: %s · Open: %s\n\n", num(st.Awesome), num(st.AwesomeOpen))
		fmt.Fprintf(&b, "<b>Last 24 h</b>\nMarkets updated: %s\nNew markets: %s\nBecame awesome: %s · Announced: %s\n",
			num(st.Updated), num(st.Created), num(st.BecameAwesome), num(st.Announced))
	}

	since := ""
	if up := now.Sub(h.Started); up < 24*time.Hour {
		since = fmt.Sprintf(" (since start, %s ago)", health.Ago(up))
	}
	fmt.Fprintf(&b, "Updates from API: %s in %d cycles%s\n\n", num(int64(h.Markets24h)), h.Cycles24h, since)

	b.WriteString("<b>Updater</b>\n")
	if h.LastCycle.IsZero() {
		b.WriteString("No cycle finished yet")
	} else {
		fmt.Fprintf(&b, "Last cycle: %s ago, took %s", health.Ago(now.Sub(h.LastCycle)), health.Ago(h.LastCycleTook))
	}
	return b.String()
}

func (c *Commands) healthCheck(ctx context.Context) string {
	now := c.now()
	var b strings.Builder

	// MongoDB is checked live; the rest is what the components reported.
	start := time.Now()
	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	pingErr := c.store.Ping(pingCtx)
	cancel()
	c.health.Report(health.Mongo, pingErr)
	h := c.health.Snapshot()

	fmt.Fprintf(&b, "🩺 <b>Health</b> · up %s\n\n", health.Ago(now.Sub(h.Started)))
	if pingErr != nil {
		fmt.Fprintf(&b, "🔴 MongoDB · %s\n", short(pingErr))
	} else {
		fmt.Fprintf(&b, "🟢 MongoDB · ping %d ms\n", time.Since(start).Milliseconds())
	}
	b.WriteString(component("Updater", h.Components[health.Updater], now))
	b.WriteString(component("Telegram", h.Components[health.Telegram], now))
	if h.RateLimits > 0 {
		fmt.Fprintf(&b, "   429: %d since start · last %s, penalty %s\n",
			h.RateLimits, h.LastRateLimit.UTC().Format("Jan 2 15:04 UTC"), health.Ago(h.LastPenalty))
	}

	b.WriteString("\n<b>Active alerts</b>\n")
	active := c.alerts.Active()
	if len(active) == 0 {
		b.WriteString("none")
	}
	for _, a := range active {
		b.WriteString("🔴 " + html.EscapeString(a) + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

func component(name string, c health.Component, now time.Time) string {
	var b strings.Builder
	switch {
	case c.Fails > 0:
		fmt.Fprintf(&b, "🔴 %s · failing (%d in a row)\n", name, c.Fails)
	case c.LastOK.IsZero():
		fmt.Fprintf(&b, "⚪ %s · no data yet\n", name)
	default:
		fmt.Fprintf(&b, "🟢 %s · last OK %s ago\n", name, health.Ago(now.Sub(c.LastOK)))
	}
	if c.Errors > 0 {
		fmt.Fprintf(&b, "   errors since start: %d · last %s: %s\n",
			c.Errors, c.LastErrAt.UTC().Format("Jan 2 15:04 UTC"), html.EscapeString(truncate(c.LastErr, 200)))
	}
	return b.String()
}

func short(err error) string { return html.EscapeString(truncate(err.Error(), 200)) }

func truncate(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}

// num formats 1204330 as "1 204 330".
func num(n int64) string {
	s := strconv.FormatInt(n, 10)
	var b strings.Builder
	for i, r := range s {
		if i > 0 && (len(s)-i)%3 == 0 && s[i-1] != '-' {
			b.WriteRune(' ')
		}
		b.WriteRune(r)
	}
	return b.String()
}

// Sender posts to a private chat (implemented by *notifier.Telegram).
type Sender interface {
	SendTo(ctx context.Context, chatID int64, html string) error
}

// Alerter sends health alerts to every admin.
type Alerter struct {
	sender Sender
	admins []int64
}

func NewAlerter(sender Sender, admins []int64) *Alerter {
	return &Alerter{sender: sender, admins: admins}
}

// Alert fails if the alert reached no admin, so the monitor retries it.
// An admin who never opened a chat with the bot cannot get messages.
func (a *Alerter) Alert(ctx context.Context, msg string) error {
	var errs []error
	for _, id := range a.admins {
		if err := a.sender.SendTo(ctx, id, msg); err != nil {
			errs = append(errs, fmt.Errorf("admin %d: %w", id, err))
		}
	}
	if len(a.admins) > 0 && len(errs) == len(a.admins) {
		return errors.Join(errs...)
	}
	return nil
}
