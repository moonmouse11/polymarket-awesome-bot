// Package health keeps the bot's own state in memory: when each component
// last worked or failed, how update cycles went, Telegram rate limits. The
// admin commands read it, the Monitor turns it into alerts. Everything is
// counted since the bot started.
package health

import (
	"sync"
	"time"
)

// Component names.
const (
	Updater  = "updater"
	Mongo    = "mongo"
	Telegram = "telegram"
)

// window is how far back cycle statistics go.
const window = 24 * time.Hour

// Component is the state of one dependency.
type Component struct {
	LastOK    time.Time
	LastErrAt time.Time
	LastErr   string
	Fails     int // consecutive failures; 0 after a success
	Errors    int // all failures since start
}

type cycle struct {
	at      time.Time
	markets int
}

// Registry is safe for concurrent use.
type Registry struct {
	mu      sync.Mutex
	now     func() time.Time
	started time.Time
	comps   map[string]*Component

	cycles     []cycle // last 24 h
	lastCycle  time.Time
	lastTook   time.Duration
	slowStreak int

	rateLimits    int
	lastRateLimit time.Time
	lastPenalty   time.Duration
	limitedUntil  time.Time
}

func NewRegistry() *Registry {
	return newRegistry(time.Now)
}

func newRegistry(now func() time.Time) *Registry {
	return &Registry{now: now, started: now(), comps: map[string]*Component{}}
}

// Report records a success (err == nil) or a failure of a component.
func (r *Registry) Report(name string, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	c := r.comps[name]
	if c == nil {
		c = &Component{}
		r.comps[name] = c
	}
	if err == nil {
		c.LastOK = r.now()
		c.Fails = 0
		return
	}
	c.LastErrAt = r.now()
	c.LastErr = err.Error()
	c.Fails++
	c.Errors++
}

// Cycle records one updater cycle: markets written, duration, whether it was
// slower than the interval, and its error.
func (r *Registry) Cycle(markets int, took time.Duration, slow bool, err error) {
	r.Report(Updater, err)

	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.now()
	r.cycles = append(prune(r.cycles, now), cycle{at: now, markets: markets})
	r.lastCycle, r.lastTook = now, took
	if slow {
		r.slowStreak++
	} else {
		r.slowStreak = 0
	}
}

// RateLimited records a Telegram 429 with the wait it asked for.
func (r *Registry) RateLimited(retryAfter time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.now()
	r.rateLimits++
	r.lastRateLimit, r.lastPenalty = now, retryAfter
	r.limitedUntil = now.Add(retryAfter)
}

func prune(cs []cycle, now time.Time) []cycle {
	i := 0
	for i < len(cs) && now.Sub(cs[i].at) > window {
		i++
	}
	return cs[i:]
}

// Snapshot is a consistent copy of the registry.
type Snapshot struct {
	Now        time.Time
	Started    time.Time
	Components map[string]Component

	Cycles24h     int
	Markets24h    int // markets written by cycles in the last 24 h (with repeats)
	LastCycle     time.Time
	LastCycleTook time.Duration
	SlowStreak    int

	RateLimits    int
	LastRateLimit time.Time
	LastPenalty   time.Duration
	LimitedUntil  time.Time
}

func (r *Registry) Snapshot() Snapshot {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.now()
	r.cycles = prune(r.cycles, now)
	s := Snapshot{
		Now:           now,
		Started:       r.started,
		Components:    make(map[string]Component, len(r.comps)),
		Cycles24h:     len(r.cycles),
		LastCycle:     r.lastCycle,
		LastCycleTook: r.lastTook,
		SlowStreak:    r.slowStreak,
		RateLimits:    r.rateLimits,
		LastRateLimit: r.lastRateLimit,
		LastPenalty:   r.lastPenalty,
		LimitedUntil:  r.limitedUntil,
	}
	for name, c := range r.comps {
		s.Components[name] = *c
	}
	for _, c := range r.cycles {
		s.Markets24h += c.markets
	}
	return s
}
