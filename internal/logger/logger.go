package logger

import (
	"os"
	"time"

	"github.com/rs/zerolog"
)

// New creates and returns a configured zerolog.Logger.
// In production (LOG_FORMAT=json), structured JSON is emitted.
// Otherwise, a human-friendly console writer is used.
func New() zerolog.Logger {
	zerolog.TimeFieldFormat = time.RFC3339

	if os.Getenv("LOG_FORMAT") == "json" {
		return zerolog.New(os.Stdout).With().Timestamp().Logger()
	}

	output := zerolog.ConsoleWriter{
		Out:        os.Stdout,
		TimeFormat: "15:04:05",
	}
	return zerolog.New(output).With().Timestamp().Logger()
}