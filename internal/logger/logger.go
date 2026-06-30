package logger

import (
	"os"
	"time"

	"github.com/rs/zerolog"
)

// L is the global logger. Use it directly or via the package-level helpers below.
var L zerolog.Logger

func init() {
	output := zerolog.ConsoleWriter{
		Out:        os.Stderr,
		TimeFormat: time.RFC3339,
	}
	L = zerolog.New(output).With().Timestamp().Logger()
}

// With returns a context with the given component field pre-set, useful for
// creating a sub-logger scoped to a package or goroutine.
//
//	log := logger.With("audio")
func With(component string) zerolog.Logger {
	return L.With().Str("component", component).Logger()
}

func Debug() *zerolog.Event { return L.Debug() }
func Info() *zerolog.Event  { return L.Info() }
func Warn() *zerolog.Event  { return L.Warn() }
func Error() *zerolog.Event { return L.Error() }

// Fatal logs at fatal level then calls os.Exit(1).
func Fatal() *zerolog.Event { return L.Fatal() }
