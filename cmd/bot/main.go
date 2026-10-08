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
	jevAPIKey := os.Getenv("JEV_API_KEY")
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

	hybridAnalyzer := analyzer.NewHybridAnalyzer(jevAPIKey, log)

	// 4. Setup Error Group for managing concurrent tasks
	g, gCtx := errgroup.WithContext(ctx)

	// Task A: Health Check HTTP Server (keeps PaaS like Replit happy)
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})

	srv := &http.Server{
		Addr:    fmt.Sprintf(":%s", port),
		Handler: mux,
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

	// Task B: The Main Bot Worker (Mocked for now)
	g.Go(func() error {
		events := []analyzer.MarketEvent{
			{ID: "1", Title: "Will aliens be found in 2026?", Description: "Finding extraterrestrial life.", Price: 0.05},
			{ID: "2", Title: "Will Trump win the 2028 election?", Description: "US Presidential election.", Price: 0.45},
			{ID: "3", Title: "Will Elon Musk buy a new platform?", Description: "Musk's next acquisition.", Price: 0.20},
		}

		for _, event := range events {
			// Check if we need to shut down before processing the next event
			select {
			case <-gCtx.Done():
				log.Info().Msg("Worker received shutdown signal, stopping event processing")
				return nil
			default:
			}

			eventLog := log.With().Str("market_id", event.ID).Logger()

			analyzeCtx, cancelAnalyze := context.WithTimeout(gCtx, 5*time.Second)
			res, err := hybridAnalyzer.Analyze(analyzeCtx, event)
			cancelAnalyze()

			if err != nil {
				eventLog.Error().Err(err).Msg("Error analyzing event")
				continue
			}

			if res.IsStrange {
				eventLog.Info().
					Str("title", event.Title).
					Str("reason", res.Reason).
					Str("analyzer", res.AnalyzedBy).
					Msg("Found strange market")

				saveCtx, cancelSave := context.WithTimeout(gCtx, 5*time.Second)
				err = mongoDB.SaveMarketEvent(saveCtx, map[string]interface{}{
					"event":     event,
					"analysis":  res,
					"timestamp": time.Now(),
				})
				cancelSave()

				if err != nil {
					eventLog.Error().Err(err).Msg("Failed to save to MongoDB")
				} else {
					eventLog.Info().Msg("Saved strange market to MongoDB")
				}
			} else {
				eventLog.Info().Str("title", event.Title).Msg("Market is normal")
			}

			// Sleep with context awareness
			select {
			case <-gCtx.Done():
				log.Info().Msg("Worker interrupted during sleep")
				return nil
			case <-time.After(2 * time.Second):
			}
		}

		log.Info().Msg("Worker finished processing all mock events")
		return nil
	})

	// 5. Wait for all tasks to finish or a fatal error to occur
	return g.Wait()
}
