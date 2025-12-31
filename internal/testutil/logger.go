package testutil

import (
	"io"
	"testing"

	"github.com/rs/zerolog"
)

// NewTestLogger creates a test logger that discards output.
// Use NewTestLoggerWithOutput to log to t.Log().
func NewTestLogger(t *testing.T) zerolog.Logger {
	return zerolog.New(io.Discard).With().Timestamp().Logger()
}

// NewTestLoggerWithOutput creates a test logger that logs to t.Log().
func NewTestLoggerWithOutput(t *testing.T) zerolog.Logger {
	return zerolog.New(&testLogWriter{t: t}).With().Timestamp().Logger()
}

// testLogWriter wraps testing.T to implement io.Writer.
type testLogWriter struct {
	t *testing.T
}

func (w *testLogWriter) Write(p []byte) (n int, err error) {
	w.t.Log(string(p))
	return len(p), nil
}
