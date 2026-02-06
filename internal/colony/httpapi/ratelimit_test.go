// Package httpapi provides tests for rate limiting (RFD 031).
package httpapi

import (
	"testing"
	"time"
)

func TestParseRateLimit(t *testing.T) {
	tests := []struct {
		input    string
		wantErr  bool
		requests int
		window   time.Duration
	}{
		{"100/hour", false, 100, time.Hour},
		{"50/minute", false, 50, time.Minute},
		{"10/second", false, 10, time.Second},
		{"1/hour", false, 1, time.Hour},
		{"1000/minute", false, 1000, time.Minute},
		{"", false, 0, 0}, // Empty returns nil, no error.
		{"invalid", true, 0, 0},
		{"100", true, 0, 0},
		{"100/day", true, 0, 0},  // Day not supported.
		{"/hour", true, 0, 0},    // Missing number.
		{"0/hour", true, 0, 0},   // Zero not allowed.
		{"-10/hour", true, 0, 0}, // Negative not allowed (won't match regex).
		{"100/", true, 0, 0},     // Missing unit.
		{"abc/hour", true, 0, 0}, // Non-numeric count.
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			rl, err := ParseRateLimit(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseRateLimit(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
				return
			}

			if tt.input == "" && rl == nil && err == nil {
				return // Empty string returns nil, nil as expected.
			}

			if !tt.wantErr && rl != nil {
				if rl.Requests != tt.requests {
					t.Errorf("ParseRateLimit(%q) Requests = %d, want %d", tt.input, rl.Requests, tt.requests)
				}
				if rl.Window != tt.window {
					t.Errorf("ParseRateLimit(%q) Window = %v, want %v", tt.input, rl.Window, tt.window)
				}
			}
		})
	}
}

