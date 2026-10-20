// Command restore-posts rebuilds which markets were already posted to the
// channel after the database was lost. The Bot API cannot read channel
// history, so it reads a Telegram Desktop export of the channel (JSON) and
// matches each message to a market by the slug in its Polymarket link.
// Run it after make markets-load and before starting the bot:
// make notify-restore file=result.json.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/moonmouse11/polymarket-awesome-bot/internal/db"
	"github.com/moonmouse11/polymarket-awesome-bot/internal/logger"
	"github.com/rs/zerolog"
)

func main() {
	log, err := logger.New(os.Getenv("LOG_LEVEL"), os.Getenv("LOG_FORMAT"), os.Stdout)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: restore-posts <result.json from Telegram Desktop export>")
		os.Exit(2)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, log, os.Args[1]); err != nil {
		log.Error().Err(err).Msg("Restoring posts failed")
		os.Exit(1)
	}
}

// post is a channel message about one market.
type post struct {
	MessageID int
	At        time.Time
	Slug      string
}

// export is the part of Telegram Desktop's result.json we need.
type export struct {
	Messages []struct {
		ID           int    `json:"id"`
		Type         string `json:"type"`
		DateUnixtime string `json:"date_unixtime"`
		TextEntities []struct {
			Type string `json:"type"`
			Href string `json:"href"`
		} `json:"text_entities"`
	} `json:"messages"`
}

// parseExport returns the market posts in the export, in message order.
// Messages without a market link (service messages, manual posts) are skipped.
func parseExport(data []byte) ([]post, error) {
	var ex export
	if err := json.Unmarshal(data, &ex); err != nil {
		return nil, fmt.Errorf("parse export: %w", err)
	}
	var posts []post
	for _, msg := range ex.Messages {
		if msg.Type != "message" {
			continue
		}
		sec, err := strconv.ParseInt(msg.DateUnixtime, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("message %d: bad date_unixtime %q", msg.ID, msg.DateUnixtime)
		}
		for _, e := range msg.TextEntities {
			if slug := marketSlug(e.Href); e.Type == "text_link" && slug != "" {
				posts = append(posts, post{MessageID: msg.ID, At: time.Unix(sec, 0).UTC(), Slug: slug})
				break
			}
		}
	}
	return posts, nil
}

// marketSlug takes the market slug from a link built by the notifier:
// /event/<event>/<market> or /market/<market>.
// shortcut: an event-only link (/event/<event>, a market without a slug) is
// skipped; it would need a lookup by event_slug. Upgrade if the run reports
// many unmatched.
func marketSlug(href string) string {
	u, err := url.Parse(href)
	if err != nil || !strings.HasSuffix(u.Hostname(), "polymarket.com") {
		return ""
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	switch {
	case len(parts) == 3 && parts[0] == "event":
		return parts[2]
	case len(parts) == 2 && parts[0] == "market":
		return parts[1]
	}
	return ""
}

func run(ctx context.Context, log zerolog.Logger, path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	posts, err := parseExport(data)
	if err != nil {
		return err
	}
	if len(posts) == 0 {
		return fmt.Errorf("no market links found in %s: is it the channel's JSON export?", path)
	}

	mongoURI := os.Getenv("MONGO_URI")
	if mongoURI == "" {
		return fmt.Errorf("MONGO_URI is not set (load it with: set -a; . ./.env; set +a)")
	}
	dbCtx, dbCancel := context.WithTimeout(ctx, 10*time.Second)
	defer dbCancel()
	mongoDB, err := db.NewMongoDB(dbCtx, mongoURI, "polymarket")
	if err != nil {
		return fmt.Errorf("initialize MongoDB: %w", err)
	}
	defer func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = mongoDB.Close(closeCtx)
	}()
	// No timeout: the slug index on millions of markets takes minutes the first time.
	if err := mongoDB.EnsureMarketIndexes(ctx); err != nil {
		return err
	}

	return restore(ctx, log, mongoDB, posts)
}

// Restorer is MongoDB (implemented by *db.MongoDB).
type Restorer interface {
	RestoreNotified(ctx context.Context, slug string, messageID int, at time.Time) (bool, error)
}

// restore marks the posted markets as notified.
// In message order, so a market posted twice keeps its latest message.
func restore(ctx context.Context, log zerolog.Logger, s Restorer, posts []post) error {
	restored := 0
	for _, p := range posts {
		found, err := s.RestoreNotified(ctx, p.Slug, p.MessageID, p.At)
		if err != nil {
			return err
		}
		if !found {
			log.Warn().Int("message_id", p.MessageID).Str("slug", p.Slug).Msg("No market with this slug; load markets first")
			continue
		}
		restored++
	}
	log.Info().Int("posts", len(posts)).Int("restored", restored).Int("unmatched", len(posts)-restored).Msg("Restoring posts finished")
	return nil
}
