// Command load-markets performs a full load of all Polymarket markets (open
// and closed) into the "markets" collection. Run it manually: make markets-load.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/moonmouse11/polymarket-awesome-bot/internal/db"
	"github.com/moonmouse11/polymarket-awesome-bot/internal/logger"
	"github.com/moonmouse11/polymarket-awesome-bot/internal/polymarket"
	"github.com/rs/zerolog"
)

const progressEveryPages = 10

func main() {
	log, err := logger.New(os.Getenv("LOG_LEVEL"), os.Getenv("LOG_FORMAT"), os.Stdout)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	if err := run(log); err != nil {
		log.Error().Err(err).Msg("Market load failed")
		os.Exit(1)
	}
}

func run(log zerolog.Logger) error {
	// Ctrl+C / SIGTERM stops the load; pages already written stay in the DB.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

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

	if err := mongoDB.EnsureMarketIndexes(dbCtx); err != nil {
		return err
	}

	client := polymarket.NewClient(polymarket.DefaultBaseURL, log)

	// Open first, then closed: a market that closes between the two passes
	// is overwritten by the closed pass and ends up with closed=true.
	for _, closed := range []bool{false, true} {
		if err := loadPass(ctx, log, client, mongoDB, closed); err != nil {
			return err
		}
	}
	return nil
}

func loadPass(ctx context.Context, log zerolog.Logger, client *polymarket.Client, mongoDB *db.MongoDB, closed bool) error {
	log = log.With().Bool("closed", closed).Logger()
	started := time.Now()
	pages, total := 0, 0

	log.Info().Msg("Market load pass started")
	err := client.AllMarkets(ctx, closed, func(page []polymarket.Market) error {
		if err := mongoDB.UpsertMarkets(ctx, page, time.Now()); err != nil {
			return err
		}
		pages++
		total += len(page)
		if pages%progressEveryPages == 0 {
			log.Info().Int("pages", pages).Int("markets", total).Msg("Loading markets")
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("closed=%t pass after %d pages (%d markets): %w", closed, pages, total, err)
	}

	log.Info().
		Int("pages", pages).
		Int("markets", total).
		Dur("took", time.Since(started).Round(time.Second)).
		Msg("Market load pass finished")
	return nil
}
