package errors

import (
	"bytes"
	"errors"
	"io"
	"testing"

	"github.com/rs/zerolog"
)

type mockCloser struct {
	closeErr error
	closed   bool
}

func (m *mockCloser) Close() error {
	m.closed = true
	return m.closeErr
}

func TestDeferClose(t *testing.T) {
	tests := []struct {
		name       string
		closer     io.Closer
		closeErr   error
		wantLogged bool
	}{
		{
			name:       "nil closer",
			closer:     nil,
			wantLogged: false,
		},
		{
			name:       "successful close",
			closer:     &mockCloser{},
			wantLogged: false,
		},
		{
			name:       "close with error",
			closer:     &mockCloser{closeErr: errors.New("close failed")},
			wantLogged: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			logger := zerolog.New(&buf)

			DeferClose(logger, tt.closer, "test close")

			if tt.closer != nil {
				mc := tt.closer.(*mockCloser)
				if !mc.closed {
					t.Error("Close() was not called")
				}
			}

			logged := buf.Len() > 0
			if logged != tt.wantLogged {
				t.Errorf("logged = %v, want %v", logged, tt.wantLogged)
			}
		})
	}
}

func TestDeferRollback(t *testing.T) {
	t.Run("nil transaction", func(t *testing.T) {
		var buf bytes.Buffer
		logger := zerolog.New(&buf)

		// Should not panic or log with nil transaction.
		DeferRollback(logger, nil)

		if buf.Len() > 0 {
			t.Error("expected no logging for nil transaction")
		}
	})

	// Note: Testing actual rollback behavior requires a database connection.
	// The function correctly handles nil checks and sql.ErrTxDone per implementation.
	// Integration tests with real transactions should be added if needed.
}

func TestMust(t *testing.T) {
	tests := []struct {
		name      string
		err       error
		msg       string
		wantPanic bool
	}{
		{
			name:      "no error",
			err:       nil,
			msg:       "initialization",
			wantPanic: false,
		},
		{
			name:      "with error",
			err:       errors.New("failed"),
			msg:       "initialization",
			wantPanic: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defer func() {
				r := recover()
				if (r != nil) != tt.wantPanic {
					t.Errorf("panic = %v, want %v", r != nil, tt.wantPanic)
				}
			}()

			Must(tt.err, tt.msg)
		})
	}
}
