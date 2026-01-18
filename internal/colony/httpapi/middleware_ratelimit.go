// Package httpapi provides rate limiting middleware for the HTTP API endpoint (RFD 031).
package httpapi

import (
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/rs/zerolog"
)

// RateLimitMiddleware enforces per-token rate limits.
type RateLimitMiddleware struct {
	limiter *RateLimiter
	logger  zerolog.Logger
}

// NewRateLimitMiddleware creates a new rate limiting middleware.
func NewRateLimitMiddleware(logger zerolog.Logger) *RateLimitMiddleware {
	return &RateLimitMiddleware{
		limiter: NewRateLimiter(),
		logger:  logger.With().Str("middleware", "ratelimit").Logger(),
	}
}

// Handler wraps an http.Handler with rate limiting.
func (m *RateLimitMiddleware) Handler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := GetAuthenticatedToken(r.Context())
		if token == nil {
			// No token, skip rate limiting.
			next.ServeHTTP(w, r)
			return
		}

		// Parse rate limit from token config.
		limit, err := ParseRateLimit(token.RateLimit)
		if err != nil {
			m.logger.Warn().
				Str("token_id", token.TokenID).
				Str("rate_limit", token.RateLimit).
				Err(err).
				Msg("Invalid rate limit configuration, skipping rate limiting")
			next.ServeHTTP(w, r)
			return
		}

		if limit == nil {
			// No rate limit configured.
			next.ServeHTTP(w, r)
			return
		}

		// Check rate limit.
		if !m.limiter.Allow(token.TokenID, limit) {
			resetTime := m.limiter.ResetTime(token.TokenID, limit)
			retryAfter := int(time.Until(resetTime).Seconds())
			if retryAfter < 1 {
				retryAfter = 1
			}

			m.logger.Warn().
				Str("token_id", token.TokenID).
				Str("path", r.URL.Path).
				Str("rate_limit", token.RateLimit).
				Int("retry_after_seconds", retryAfter).
				Msg("Rate limit exceeded")

			w.Header().Set("Retry-After", strconv.Itoa(retryAfter))
			w.Header().Set("X-RateLimit-Limit", strconv.Itoa(limit.Requests))
			w.Header().Set("X-RateLimit-Remaining", "0")
			w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(resetTime.Unix(), 10))
			http.Error(w, "Too Many Requests", http.StatusTooManyRequests)
			return
		}

		// Add rate limit headers.
		remaining := m.limiter.Remaining(token.TokenID, limit)
		resetTime := m.limiter.ResetTime(token.TokenID, limit)
		w.Header().Set("X-RateLimit-Limit", strconv.Itoa(limit.Requests))
		w.Header().Set("X-RateLimit-Remaining", strconv.Itoa(remaining))
		w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(resetTime.Unix(), 10))
		w.Header().Set("X-RateLimit-Policy", fmt.Sprintf("%d;w=%d", limit.Requests, int(limit.Window.Seconds())))

		next.ServeHTTP(w, r)
	})
}

// Limiter returns the underlying rate limiter for testing.
func (m *RateLimitMiddleware) Limiter() *RateLimiter {
	return m.limiter
}

// ApplyRateLimit creates a rate limiting middleware.
// This is a convenience function.
func ApplyRateLimit(logger zerolog.Logger) func(http.Handler) http.Handler {
	mw := NewRateLimitMiddleware(logger)
	return mw.Handler
}
