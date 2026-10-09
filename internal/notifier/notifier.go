// Package notifier announces markets that became awesome. The queue lives in
// MongoDB (markets with notify_pending, filled by the awesome recompute), so a
// Telegram outage or a restart loses nothing: unsent markets stay queued.
package notifier

import (
	"context"
	"fmt"
	"html"
	"strings"
	"time"

	"github.com/rs/zerolog"

	"github.com/moonmouse11/polymarket-awesome-bot/internal/db"
)

const (
	// sendEvery keeps us under Telegram's limit of ~20 messages a minute
	// per channel. A burst (e.g. after adding a keyword) waits in the queue.
	sendEvery = 3 * time.Second
	// idleWait is how often an empty queue is checked. Markets are marked
	// every POLL_INTERVAL, so checking more often gains nothing.
	idleWait = 30 * time.Second
	// batchSize markets are read from the queue at a time.
	batchSize = 20
)

// Queue is MongoDB (implemented by *db.MongoDB).
type Queue interface {
	PendingNotifications(ctx context.Context, limit int64) ([]db.AwesomeMarket, error)
	MarkNotified(ctx context.Context, id string, at time.Time, messageID int) error
}

// Sender posts a message and returns its id (implemented by *Telegram).
type Sender interface {
	Send(ctx context.Context, html string) (int, error)
}

// Notifier sends queued markets one by one.
type Notifier struct {
	queue     Queue
	sender    Sender
	log       zerolog.Logger
	sendEvery time.Duration
	idleWait  time.Duration
}

func New(queue Queue, sender Sender, log zerolog.Logger) *Notifier {
	return &Notifier{
		queue:     queue,
		sender:    sender,
		log:       log.With().Str("component", "notifier").Logger(),
		sendEvery: sendEvery,
		idleWait:  idleWait,
	}
}

// Run sends notifications until ctx is cancelled. Failures are logged and
// retried later; they never stop the bot.
func (n *Notifier) Run(ctx context.Context) error {
	n.log.Info().Msg("Notifier started")
	for {
		wait := n.idleWait
		sent, err := n.sendBatch(ctx)
		switch {
		case ctx.Err() != nil:
		case err != nil:
			n.log.Warn().Err(err).Msg("Sending notifications failed; will retry")
			if d := retryAfter(err); d > 0 {
				wait = d
			}
		case sent > 0:
			wait = 0 // the queue may have more
		}

		select {
		case <-ctx.Done():
			n.log.Info().Msg("Notifier stopped")
			return nil
		case <-time.After(wait):
		}
	}
}

// sendBatch sends up to batchSize queued markets, oldest first. It stops at
// the first send error so the order is kept and nothing is skipped.
func (n *Notifier) sendBatch(ctx context.Context) (int, error) {
	markets, err := n.queue.PendingNotifications(ctx, batchSize)
	if err != nil {
		return 0, err
	}
	sent := 0
	for i, mk := range markets {
		if i > 0 {
			select {
			case <-ctx.Done():
				return sent, ctx.Err()
			case <-time.After(n.sendEvery):
			}
		}

		msgID, err := n.sender.Send(ctx, Format(mk))
		if err != nil {
			if !isBadMessage(err) {
				return sent, fmt.Errorf("send market %s: %w", mk.ID, err)
			}
			// Telegram rejects this message for good: skip it instead of
			// blocking the queue forever.
			n.log.Error().Err(err).Str("market", mk.ID).Msg("Telegram rejected the notification; skipping market")
		}
		if err := n.queue.MarkNotified(ctx, mk.ID, time.Now(), msgID); err != nil {
			return sent, err
		}
		sent++
		n.log.Info().Str("market", mk.ID).Str("question", mk.Question).Msg("Awesome market announced")
	}
	return sent, nil
}

// Format builds the HTML message for a market. Sections are separated by an
// empty line; the link is a text link, so no URL is shown.
func Format(mk db.AwesomeMarket) string {
	sections := []string{"🛸 <b>" + html.EscapeString(mk.Question) + "</b>"}

	var odds []string
	for i, outcome := range mk.Outcomes {
		if i >= 2 || i >= len(mk.OutcomePrices) {
			break
		}
		odds = append(odds, fmt.Sprintf("%s %.0f%%", html.EscapeString(outcome), mk.OutcomePrices[i]*100))
	}
	var stats []string
	if len(odds) > 0 {
		stats = append(stats, strings.Join(odds, " · "))
	}
	var volEnd []string
	if mk.Volume > 0 {
		volEnd = append(volEnd, "Volume "+money(mk.Volume))
	}
	if mk.EndDate != nil {
		volEnd = append(volEnd, "Ends "+mk.EndDate.UTC().Format("Jan 2, 2006"))
	}
	if len(volEnd) > 0 {
		stats = append(stats, strings.Join(volEnd, " · "))
	}
	if len(stats) > 0 {
		sections = append(sections, strings.Join(stats, "\n"))
	}

	sections = append(sections, rule(mk))
	sections = append(sections, fmt.Sprintf(`<a href="%s">Open on Polymarket</a>`, html.EscapeString(marketURL(mk))))
	return strings.Join(sections, "\n\n")
}

// rule explains which rule made the market awesome.
func rule(mk db.AwesomeMarket) string {
	tags := "Tags: none"
	if len(mk.Tags) > 0 {
		tags = "Tags: " + html.EscapeString(strings.Join(mk.Tags, ", "))
	}
	if mk.Reason == db.ReasonWords && len(mk.Words) > 0 {
		return fmt.Sprintf("Rule: keyword %s overrides excluded tag «%s»\n%s",
			quoteAll(mk.Words), html.EscapeString(mk.ExcludedBy), tags)
	}
	return "Rule: no excluded tags\n" + tags
}

func quoteAll(words []string) string {
	q := make([]string, len(words))
	for i, w := range words {
		q[i] = "«" + html.EscapeString(w) + "»"
	}
	return strings.Join(q, ", ")
}

// marketURL points at the market itself: an event can hold several markets
// (e.g. "by 2027" and "by 2028"), and the event page opens the first one.
func marketURL(mk db.AwesomeMarket) string {
	if mk.EventSlug != "" && mk.Slug != "" {
		return "https://polymarket.com/event/" + mk.EventSlug + "/" + mk.Slug
	}
	if mk.EventSlug != "" {
		return "https://polymarket.com/event/" + mk.EventSlug
	}
	return "https://polymarket.com/market/" + mk.Slug
}

// money formats dollars short: $950, $48k, $1.2M.
func money(v float64) string {
	switch {
	case v >= 1e6:
		return fmt.Sprintf("$%.1fM", v/1e6)
	case v >= 1e3:
		return fmt.Sprintf("$%.0fk", v/1e3)
	default:
		return fmt.Sprintf("$%.0f", v)
	}
}
