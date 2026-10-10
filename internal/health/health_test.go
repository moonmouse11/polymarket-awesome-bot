package health

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"
)

var t0 = time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)

// fakeClock is a settable time source.
type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time      { return c.t }
func (c *fakeClock) add(d time.Duration) { c.t = c.t.Add(d) }

func newClock() (*fakeClock, func() time.Time) {
	c := &fakeClock{t: t0}
	return c, c.now
}

func TestRegistry_ComponentsAndCycles(t *testing.T) {
	c, now := newClock()
	r := newRegistry(now)

	r.Report(Mongo, errors.New("timeout"))
	r.Report(Mongo, errors.New("timeout"))
	if got := r.Snapshot().Components[Mongo]; got.Fails != 2 || got.Errors != 2 || got.LastErr != "timeout" {
		t.Errorf("after 2 failures: %+v", got)
	}
	r.Report(Mongo, nil)
	if got := r.Snapshot().Components[Mongo]; got.Fails != 0 || got.Errors != 2 || !got.LastOK.Equal(t0) {
		t.Errorf("after success: %+v", got)
	}

	r.Cycle(100, time.Minute, false, nil)
	c.add(23 * time.Hour)
	r.Cycle(50, 6*time.Minute, true, nil)
	r.Cycle(0, 7*time.Minute, true, errors.New("API down"))
	s := r.Snapshot()
	if s.Cycles24h != 3 || s.Markets24h != 150 || s.SlowStreak != 2 || s.Components[Updater].Fails != 1 {
		t.Errorf("snapshot = %+v", s)
	}

	c.add(2 * time.Hour) // the first cycle leaves the 24 h window
	if s := r.Snapshot(); s.Cycles24h != 2 || s.Markets24h != 50 {
		t.Errorf("after 25 h: cycles=%d markets=%d, want 2 and 50", s.Cycles24h, s.Markets24h)
	}
}

type fakeAlerter struct {
	sent []string
	fail bool
}

func (f *fakeAlerter) Alert(_ context.Context, msg string) error {
	if f.fail {
		return errors.New("telegram down")
	}
	f.sent = append(f.sent, msg)
	return nil
}

func newTestMonitor(a Alerter) *Monitor {
	return NewMonitor(newRegistry(time.Now), nil, nil, nil, a, zerolog.Nop())
}

// st builds the rules input at time now, changed by mut.
func st(now time.Time, mut func(*state)) state {
	s := state{Snapshot: Snapshot{Now: now, Components: map[string]Component{}}}
	mut(&s)
	return s
}

func TestMonitor_AlertsOnceRemindsAndResolves(t *testing.T) {
	a := &fakeAlerter{}
	m := newTestMonitor(a)
	ctx := context.Background()
	failing := func(n int) func(*state) {
		return func(s *state) { s.Components[Updater] = Component{Fails: n, LastErr: "API down"} }
	}

	m.evaluate(ctx, st(t0, failing(2))) // below the threshold
	m.evaluate(ctx, st(t0.Add(5*time.Minute), failing(3)))
	m.evaluate(ctx, st(t0.Add(10*time.Minute), failing(4))) // no repeat
	if len(a.sent) != 1 || !strings.Contains(a.sent[0], "🔴 <b>Market updates are failing</b>") || !strings.Contains(a.sent[0], "API down") {
		t.Fatalf("alerts = %q, want one 🔴", a.sent)
	}
	if active := m.Active(); len(active) != 1 {
		t.Errorf("Active = %v, want one problem", active)
	}

	m.evaluate(ctx, st(t0.Add(6*time.Hour+5*time.Minute), failing(70)))
	if len(a.sent) != 2 || !strings.Contains(a.sent[1], "🔴 Still:") {
		t.Fatalf("alerts = %q, want a reminder after 6 h", a.sent)
	}

	m.evaluate(ctx, st(t0.Add(7*time.Hour+5*time.Minute), failing(0)))
	if len(a.sent) != 3 || !strings.Contains(a.sent[2], "🟢 <b>Resolved: Market updates are failing</b>\nlasted 7 h 0 min") {
		t.Fatalf("alerts = %q, want 🟢 lasting 7 h", a.sent)
	}
	if active := m.Active(); len(active) != 0 {
		t.Errorf("Active = %v, want none", active)
	}
}

func TestMonitor_TelegramOutageReportedAfterRecovery(t *testing.T) {
	a := &fakeAlerter{fail: true} // Telegram is down: alerts cannot be sent
	m := newTestMonitor(a)
	ctx := context.Background()
	down := func(s *state) { s.Components[Telegram] = Component{Fails: 5, LastErr: "dial tcp: i/o timeout"} }

	m.evaluate(ctx, st(t0, down))
	m.evaluate(ctx, st(t0.Add(time.Hour), down))
	a.fail = false
	m.evaluate(ctx, st(t0.Add(2*time.Hour), func(s *state) { s.Components[Telegram] = Component{} }))

	if len(a.sent) != 1 || !strings.Contains(a.sent[0], "🟢 <b>Telegram was unreachable</b>\nfrom Oct 10 12:00 UTC to Oct 10 14:00 UTC (2 h 0 min)") {
		t.Fatalf("alerts = %q, want only the recovery message with the outage period", a.sent)
	}
}

func TestMonitor_FailedAlertIsRetried(t *testing.T) {
	a := &fakeAlerter{fail: true}
	m := newTestMonitor(a)
	ctx := context.Background()
	big := func(s *state) { s.queue = 600 }

	m.evaluate(ctx, st(t0, big))
	a.fail = false
	m.evaluate(ctx, st(t0.Add(time.Minute), big))
	if len(a.sent) != 1 || !strings.Contains(a.sent[0], "Notification queue is growing") {
		t.Fatalf("alerts = %q, want the queue alert on retry", a.sent)
	}

	// Hysteresis: 300 is below the alert level but not low enough to resolve.
	m.evaluate(ctx, st(t0.Add(2*time.Minute), func(s *state) { s.queue = 300 }))
	m.evaluate(ctx, st(t0.Add(3*time.Minute), func(s *state) { s.queue = -1 })) // unknown: no change
	if len(a.sent) != 1 {
		t.Fatalf("alerts = %q, want no change between 100 and 500 or when unknown", a.sent)
	}
	m.evaluate(ctx, st(t0.Add(4*time.Minute), func(s *state) { s.queue = 50 }))
	if len(a.sent) != 2 || !strings.Contains(a.sent[1], "🟢") {
		t.Fatalf("alerts = %q, want 🟢 below 100", a.sent)
	}
}

func TestAgo(t *testing.T) {
	for d, want := range map[time.Duration]string{
		45 * time.Second:            "45 s",
		12 * time.Minute:            "12 min",
		3*time.Hour + 5*time.Minute: "3 h 5 min",
		52 * time.Hour:              "2 d 4 h",
	} {
		if got := Ago(d); got != want {
			t.Errorf("Ago(%v) = %q, want %q", d, got, want)
		}
	}
}
