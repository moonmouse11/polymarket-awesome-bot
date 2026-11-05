package logger

import (
	"fmt"
	"io"
	"os"
	"time"

	"github.com/rs/zerolog"
)

// New builds the application logger.
//
// level:  debug | info | warn | error ("" means info)
// format: json | console ("" means json); console is human-readable for local use.
func New(level, format string, w io.Writer) (zerolog.Logger, error) {
	if level == "" {
		level = "info"
	}
	lvl, err := zerolog.ParseLevel(level)
	if err != nil || lvl == zerolog.NoLevel {
		return zerolog.Nop(), fmt.Errorf("invalid LOG_LEVEL %q: want debug, info, warn or error", level)
	}

	switch format {
	case "", "json":
	case "console":
		w = zerolog.ConsoleWriter{Out: w, TimeFormat: time.RFC3339}
	default:
		return zerolog.Nop(), fmt.Errorf("invalid LOG_FORMAT %q: want json or console", format)
	}

	return zerolog.New(w).Level(lvl).With().Timestamp().Logger(), nil
}

// Must is New for command mains: it exits with code 1 on invalid
// level/format instead of returning an error.
func Must(level, format string, w io.Writer) zerolog.Logger {
	log, err := New(level, format, w)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	return log
}
