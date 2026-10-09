// Command mark-awesome recomputes the awesome fields for all stored markets
// from the excluded_tags and keywords collections. Run it manually: make markets-awesome.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/moonmouse11/polymarket-awesome-bot/internal/analyzer"
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

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	err = run(ctx, log)
	// Read before stop(): stop() cancels ctx itself.
	interrupted := ctx.Err() != nil
	stop()

	switch {
	case err == nil:
	case interrupted:
		// The update is a single server-side operation; rerun to recompute fully.
		log.Info().Msg("Awesome marking stopped by signal; rerun to recompute")
		os.Exit(130)
	default:
		log.Error().Err(err).Msg("Awesome marking failed")
		os.Exit(1)
	}
}

func run(ctx context.Context, log zerolog.Logger) error {
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

	// No timeout: building a new index on millions of markets takes minutes.
	if err := mongoDB.EnsureMarketIndexes(ctx); err != nil {
		return err
	}
	if err := mongoDB.EnsureExcludedTags(dbCtx, analyzer.DefaultExcludedTags); err != nil {
		return err
	}
	excluded, err := mongoDB.ListExcludedTags(dbCtx)
	if err != nil {
		return err
	}
	if err := mongoDB.EnsureKeywords(dbCtx, analyzer.DefaultKeywords); err != nil {
		return err
	}
	keywords, err := mongoDB.ListKeywords(dbCtx)
	if err != nil {
		return err
	}

	log.Info().Strs("excluded_tags", excluded).Strs("keywords", keywords).Msg("Awesome marking started")
	started := time.Now()

	// No timeout: one update over millions of documents takes minutes.
	matched, err := mongoDB.MarkAwesome(ctx, excluded, keywords)
	if err != nil {
		return err
	}
	total, open, err := mongoDB.CountAwesome(ctx)
	if err != nil {
		return err
	}

	log.Info().
		Int64("markets", matched).
		Int64("awesome", total).
		Int64("awesome_open", open).
		Dur("took", time.Since(started).Round(time.Second)).
		Msg("Awesome marking finished")
	return nil
}
