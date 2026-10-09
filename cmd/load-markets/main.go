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
	"golang.org/x/sync/errgroup"
)

const (
	progressEveryPages = 10
	// pageBuffer is how many fetched pages may wait for the DB writer (~2 MB).
	// When it is full, fetching pauses until the writer catches up.
	pageBuffer = 10
)

func main() {
	log, err := logger.New(os.Getenv("LOG_LEVEL"), os.Getenv("LOG_FORMAT"), os.Stdout)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	// Ctrl+C / SIGTERM stops the load; pages already written stay in the DB.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	err = run(ctx, log)
	// Read before stop(): stop() cancels ctx itself.
	interrupted := ctx.Err() != nil
	stop()

	switch {
	case err == nil:
	case interrupted:
		// Stopped on purpose: not a failure, the error is just "context canceled".
		log.Info().Msg("Market load stopped by signal; written pages are kept, rerun to continue")
		os.Exit(130) // 128 + SIGINT, the shell convention for an interrupted command
	default:
		log.Error().Err(err).Msg("Market load failed")
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

	client := polymarket.NewClient(polymarket.DefaultBaseURL, log)

	// Open and closed passes run in parallel. A market that closes during the
	// load may come from both; UpsertMarkets keeps the closed copy.
	// If one pass fails, the shared context stops the other one too.
	g, gctx := errgroup.WithContext(ctx)
	for _, closed := range []bool{false, true} {
		g.Go(func() error {
			return loadPass(gctx, log, client, mongoDB, closed)
		})
	}
	return g.Wait()
}

// loadPass is a two-stage pipeline: one goroutine fetches pages from the API,
// another writes them to MongoDB, so a page is written while the next one is
// being fetched.
func loadPass(ctx context.Context, log zerolog.Logger, client *polymarket.Client, mongoDB *db.MongoDB, closed bool) error {
	log = log.With().Bool("closed", closed).Logger()
	started := time.Now()
	pages, total, skipped := 0, 0, 0

	log.Info().Msg("Market load pass started")

	// Writer failure cancels gctx and stops the fetcher, and vice versa.
	g, gctx := errgroup.WithContext(ctx)
	pageCh := make(chan []polymarket.Market, pageBuffer)

	g.Go(func() error {
		defer close(pageCh)
		return client.AllMarkets(gctx, closed, func(page []polymarket.Market) error {
			select {
			case pageCh <- page:
				return nil
			case <-gctx.Done():
				return gctx.Err()
			}
		})
	})

	// Counters are touched only by the writer and read after g.Wait.
	g.Go(func() error {
		for page := range pageCh {
			n, err := mongoDB.UpsertMarkets(gctx, page, time.Now())
			if err != nil {
				return err
			}
			pages++
			total += len(page)
			skipped += n
			if pages%progressEveryPages == 0 {
				log.Info().Int("pages", pages).Int("markets", total).Msg("Loading markets")
			}
		}
		return nil
	})

	if err := g.Wait(); err != nil {
		if ctx.Err() != nil {
			// Stopped by a signal or by a failure of the other pass.
			log.Info().Int("pages", pages).Int("markets", total).Msg("Market load pass stopped")
		}
		return fmt.Errorf("closed=%t pass after %d pages (%d markets): %w", closed, pages, total, err)
	}

	log.Info().
		Int("pages", pages).
		Int("markets", total).
		Int("skipped_closed", skipped).
		Dur("took", time.Since(started).Round(time.Second)).
		Msg("Market load pass finished")
	return nil
}
