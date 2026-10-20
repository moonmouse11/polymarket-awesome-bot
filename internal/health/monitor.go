package health

import (
	"context"
	"fmt"
	"html"
	"sort"
	"sync"
	"time"

	"github.com/rs/zerolog"
)

const (
	checkEvery = time.Minute
	// remindEvery repeats an alert while the problem lasts.
	remindEvery = 6 * time.Hour

	failedCycles   = 3               // updater: failed cycles in a row
	slowCycles     = 3               // updater: cycles slower than the interval in a row
	failedPings    = 2               // MongoDB: failed pings in a row (one a minute)
	failedTelegram = 3               // Telegram: failed calls in a row
	longPenalty    = 5 * time.Minute // Telegram 429 longer than this
	queueHigh      = 500             // notifications waiting: alert above...
	queueLow       = 100             // ...resolved below
)

// Pinger checks a dependency (MongoDB, Telegram).
type Pinger interface {
	Ping(ctx context.Context) error
}

// Queue reports the notification queue sizes (implemented by *db.MongoDB).
type Queue interface {
	QueueSizes(ctx context.Context) (sends, edits int64, err error)
}

// Alerter delivers an alert to the admins.
type Alerter interface {
	Alert(ctx context.Context, html string) error
}

// state is the input of the alert rules.
type state struct {
	Snapshot
	queue int64 // notifications waiting to be sent; -1 when unknown
}

type rule struct {
	title string
	// silent rules cannot alert while they fire (Telegram down): only the
	// recovery message is sent, with the outage period.
	silent   bool
	firing   func(state) (bool, string) // and a detail for the alert
	resolved func(state) bool

	active      bool
	since       time.Time
	detail      string
	lastSent    time.Time
	recovered   bool // a recovery message is waiting to be sent
	recoveredAt time.Time
}

// Monitor checks the bot's health every minute and alerts the admins when a
// problem starts (and every 6 h while it lasts) and when it is resolved.
type Monitor struct {
	reg     *Registry
	mongo   Pinger
	tg      Pinger
	queue   Queue
	alerter Alerter
	log     zerolog.Logger

	mu    sync.Mutex
	rules []*rule
}

func NewMonitor(reg *Registry, mongo, tg Pinger, queue Queue, alerter Alerter, log zerolog.Logger) *Monitor {
	return &Monitor{
		reg: reg, mongo: mongo, tg: tg, queue: queue, alerter: alerter,
		log:   log.With().Str("component", "monitor").Logger(),
		rules: defaultRules(),
	}
}

func defaultRules() []*rule {
	comp := func(s state, name string) Component { return s.Components[name] }
	return []*rule{
		{
			title: "Market updates are failing",
			firing: func(s state) (bool, string) {
				c := comp(s, Updater)
				return c.Fails >= failedCycles, fmt.Sprintf("%d cycles in a row; last error: %s", c.Fails, c.LastErr)
			},
			resolved: func(s state) bool { return comp(s, Updater).Fails == 0 },
		},
		{
			title: "Market updates are lagging",
			firing: func(s state) (bool, string) {
				return s.SlowStreak >= slowCycles, fmt.Sprintf("%d cycles in a row took longer than the interval; last took %s", s.SlowStreak, s.LastCycleTook.Round(time.Second))
			},
			resolved: func(s state) bool { return s.SlowStreak == 0 },
		},
		{
			title: "MongoDB is unreachable",
			firing: func(s state) (bool, string) {
				c := comp(s, Mongo)
				return c.Fails >= failedPings, c.LastErr
			},
			resolved: func(s state) bool { return comp(s, Mongo).Fails == 0 },
		},
		{
			title: "Telegram rate limit",
			firing: func(s state) (bool, string) {
				return s.LimitedUntil.After(s.Now) && s.LastPenalty > longPenalty,
					fmt.Sprintf("429, penalty %s until %s", s.LastPenalty.Round(time.Second), s.LimitedUntil.UTC().Format("15:04 UTC"))
			},
			resolved: func(s state) bool { return !s.LimitedUntil.After(s.Now) },
		},
		{
			title: "Notification queue is growing",
			firing: func(s state) (bool, string) {
				return s.queue > queueHigh, fmt.Sprintf("%d markets waiting to be sent", s.queue)
			},
			resolved: func(s state) bool { return s.queue >= 0 && s.queue < queueLow },
		},
		{
			title:  "Telegram was unreachable",
			silent: true,
			firing: func(s state) (bool, string) {
				c := comp(s, Telegram)
				return c.Fails >= failedTelegram, c.LastErr
			},
			resolved: func(s state) bool { return comp(s, Telegram).Fails == 0 },
		},
	}
}