func TestFormatRateLimit(t *testing.T) {
	tests := []struct {
		rl   *RateLimit
		want string
	}{
		{nil, ""},
		{&RateLimit{Requests: 100, Window: time.Hour}, "100/hour"},
		{&RateLimit{Requests: 50, Window: time.Minute}, "50/minute"},
		{&RateLimit{Requests: 10, Window: time.Second}, "10/second"},
		{&RateLimit{Requests: 1, Window: time.Hour}, "1/hour"},
		{&RateLimit{Requests: 100, Window: 30 * time.Second}, "100/30s"}, // Non-standard window.
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			got := FormatRateLimit(tt.rl)
			if got != tt.want {
				t.Errorf("FormatRateLimit() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRateLimiter_Allow_NoLimit(t *testing.T) {
	rl := NewRateLimiter()

	// No limit should always allow.
	for i := 0; i < 1000; i++ {
		if !rl.Allow("token1", nil) {
			t.Errorf("Allow() with nil limit should always return true")
		}
	}
}

func TestRateLimiter_Allow_Basic(t *testing.T) {
	rl := NewRateLimiter()
	limit := &RateLimit{Requests: 3, Window: time.Minute}

	// First 3 requests should be allowed.
	for i := 0; i < 3; i++ {
		if !rl.Allow("token1", limit) {
			t.Errorf("Request %d should be allowed", i+1)
		}
	}

	// 4th request should be denied.
	if rl.Allow("token1", limit) {
		t.Error("Request 4 should be denied")
	}

	// Different token should have its own limit.
	if !rl.Allow("token2", limit) {
		t.Error("token2 first request should be allowed")
	}
}

func TestRateLimiter_Allow_WindowExpiry(t *testing.T) {
	rl := NewRateLimiter()
	// Use a very short window for testing.
	limit := &RateLimit{Requests: 2, Window: 50 * time.Millisecond}

	// Use up the limit.
	if !rl.Allow("token1", limit) {
		t.Error("Request 1 should be allowed")
	}
	if !rl.Allow("token1", limit) {
		t.Error("Request 2 should be allowed")
	}
	if rl.Allow("token1", limit) {
		t.Error("Request 3 should be denied")
	}

	// Wait for window to expire.
	time.Sleep(60 * time.Millisecond)

	// Should be allowed again.
	if !rl.Allow("token1", limit) {
		t.Error("Request after window expiry should be allowed")
	}
}

func TestRateLimiter_Remaining(t *testing.T) {
	rl := NewRateLimiter()
	limit := &RateLimit{Requests: 5, Window: time.Minute}

	// Initially all requests remaining.
	remaining := rl.Remaining("token1", limit)
	if remaining != 5 {
		t.Errorf("Remaining() = %d, want 5", remaining)
	}

	// Use some requests.
	rl.Allow("token1", limit)
	rl.Allow("token1", limit)

	remaining = rl.Remaining("token1", limit)
	if remaining != 3 {
		t.Errorf("Remaining() after 2 requests = %d, want 3", remaining)
	}

	// Use all remaining.
	rl.Allow("token1", limit)
	rl.Allow("token1", limit)
	rl.Allow("token1", limit)

	remaining = rl.Remaining("token1", limit)
	if remaining != 0 {
		t.Errorf("Remaining() after exhausted = %d, want 0", remaining)
	}

	// Unlimited returns -1.
	remaining = rl.Remaining("token1", nil)
	if remaining != -1 {
		t.Errorf("Remaining() with nil limit = %d, want -1", remaining)
	}
}

func TestRateLimiter_ResetTime(t *testing.T) {
	rl := NewRateLimiter()
	limit := &RateLimit{Requests: 3, Window: time.Minute}

	// No requests yet - reset time should be ~now + window.
	resetTime := rl.ResetTime("token1", limit)
	expectedReset := time.Now().Add(time.Minute)
	if resetTime.Before(time.Now()) {
		t.Error("ResetTime() should be in the future")
	}
	if resetTime.Sub(expectedReset) > time.Second {
		t.Errorf("ResetTime() difference from expected too large: %v", resetTime.Sub(expectedReset))
	}

	// Make a request.
	rl.Allow("token1", limit)

	// Reset time should be based on oldest request.
	resetTime = rl.ResetTime("token1", limit)
	if resetTime.Before(time.Now()) {
		t.Error("ResetTime() should be in the future after request")
	}

	// Unlimited returns zero time.
	zeroTime := rl.ResetTime("token1", nil)
	if !zeroTime.IsZero() {
		t.Errorf("ResetTime() with nil limit should be zero, got %v", zeroTime)
	}
}

func TestRateLimiter_SlidingWindow(t *testing.T) {
	rl := NewRateLimiter()
	limit := &RateLimit{Requests: 3, Window: 200 * time.Millisecond}

	// Make requests spread over time.
	rl.Allow("token1", limit) // t=0
	time.Sleep(80 * time.Millisecond)
	rl.Allow("token1", limit) // t=80ms
	time.Sleep(80 * time.Millisecond)
	rl.Allow("token1", limit) // t=160ms

	// Should be denied now.
	if rl.Allow("token1", limit) {
		t.Error("Should be denied at t=160ms")
	}

	// Wait until first request expires (t=200ms).
	time.Sleep(60 * time.Millisecond)

	// Should be allowed now (first request expired).
	if !rl.Allow("token1", limit) {
		t.Error("Should be allowed after first request expires")
	}
}

func TestRateLimiter_MultipleTokens(t *testing.T) {
	rl := NewRateLimiter()
	limit := &RateLimit{Requests: 2, Window: time.Minute}

	// Token 1 uses its limit.
	rl.Allow("token1", limit)
	rl.Allow("token1", limit)

	// Token 2 should have full limit.
	if remaining := rl.Remaining("token2", limit); remaining != 2 {
		t.Errorf("token2 Remaining() = %d, want 2", remaining)
	}

	// Token 1 should be exhausted.
	if remaining := rl.Remaining("token1", limit); remaining != 0 {
		t.Errorf("token1 Remaining() = %d, want 0", remaining)
	}

	// Token 2 can still make requests.
	if !rl.Allow("token2", limit) {
		t.Error("token2 should be allowed")
	}
}

func TestRateLimiter_DifferentLimits(t *testing.T) {
	rl := NewRateLimiter()
	limit1 := &RateLimit{Requests: 2, Window: time.Minute}
	limit2 := &RateLimit{Requests: 5, Window: time.Minute}

	// Use limit1 for token1.
	rl.Allow("token1", limit1)
	rl.Allow("token1", limit1)

	// Token1 should be exhausted with limit1.
	if rl.Allow("token1", limit1) {
		t.Error("token1 should be denied with limit1")
	}

	// But allowed with limit2 (different limit, but same token counter).
	// Note: This tests the behavior where the counter is shared but limits differ.
	// The actual remaining requests is based on the current window.
	if rl.Remaining("token1", limit2) != 3 {
		t.Errorf("token1 Remaining(limit2) = %d, want 3", rl.Remaining("token1", limit2))
	}
}
