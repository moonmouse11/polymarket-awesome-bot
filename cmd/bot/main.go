package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/moonmouse11/polymarket-awesome-bot/internal/analyzer"
	"github.com/moonmouse11/polymarket-awesome-bot/internal/db"
	"github.com/moonmouse11/polymarket-awesome-bot/internal/logger"
	"github.com/moonmouse11/polymarket-awesome-bot/internal/notifier"
	"github.com/moonmouse11/polymarket-awesome-bot/internal/polymarket"
	"github.com/moonmouse11/polymarket-awesome-bot/internal/updater"
	"github.com/rs/zerolog"
	"golang.org/x/sync/errgroup"
)

func main() {
	log, err := logger.New(os.Getenv("LOG_LEVEL"), os.Getenv("LOG_FORMAT"), os.Stdout)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	// run returns instead of exiting, so its deferred cleanup (MongoDB close) always runs.
	if err := run(log); err != nil {
		log.Error().Err(err).Msg("Bot stopped with error")
		os.Exit(1)
	}
	log.Info().Msg("Bot stopped gracefully")
}

func run(log zerolog.Logger) error {
	log.Info().Msg("Starting Polymarket Bot")

	// 1. Setup graceful shutdown context
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// 2. Read environment variables
	mongoURI := os.Getenv("MONGO_URI")
	if mongoURI == "" {
		mongoURI = "mongodb://localhost:27017"
	}
	pollInterval, err := updater.ParseInterval(os.Getenv("POLL_INTERVAL"))
	if err != nil {
		return err
	}
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080" // Default port for health checks (required by Replit/Render/etc)
	}

	// 3. Initialize Database
	dbCtx, dbCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer dbCancel()

	mongoDB, err := db.NewMongoDB(dbCtx, mongoURI, "polymarket")
	if err != nil {
		return fmt.Errorf("initialize MongoDB: %w", err)
	}
	defer func() {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()
		if err := mongoDB.Close(shutdownCtx); err != nil {
			log.Error().Err(err).Msg("Error closing MongoDB")
		} else {
			log.Info().Msg("MongoDB connection closed gracefully")
		}
	}()
	log.Info().Msg("Connected to MongoDB")

	if err := mongoDB.EnsureKeywords(dbCtx, analyzer.DefaultKeywords); err != nil {
		return fmt.Errorf("prepare keywords collection: %w", err)
	}
	log.Info().Msg("Keywords collection is ready")

	keywordWords, err := mongoDB.ListKeywords(dbCtx)
	if err != nil {
		return fmt.Errorf("load keywords: %w", err)
	}
	log.Info().Int("count", len(keywordWords)).Msg("Loaded keywords from MongoDB")

	if err := mongoDB.EnsureExcludedTags(dbCtx, analyzer.DefaultExcludedTags); err != nil {
		return fmt.Errorf("prepare excluded tags collection: %w", err)
	}
	// No timeout: building a new index on millions of markets takes minutes.
	if err := mongoDB.EnsureMarketIndexes(ctx); err != nil {
		return err
	}

	// Telegram is optional: without a token the bot only tracks markets.
	var tg *notifier.Telegram
	if token := os.Getenv("TELEGRAM_BOT_TOKEN"); token != "" {
		if tg, err = notifier.NewTelegram(token, os.Getenv("TELEGRAM_CHANNEL_ID")); err != nil {
			return err
		}
		// A wrong token or channel stops the bot. Telegram being unreachable
		// does not: markets keep updating, notifications wait in the queue.
		switch err := tg.Check(ctx); {
		case err == nil:
			log.Info().Msg("Telegram channel is ready")
		case notifier.IsConfigError(err):
			return err
		default:
			log.Warn().Err(err).Msg("Telegram is unreachable; notifications will be sent when it is back")
		}
	} else {
		log.Info().Msg("TELEGRAM_BOT_TOKEN is not set; notifications are off")
	}

	// 4. Setup Error Group for managing concurrent tasks
	g, gCtx := errgroup.WithContext(ctx)

	// Task A: Health Check HTTP Server (keeps PaaS like Replit happy)
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("OK"))
	})

	srv := &http.Server{
		Addr:              fmt.Sprintf(":%s", port),
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	g.Go(func() error {
		log.Info().Str("port", port).Msg("Starting health check server")
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("http server error: %w", err)
		}
		return nil
	})

	g.Go(func() error {
		<-gCtx.Done()
		log.Info().Msg("Shutting down health check server")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	})

	// Task B: keep markets up to date (polls the API every POLL_INTERVAL)
	upd := updater.New(polymarket.NewClient(polymarket.DefaultBaseURL, log), mongoDB, log, pollInterval)
	g.Go(func() error {
		return upd.Run(gCtx)
	})

	// Task C: announce markets that became awesome
	if tg != nil {
		n := notifier.New(mongoDB, tg, log)
		g.Go(func() error {
			return n.Run(gCtx)
		})
	}

	// 5. Wait for all tasks to finish or a fatal error to occur
	return g.Wait()
}