// Run checks every minute until ctx is cancelled.
func (m *Monitor) Run(ctx context.Context) error {
	m.log.Info().Msg("Health monitor started")
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(checkEvery):
		}
		m.check(ctx)
	}
}

// check probes the dependencies, then evaluates the rules.
func (m *Monitor) check(ctx context.Context) {
	probe := func(name string, p Pinger) {
		pctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		err := p.Ping(pctx)
		if ctx.Err() == nil {
			m.reg.Report(name, err)
		}
	}
	probe(Mongo, m.mongo)
	probe(Telegram, m.tg)

	queue := int64(-1)
	if sends, _, err := m.queue.QueueSizes(ctx); err == nil {
		queue = sends
	}
	if ctx.Err() != nil {
		return
	}
	m.evaluate(ctx, state{Snapshot: m.reg.Snapshot(), queue: queue})
}

func (m *Monitor) evaluate(ctx context.Context, s state) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, r := range m.rules {
		firing, detail := r.firing(s)
		switch {
		case !r.active && firing:
			r.active, r.since, r.detail, r.lastSent, r.recovered = true, s.Now, detail, time.Time{}, false
		case r.active && r.resolved(s):
			r.active, r.recovered, r.recoveredAt = false, true, s.Now
		case r.active:
			r.detail = detail
		}

		if r.active && !r.silent && (r.lastSent.IsZero() || s.Now.Sub(r.lastSent) >= remindEvery) {
			m.sendFiring(ctx, r, s.Now)
		}
		if r.recovered {
			m.sendResolved(ctx, r)
		}
	}
}

// sendFiring sends the first alert or a reminder for an active rule.
func (m *Monitor) sendFiring(ctx context.Context, r *rule, now time.Time) {
	prefix := "🔴"
	if !r.lastSent.IsZero() {
		prefix = "🔴 Still:"
	}
	msg := fmt.Sprintf("%s <b>%s</b>\nsince %s (%s)\n%s", prefix, html.EscapeString(r.title),
		clock(r.since), Ago(now.Sub(r.since)), html.EscapeString(r.detail))
	if m.send(ctx, r.title, msg) {
		r.lastSent = now
	}
}

// sendResolved reports a recovered rule; silent rules get the whole outage in one message.
func (m *Monitor) sendResolved(ctx context.Context, r *rule) {
	msg := fmt.Sprintf("🟢 <b>Resolved: %s</b>\nlasted %s", html.EscapeString(r.title), Ago(r.recoveredAt.Sub(r.since)))
	if r.silent {
		msg = fmt.Sprintf("🟢 <b>%s</b>\nfrom %s to %s (%s)\nlast error: %s", html.EscapeString(r.title),
			clock(r.since), clock(r.recoveredAt), Ago(r.recoveredAt.Sub(r.since)), html.EscapeString(r.detail))
	}
	if m.send(ctx, r.title, msg) {
		r.recovered = false
	}
}

// send delivers an alert; on failure it is retried on the next check.
func (m *Monitor) send(ctx context.Context, title, msg string) bool {
	if err := m.alerter.Alert(ctx, msg); err != nil {
		m.log.Warn().Err(err).Str("alert", title).Msg("Sending an alert failed; will retry")
		return false
	}
	m.log.Info().Str("alert", title).Msg("Alert sent")
	return true
}

// Active lists the problems that are happening now, for /health_check.
func (m *Monitor) Active() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []string
	for _, r := range m.rules {
		if r.active {
			out = append(out, fmt.Sprintf("%s since %s: %s", r.title, clock(r.since), r.detail))
		}
	}
	sort.Strings(out)
	return out
}

func clock(t time.Time) string { return t.UTC().Format("Jan 2 15:04 UTC") }

// Ago formats a duration for people: "45 s", "12 min", "3 h 5 min", "2 d 4 h".
func Ago(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%d s", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%d min", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%d h %d min", int(d.Hours()), int(d.Minutes())%60)
	default:
		return fmt.Sprintf("%d d %d h", int(d.Hours())/24, int(d.Hours())%24)
	}
}
