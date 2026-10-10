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
	"unicode"

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
	PendingEdits(ctx context.Context, limit int64) ([]db.AwesomeMarket, error)
	MarkEdited(ctx context.Context, id string) error
}

// Sender posts and edits messages (implemented by *Telegram).
type Sender interface {
	Send(ctx context.Context, html string) (int, error)
	Edit(ctx context.Context, messageID int, html string) error
}

// Notifier sends queued markets one by one and edits posted messages.
type Notifier struct {
	queue     Queue
	sender    Sender
	log       zerolog.Logger
	sendEvery time.Duration
	idleWait  time.Duration
	lastCall  time.Time // last Telegram call, for pacing
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

// Run sends notifications until ctx is cancelled. New markets go first;
// edits are made only when nothing waits to be sent. Failures are logged and
// retried later; they never stop the bot.
func (n *Notifier) Run(ctx context.Context) error {
	n.log.Info().Msg("Notifier started")
	for {
		wait := n.idleWait
		done, err := n.sendBatch(ctx)
		if err == nil && done == 0 {
			done, err = n.editBatch(ctx)
		}
		switch {
		case ctx.Err() != nil:
		case err != nil:
			n.log.Warn().Err(err).Msg("Telegram call failed; will retry")
			if d := retryAfter(err); d > 0 {
				wait = d
			}
		case done > 0:
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

// pace waits so that Telegram calls are at least sendEvery apart, across
// batches too: Telegram limits sends and edits together.
func (n *Notifier) pace(ctx context.Context) error {
	if wait := time.Until(n.lastCall.Add(n.sendEvery)); wait > 0 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(wait):
		}
	}
	n.lastCall = time.Now()
	return nil
}

// sendBatch sends up to batchSize queued markets, oldest first. It stops at
// the first send error so the order is kept and nothing is skipped.
func (n *Notifier) sendBatch(ctx context.Context) (int, error) {
	markets, err := n.queue.PendingNotifications(ctx, batchSize)
	if err != nil {
		return 0, err
	}
	sent := 0
	for _, mk := range markets {
		if err := n.pace(ctx); err != nil {
			return sent, err
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

// editBatch re-renders up to batchSize posted messages from the current
// market data and edits them in the channel.
func (n *Notifier) editBatch(ctx context.Context) (int, error) {
	markets, err := n.queue.PendingEdits(ctx, batchSize)
	if err != nil {
		return 0, err
	}
	edited := 0
	for _, mk := range markets {
		if err := n.pace(ctx); err != nil {
			return edited, err
		}
		if err := n.sender.Edit(ctx, mk.MessageID, Format(mk)); err != nil {
			if !isBadMessage(err) {
				return edited, fmt.Errorf("edit message %d (market %s): %w", mk.MessageID, mk.ID, err)
			}
			// E.g. the message was deleted from the channel.
			n.log.Error().Err(err).Str("market", mk.ID).Int("message", mk.MessageID).Msg("Telegram rejected the edit; skipping market")
		}
		if err := n.queue.MarkEdited(ctx, mk.ID); err != nil {
			return edited, err
		}
		edited++
		n.log.Debug().Str("market", mk.ID).Int("message", mk.MessageID).Msg("Message edited")
	}
	if edited > 0 {
		n.log.Info().Int("edited", edited).Msg("Channel messages edited")
	}
	return edited, nil
}

// Format builds the HTML message for a market. Sections are separated by an
// empty line; the link is a text link, so no URL is shown.
func Format(mk db.AwesomeMarket) string {
	sections := []string{"<b>" + html.EscapeString(mk.Question) + "</b>"}

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

	sections = append(sections, reason(mk))
	if tags := hashtags(mk.Tags); tags != "" {
		sections = append(sections, tags)
	}
	sections = append(sections, fmt.Sprintf(`<a href="%s">Open on Polymarket</a>`, html.EscapeString(marketURL(mk))))
	return strings.Join(sections, "\n\n")
}

// reason says why the market passed. The rule is negative (awesome unless
// filtered out), so for most markets the honest answer is "not filtered".
func reason(mk db.AwesomeMarket) string {
	if mk.Reason == db.ReasonExcludedTag {
		// Edits re-render messages: the market may have been filtered out
		// since it was posted (e.g. a tag was added to excluded_tags).
		return fmt.Sprintf("⛔ Not awesome anymore\n(excluded tag «%s»)", html.EscapeString(mk.ExcludedBy))
	}
	if mk.Reason == db.ReasonWords && len(mk.Words) > 0 {
		return fmt.Sprintf("🔑 Passed: keyword %s\n(overrides excluded tag «%s»)",
			quoteAll(mk.Words), html.EscapeString(mk.ExcludedBy))
	}
	r := "✅ Passed: not filtered\n(none of the excluded tags)"
	if len(mk.Words) > 0 {
		r += "\nKeywords found: " + quoteAll(mk.Words)
	}
	return r
}

// hashtags turns tags into Telegram hashtags ("Middle East" → #MiddleEast),
// which are clickable and searchable in the channel. A hashtag keeps only
// letters, digits and "_"; one without a letter is not a hashtag in
// Telegram and is dropped.
func hashtags(tags []string) string {
	seen := map[string]bool{}
	var out []string
	for _, tag := range tags {
		var b strings.Builder
		hasLetter := false
		for _, r := range tag {
			switch {
			case unicode.IsLetter(r):
				hasLetter = true
				b.WriteRune(r)
			case unicode.IsDigit(r) || r == '_':
				b.WriteRune(r)
			}
		}
		h := b.String()
		if !hasLetter || seen[strings.ToLower(h)] {
			continue
		}
		seen[strings.ToLower(h)] = true
		out = append(out, "#"+h)
	}
	return strings.Join(out, " ")
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
