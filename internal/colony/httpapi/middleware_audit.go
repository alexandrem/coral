// Package httpapi provides audit logging middleware for the HTTP API endpoint (RFD 031).
package httpapi

import (
	"net/http"
	"time"

	"github.com/rs/zerolog"
)

// AuditMiddleware logs all authenticated requests for security auditing.
type AuditMiddleware struct {
	logger zerolog.Logger
}

// NewAuditMiddleware creates a new audit logging middleware.
func NewAuditMiddleware(logger zerolog.Logger) *AuditMiddleware {
	return &AuditMiddleware{
		logger: logger.With().Str("component", "audit").Logger(),
	}
}

// Handler wraps an http.Handler with audit logging.
func (m *AuditMiddleware) Handler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		// Wrap response writer to capture status code.
		wrapped := &statusResponseWriter{
			ResponseWriter: w,
			status:         http.StatusOK,
		}

		// Call the next handler.
		next.ServeHTTP(wrapped, r)

		// Get authenticated token if present.
		token := GetAuthenticatedToken(r.Context())

		// Build audit log entry.
		event := m.logger.Info().
			Time("timestamp", start).
			Str("method", r.Method).
			Str("path", r.URL.Path).
			Str("remote_addr", r.RemoteAddr).
			Int("status", wrapped.status).
			Dur("duration", time.Since(start))

		// Add token info if present (but never log the token value itself).
		if token != nil {
			event.Str("token_id", token.TokenID)
		} else {
			event.Str("token_id", "anonymous")
		}

		// Add user agent if present.
		if ua := r.Header.Get("User-Agent"); ua != "" {
			event.Str("user_agent", ua)
		}

		// Add content length if present.
		if cl := r.ContentLength; cl > 0 {
			event.Int64("content_length", cl)
		}

		event.Msg("Request")
	})
}

// statusResponseWriter wraps http.ResponseWriter to capture the status code.
type statusResponseWriter struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

// WriteHeader captures the status code before calling the underlying WriteHeader.
func (w *statusResponseWriter) WriteHeader(code int) {
	if !w.wroteHeader {
		w.status = code
		w.wroteHeader = true
	}
	w.ResponseWriter.WriteHeader(code)
}

// Write captures the status code (defaults to 200) if WriteHeader wasn't called.
func (w *statusResponseWriter) Write(b []byte) (int, error) {
	if !w.wroteHeader {
		w.status = http.StatusOK
		w.wroteHeader = true
	}
	return w.ResponseWriter.Write(b)
}

// Unwrap returns the underlying ResponseWriter for middleware compatibility.
func (w *statusResponseWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

// AuditLog creates an audit logging middleware.
// This is a convenience function.
func AuditLog(logger zerolog.Logger) func(http.Handler) http.Handler {
	mw := NewAuditMiddleware(logger)
	return mw.Handler
}
