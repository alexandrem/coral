package logging

import (
	"bytes"
	"strings"
	"testing"

	"github.com/rs/zerolog"
)

func TestNew_TraceLevel(t *testing.T) {
	var buf bytes.Buffer
	logger := New(Config{
		Level:  "trace",
		Pretty: false,
		Output: &buf,
	})

	// Test that trace level messages are logged.
	logger.Trace().Msg("trace message")
	logger.Debug().Msg("debug message")
	logger.Info().Msg("info message")

	output := buf.String()

	if !strings.Contains(output, "trace message") {
		t.Error("Expected trace message to be logged at trace level")
	}
	if !strings.Contains(output, "debug message") {
		t.Error("Expected debug message to be logged at trace level")
	}
	if !strings.Contains(output, "info message") {
		t.Error("Expected info message to be logged at trace level")
	}
}

func TestNew_DebugLevel(t *testing.T) {
	var buf bytes.Buffer
	logger := New(Config{
		Level:  "debug",
		Pretty: false,
		Output: &buf,
	})

	// Test that trace messages are NOT logged at debug level.
	logger.Trace().Msg("trace message")
	logger.Debug().Msg("debug message")
	logger.Info().Msg("info message")

	output := buf.String()

	if strings.Contains(output, "trace message") {
		t.Error("Expected trace message to NOT be logged at debug level")
	}
	if !strings.Contains(output, "debug message") {
		t.Error("Expected debug message to be logged at debug level")
	}
	if !strings.Contains(output, "info message") {
		t.Error("Expected info message to be logged at debug level")
	}
}

func TestNew_InfoLevel(t *testing.T) {
	var buf bytes.Buffer
	logger := New(Config{
		Level:  "info",
		Pretty: false,
		Output: &buf,
	})

	// Test that trace and debug messages are NOT logged at info level.
	logger.Trace().Msg("trace message")
	logger.Debug().Msg("debug message")
	logger.Info().Msg("info message")

	output := buf.String()

	if strings.Contains(output, "trace message") {
		t.Error("Expected trace message to NOT be logged at info level")
	}
	if strings.Contains(output, "debug message") {
		t.Error("Expected debug message to NOT be logged at info level")
	}
	if !strings.Contains(output, "info message") {
		t.Error("Expected info message to be logged at info level")
	}
}

func TestNew_WarnLevel(t *testing.T) {
	var buf bytes.Buffer
	logger := New(Config{
		Level:  "warn",
		Pretty: false,
		Output: &buf,
	})

	logger.Info().Msg("info message")
	logger.Warn().Msg("warn message")

	output := buf.String()

	if strings.Contains(output, "info message") {
		t.Error("Expected info message to NOT be logged at warn level")
	}
	if !strings.Contains(output, "warn message") {
		t.Error("Expected warn message to be logged at warn level")
	}
}

func TestNew_ErrorLevel(t *testing.T) {
	var buf bytes.Buffer
	logger := New(Config{
		Level:  "error",
		Pretty: false,
		Output: &buf,
	})

	logger.Warn().Msg("warn message")
	logger.Error().Msg("error message")

	output := buf.String()

	if strings.Contains(output, "warn message") {
		t.Error("Expected warn message to NOT be logged at error level")
	}
	if !strings.Contains(output, "error message") {
		t.Error("Expected error message to be logged at error level")
	}
}

func TestNewWithComponent(t *testing.T) {
	var buf bytes.Buffer
	logger := NewWithComponent(Config{
		Level:  "info",
		Pretty: false,
		Output: &buf,
	}, "test-component")

	logger.Info().Msg("test message")

	output := buf.String()

	if !strings.Contains(output, "test-component") {
		t.Error("Expected log to contain component name 'test-component'")
	}
	if !strings.Contains(output, "test message") {
		t.Error("Expected log to contain message 'test message'")
	}
}

func TestNew_PrettyOutput(t *testing.T) {
	var buf bytes.Buffer
	logger := New(Config{
		Level:  "info",
		Pretty: true,
		Output: &buf,
	})

	logger.Info().Msg("test message")

	output := buf.String()

	// Pretty output should contain the message (specific formatting may vary).
	if !strings.Contains(output, "test message") {
		t.Error("Expected pretty output to contain message 'test message'")
	}
}

func TestNew_DefaultOutput(t *testing.T) {
	// Test that logger doesn't panic when Output is nil.
	logger := New(Config{
		Level:  "info",
		Pretty: false,
		Output: nil, // Should default to os.Stdout.
	})

	// This should not panic.
	logger.Info().Msg("test message")
}

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()

	if cfg.Level != "info" {
		t.Errorf("Expected default level 'info', got '%s'", cfg.Level)
	}
	if !cfg.Pretty {
		t.Error("Expected default pretty to be true")
	}
}

func TestNew_InvalidLevel(t *testing.T) {
	var buf bytes.Buffer
	logger := New(Config{
		Level:  "invalid",
		Pretty: false,
		Output: &buf,
	})

	// Invalid level should default to info.
	logger.Debug().Msg("debug message")
	logger.Info().Msg("info message")

	output := buf.String()

	// Debug should not be logged (defaults to info level).
	if strings.Contains(output, "debug message") {
		t.Error("Expected debug message to NOT be logged with invalid level (should default to info)")
	}
	if !strings.Contains(output, "info message") {
		t.Error("Expected info message to be logged with invalid level (should default to info)")
	}
}

func TestNew_LevelHierarchy(t *testing.T) {
	// Test the complete level hierarchy.
	levels := []struct {
		level    string
		expected zerolog.Level
	}{
		{"trace", zerolog.TraceLevel},
		{"debug", zerolog.DebugLevel},
		{"info", zerolog.InfoLevel},
		{"warn", zerolog.WarnLevel},
		{"error", zerolog.ErrorLevel},
	}

	for _, tc := range levels {
		t.Run(tc.level, func(t *testing.T) {
			var buf bytes.Buffer
			logger := New(Config{
				Level:  tc.level,
				Pretty: false,
				Output: &buf,
			})

			// Check the level is correctly set.
			if logger.GetLevel() != tc.expected {
				t.Errorf("Expected level %v, got %v", tc.expected, logger.GetLevel())
			}
		})
	}
}
