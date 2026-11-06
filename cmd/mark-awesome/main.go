// Command mark-awesome recomputes the awesome fields for all stored markets
// from the filter_rules collection. Run it manually: make markets-awesome.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/moonmouse11/polymarket-awesome-bot/internal/command"
	"github.com/moonmouse11/polymarket-awesome-bot/internal/config"
	"github.com/moonmouse11/polymarket-awesome-bot/internal/db"
	"github.com/moonmouse11/polymarket-awesome-bot/internal/logger"
	"github.com/rs/zerolog"
)

func main() {
	cfg := config.MustLoad()
	log := logger.Must(cfg.LogLevel, cfg.LogFormat, os.Stdout)

	// The update is a single server-side operation; rerun to recompute fully.
	command.Main("Awesome marking", "rerun to recompute", log,
		func(ctx context.Context) error { return run(ctx, log, cfg) })
}

func run(ctx context.Context, log zerolog.Logger, cfg config.Config) error {
	mongoDB, err := db.Connect(ctx, cfg.MongoURI, cfg.MongoDBName)
	if err != nil {
		return fmt.Errorf("initialize MongoDB: %w", err)
	}
	defer mongoDB.CloseWithTimeout()

	// No timeout: building a new index on millions of markets takes minutes.
	if err := mongoDB.EnsureMarketIndexes(ctx); err != nil {
		return err
	}
	if err := mongoDB.EnsureFilterRules(ctx); err != nil {
		return err
	}
	rules, err := mongoDB.FilterRules(ctx)
	if err != nil {
		return err
	}
	if rules.Empty() {
		// Every market would become awesome.
		return errors.New("no deny rules in filter_rules: add one with make rule-add first")
	}

	log.Info().Interface("rules", rules).Msg("Awesome marking started")
	started := time.Now()

	// No timeout: one update over millions of documents takes minutes.
	matched, err := mongoDB.MarkAwesome(ctx, rules)
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
