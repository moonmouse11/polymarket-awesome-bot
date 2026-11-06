// Package command runs one-shot maintenance commands (load-markets,
// mark-awesome) with graceful Ctrl+C/SIGTERM handling.
package command

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/rs/zerolog"
)

// Main runs run with a context canceled on SIGINT/SIGTERM and exits the
// process with Run's exit code.
func Main(name, rerunHint string, log zerolog.Logger, run func(ctx context.Context) error) {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	os.Exit(Run(ctx, name, rerunHint, log, run))
}

// Run reports the outcome and returns the exit code: 0 on success, 130 when
// interrupted (rerunHint tells how to continue), 1 on error.
func Run(ctx context.Context, name, rerunHint string, log zerolog.Logger, run func(ctx context.Context) error) int {
	err := run(ctx)
	switch {
	case err == nil:
		return 0
	case ctx.Err() != nil:
		// Stopped on purpose: not a failure, the error is just "context canceled".
		log.Info().Msg(name + " stopped by signal; " + rerunHint)
		return 130 // 128 + SIGINT, the shell convention for an interrupted command
	default:
		log.Error().Err(err).Msg(name + " failed")
		return 1
	}
}
