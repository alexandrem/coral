package logging

import (
	"io"
	"os"
	"time"

	"github.com/rs/zerolog"
)

// Config contains logger configuration.
type Config struct {
	// Level sets the logging level (debug, info, warn, error).
	Level string
	// Pretty enables human-readable console output with colors.
	Pretty bool
	// Output sets the output writer (defaults to os.Stdout).
	Output io.Writer
}

// DefaultConfig returns a default logger configuration.
func DefaultConfig() Config {
	return Config{
		Level:  "info",
		Pretty: true,
		Output: os.Stdout,
	}
}

// New creates a new zerolog logger with the given configuration.
func New(cfg Config) zerolog.Logger {
	// Set global time format
	zerolog.TimeFieldFormat = time.RFC3339

	// Parse log level
	level := zerolog.InfoLevel
	switch cfg.Level {
	case "debug":
		level = zerolog.DebugLevel
	case "info":
		level = zerolog.InfoLevel
	case "warn":
		level = zerolog.WarnLevel
	case "error":
		level = zerolog.ErrorLevel
	}

	// Set up output
	output := cfg.Output
	if output == nil {
		output = os.Stdout
	}

	// Use pretty console writer for human-readable output
	if cfg.Pretty {
		output = zerolog.ConsoleWriter{
			Out:        output,
			TimeFormat: "15:04:05",
			NoColor:    false,
		}
	}

	return zerolog.New(output).
		Level(level).
		With().
		Timestamp().
		Logger()
}

// NewWithComponent creates a logger with a component field for structured logging.
func NewWithComponent(cfg Config, component string) zerolog.Logger {
	return New(cfg).With().Str("component", component).Logger()
}
