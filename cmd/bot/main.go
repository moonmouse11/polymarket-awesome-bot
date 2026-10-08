package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/moonmouse11/polymarket-awesome-bot/internal/analyzer"
	"github.com/moonmouse11/polymarket-awesome-bot/internal/db"
	"golang.org/x/sync/errgroup"
)

func main() {
	log.Println("Starting Polymarket Bot...")

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
		log.Fatalf("Failed to initialize MongoDB: %v", err)
	}
	defer func() {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()
		if err := mongoDB.Close(shutdownCtx); err != nil {
			log.Printf("Error closing MongoDB: %v", err)
		} else {
			log.Println("MongoDB connection closed gracefully")
		}
	}()
	log.Println("Connected to MongoDB successfully")

	if err := mongoDB.EnsureKeywords(dbCtx, analyzer.DefaultKeywords); err != nil {
		log.Fatalf("Failed to prepare keywords collection: %v", err)
	}
	log.Println("Keywords collection is ready")

	hybridAnalyzer := analyzer.NewHybridAnalyzer(jevAPIKey)

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
		log.Printf("Starting health check server on port %s", port)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			return fmt.Errorf("http server error: %w", err)
		}
		return nil
	})

	g.Go(func() error {
		<-gCtx.Done()
		log.Println("Shutting down health check server...")
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
				log.Println("Worker received shutdown signal, stopping event processing...")
				return nil
			default:
			}

			analyzeCtx, cancelAnalyze := context.WithTimeout(gCtx, 5*time.Second)
			res, err := hybridAnalyzer.Analyze(analyzeCtx, event)
			cancelAnalyze()

			if err != nil {
				log.Printf("Error analyzing event %s: %v", event.ID, err)
				continue
			}

			if res.IsStrange {
				log.Printf("FOUND STRANGE MARKET: %s (Reason: %s, Analyzer: %s)", event.Title, res.Reason, res.AnalyzedBy)
				
				saveCtx, cancelSave := context.WithTimeout(gCtx, 5*time.Second)
				err = mongoDB.SaveMarketEvent(saveCtx, map[string]interface{}{
					"event":      event,
					"analysis":   res,
					"timestamp":  time.Now(),
				})
				cancelSave()

				if err != nil {
					log.Printf("Failed to save to MongoDB: %v", err)
				} else {
					log.Printf("Saved strange market %s to MongoDB", event.ID)
				}
			} else {
				log.Printf("Market %s is normal.", event.Title)
			}
			
			// Sleep with context awareness
			select {
			case <-gCtx.Done():
				log.Println("Worker interrupted during sleep...")
				return nil
			case <-time.After(2 * time.Second):
			}
		}
		
		log.Println("Worker finished processing all mock events.")
		return nil
	})

	// 5. Wait for all tasks to finish or a fatal error to occur
	if err := g.Wait(); err != nil {
		log.Printf("Bot stopped with error: %v", err)
	} else {
		log.Println("Bot stopped gracefully.")
	}
}
