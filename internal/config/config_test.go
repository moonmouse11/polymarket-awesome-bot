package config

import (
	"testing"
	"time"
)

func TestLoadRequiresMongoURI(t *testing.T) {
	t.Setenv("MONGO_URI", "") // empty is treated as unset, even when .env provides a value
	if _, err := Load(); err == nil {
		t.Fatal("expected error when MONGO_URI is not set")
	}
}

func TestLoadDefaults(t *testing.T) {
	t.Setenv("MONGO_URI", "mongodb://localhost:27017")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.MongoDBName != "polymarket" {
		t.Errorf("MongoDBName = %q, want polymarket", cfg.MongoDBName)
	}
	if cfg.Port != "8080" {
		t.Errorf("Port = %q, want 8080", cfg.Port)
	}
	if cfg.PollInterval != 5*time.Minute {
		t.Errorf("PollInterval = %v, want 5m", cfg.PollInterval)
	}
}

func TestLoadOverrides(t *testing.T) {
	t.Setenv("MONGO_URI", "mongodb://example:27017")
	t.Setenv("MONGO_DB", "other")
	t.Setenv("PORT", "9090")
	t.Setenv("POLL_INTERVAL", "90s")
	t.Setenv("LOG_LEVEL", "debug")
	t.Setenv("LOG_FORMAT", "json")
	t.Setenv("TELEGRAM_BOT_TOKEN", "tok")
	t.Setenv("TELEGRAM_CHANNEL_ID", "chan")
	t.Setenv("TELEGRAM_ADMIN_IDS", "1, 2")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.MongoURI != "mongodb://example:27017" {
		t.Errorf("MongoURI = %q", cfg.MongoURI)
	}
	if cfg.MongoDBName != "other" {
		t.Errorf("MongoDBName = %q", cfg.MongoDBName)
	}
	if cfg.Port != "9090" {
		t.Errorf("Port = %q", cfg.Port)
	}
	if cfg.PollInterval != 90*time.Second {
		t.Errorf("PollInterval = %v", cfg.PollInterval)
	}
	if cfg.LogLevel != "debug" || cfg.LogFormat != "json" {
		t.Errorf("Log = %q/%q", cfg.LogLevel, cfg.LogFormat)
	}
	if cfg.TelegramToken != "tok" || cfg.TelegramChannel != "chan" {
		t.Errorf("Telegram = %q/%q", cfg.TelegramToken, cfg.TelegramChannel)
	}
	if len(cfg.TelegramAdminIDs) != 2 || cfg.TelegramAdminIDs[0] != 1 || cfg.TelegramAdminIDs[1] != 2 {
		t.Errorf("TelegramAdminIDs = %v", cfg.TelegramAdminIDs)
	}
}

func TestLoadInvalidValues(t *testing.T) {
	t.Setenv("MONGO_URI", "mongodb://localhost:27017")
	t.Setenv("POLL_INTERVAL", "not-a-duration")
	t.Setenv("TELEGRAM_ADMIN_IDS", "abc")

	if _, err := Load(); err == nil {
		t.Fatal("expected error for invalid POLL_INTERVAL and TELEGRAM_ADMIN_IDS")
	}
}

func TestMustLoad(t *testing.T) {
	t.Setenv("MONGO_URI", "mongodb://localhost:27017")
	if cfg := MustLoad(); cfg.MongoURI == "" {
		t.Error("MustLoad returned empty MongoURI")
	}
}
