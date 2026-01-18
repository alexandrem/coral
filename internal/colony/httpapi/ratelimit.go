// Package httpapi provides rate limiting for the HTTP API endpoint (RFD 031).
package httpapi

import (
	"fmt"
	"regexp"
	"strconv"
	"sync"
	"time"
)

// RateLimit represents a rate limit configuration.
type RateLimit struct {
	Requests int           // Number of requests allowed.
	Window   time.Duration // Time window.
}

// RateLimiter tracks request rates per token using a sliding window algorithm.
type RateLimiter struct {
	mu       sync.RWMutex
	counters map[string]*slidingWindow // tokenID -> window
}

type slidingWindow struct {
	requests []time.Time
	limit    *RateLimit
}

// NewRateLimiter creates a new rate limiter.
func NewRateLimiter() *RateLimiter {
	rl := &RateLimiter{
		counters: make(map[string]*slidingWindow),
	}

	// Start background cleanup goroutine.
	go rl.cleanup()

	return rl
}

// Allow checks if a request is allowed for the given token.
// Returns true if the request is within the rate limit, false otherwise.
func (rl *RateLimiter) Allow(tokenID string, limit *RateLimit) bool {
	if limit == nil {
		return true // No limit configured.
	}

	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	window, exists := rl.counters[tokenID]
	if !exists {
		window = &slidingWindow{
			requests: make([]time.Time, 0, limit.Requests),
			limit:    limit,
		}
		rl.counters[tokenID] = window
	}

	// Remove expired requests outside the window.
	cutoff := now.Add(-limit.Window)
	valid := make([]time.Time, 0, len(window.requests))
	for _, t := range window.requests {
		if t.After(cutoff) {
			valid = append(valid, t)
		}
	}
	window.requests = valid

	// Check if we're within the limit.
	if len(window.requests) >= limit.Requests {
		return false
	}

	// Add current request.
	window.requests = append(window.requests, now)
	return true
}

// Remaining returns the number of remaining requests for a token.
func (rl *RateLimiter) Remaining(tokenID string, limit *RateLimit) int {
	if limit == nil {
		return -1 // Unlimited.
	}

	rl.mu.RLock()
	defer rl.mu.RUnlock()

	window, exists := rl.counters[tokenID]
	if !exists {
		return limit.Requests
	}

	now := time.Now()
	cutoff := now.Add(-limit.Window)
	count := 0
	for _, t := range window.requests {
		if t.After(cutoff) {
			count++
		}
	}

	return limit.Requests - count
}

// ResetTime returns the time when the rate limit window resets for a token.
func (rl *RateLimiter) ResetTime(tokenID string, limit *RateLimit) time.Time {
	if limit == nil {
		return time.Time{}
	}

	rl.mu.RLock()
	defer rl.mu.RUnlock()

	window, exists := rl.counters[tokenID]
	if !exists || len(window.requests) == 0 {
		return time.Now().Add(limit.Window)
	}

	// Find the oldest request still in the window.
	now := time.Now()
	cutoff := now.Add(-limit.Window)
	var oldest time.Time
	for _, t := range window.requests {
		if t.After(cutoff) {
			if oldest.IsZero() || t.Before(oldest) {
				oldest = t
			}
		}
	}

	if oldest.IsZero() {
		return now.Add(limit.Window)
	}

	return oldest.Add(limit.Window)
}

// cleanup periodically removes stale entries from the rate limiter.
func (rl *RateLimiter) cleanup() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		rl.mu.Lock()
		now := time.Now()
		for tokenID, window := range rl.counters {
			if window.limit == nil {
				continue
			}
			cutoff := now.Add(-window.limit.Window)
			// Remove entries with no recent requests.
			hasRecent := false
			for _, t := range window.requests {
				if t.After(cutoff) {
					hasRecent = true
					break
				}
			}
			if !hasRecent {
				delete(rl.counters, tokenID)
			}
		}
		rl.mu.Unlock()
	}
}

// rateLimitPattern matches rate limit strings like "100/hour", "50/minute", "10/second".
var rateLimitPattern = regexp.MustCompile(`^(\d+)/(hour|minute|second)$`)

// ParseRateLimit parses a rate limit string like "100/hour".
// Returns nil if the string is empty.
func ParseRateLimit(s string) (*RateLimit, error) {
	if s == "" {
		return nil, nil
	}

	matches := rateLimitPattern.FindStringSubmatch(s)
	if matches == nil {
		return nil, fmt.Errorf("invalid rate limit format: %q (expected format: N/hour, N/minute, or N/second)", s)
	}

	count, err := strconv.Atoi(matches[1])
	if err != nil {
		return nil, fmt.Errorf("invalid rate limit count: %w", err)
	}

	if count <= 0 {
		return nil, fmt.Errorf("rate limit count must be positive")
	}

	var window time.Duration
	switch matches[2] {
	case "hour":
		window = time.Hour
	case "minute":
		window = time.Minute
	case "second":
		window = time.Second
	}

	return &RateLimit{
		Requests: count,
		Window:   window,
	}, nil
}

// FormatRateLimit formats a rate limit as a string.
func FormatRateLimit(rl *RateLimit) string {
	if rl == nil {
		return ""
	}

	var unit string
	switch rl.Window {
	case time.Hour:
		unit = "hour"
	case time.Minute:
		unit = "minute"
	case time.Second:
		unit = "second"
	default:
		// For non-standard windows, express in seconds.
		return fmt.Sprintf("%d/%ds", rl.Requests, int(rl.Window.Seconds()))
	}

	return fmt.Sprintf("%d/%s", rl.Requests, unit)
}
