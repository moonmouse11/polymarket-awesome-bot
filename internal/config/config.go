// Package config reads and validates the process environment once at startup.
package config

import (
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/moonmouse11/polymarket-awesome-bot/internal/admin"
	"github.com/moonmouse11/polymarket-awesome-bot/internal/updater"
)

// Config holds every environment-driven setting of the application.
type Config struct {
	MongoURI    string // MONGO_URI, required
	MongoDBName string // MONGO_DB, defaults to "polymarket"

	LogLevel  string // LOG_LEVEL (empty is fine, logger decides)
	LogFormat string // LOG_FORMAT

	PollInterval time.Duration // POLL_INTERVAL
	Port         string        // PORT, defaults to "8080"

	TelegramToken    string  // TELEGRAM_BOT_TOKEN (optional; empty disables Telegram)
	TelegramChannel  string  // TELEGRAM_CHANNEL_ID
	TelegramAdminIDs []int64 // TELEGRAM_ADMIN_IDS
}

// Load reads the environment. Only secrets are required; operational values
// carry safe defaults. All validation errors are joined into one.
func Load() (Config, error) {
	var cfg Config

	var errs []error
	cfg.MongoURI = os.Getenv("MONGO_URI")
	if cfg.MongoURI == "" {
		errs = append(errs, errors.New("MONGO_URI is not set (load it with: set -a; . ./.env; set +a)"))
	}

	cfg.MongoDBName = os.Getenv("MONGO_DB")
	if cfg.MongoDBName == "" {
		cfg.MongoDBName = "polymarket"
	}

	cfg.LogLevel = os.Getenv("LOG_LEVEL")
	cfg.LogFormat = os.Getenv("LOG_FORMAT")

	var err error
	if cfg.PollInterval, err = updater.ParseInterval(os.Getenv("POLL_INTERVAL")); err != nil {
		errs = append(errs, err)
	}

	cfg.Port = os.Getenv("PORT")
	if cfg.Port == "" {
		cfg.Port = "8080"
	}

	cfg.TelegramToken = os.Getenv("TELEGRAM_BOT_TOKEN")
	cfg.TelegramChannel = os.Getenv("TELEGRAM_CHANNEL_ID")
	if cfg.TelegramAdminIDs, err = admin.ParseAdminIDs(os.Getenv("TELEGRAM_ADMIN_IDS")); err != nil {
		errs = append(errs, err)
	}

	if len(errs) > 0 {
		return Config{}, fmt.Errorf("invalid configuration: %w", errors.Join(errs...))
	}
	return cfg, nil
}

// MustLoad is Load for command mains: it exits with code 1 on invalid
// environment instead of returning an error.
func MustLoad() Config {
	cfg, err := Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	return cfg
}
