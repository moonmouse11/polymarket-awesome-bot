package command

import (
	"context"
	"errors"
	"github.com/moonmouse11/polymarket-awesome-bot/internal/config"
	"github.com/moonmouse11/polymarket-awesome-bot/internal/db"
	"io"
	"testing"

	"github.com/rs/zerolog"
)

func TestRun(t *testing.T) {
	log := zerolog.New(io.Discard)
	fail := errors.New("boom")

	tests := []struct {
		name     string
		ctx      context.Context
		runErr   error
		wantCode int
	}{
		{"success", context.Background(), nil, 0},
		{"failure", context.Background(), fail, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code := Run(tt.ctx, "Test", "rerun", log, func(context.Context) error { return tt.runErr })
			if code != tt.wantCode {
				t.Errorf("code = %d, want %d", code, tt.wantCode)
			}
		})
	}
}

func TestRunInterrupted(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	code := Run(ctx, "Test", "rerun", zerolog.New(io.Discard),
		func(context.Context) error { return context.Canceled })
	if code != 130 {
		t.Errorf("code = %d, want 130", code)
	}
}

func TestRunWithDBConnectError(t *testing.T) {
	cfg := config.Config{MongoURI: "mongodb://127.0.0.1:1", MongoDBName: "test"}
	err := RunWithDB(context.Background(), cfg, func(context.Context, *db.MongoDB) error {
		t.Fatal("run must not be called when connect fails")
		return nil
	})
	if err == nil {
		t.Fatal("expected connect error")
	}
}
