package helpers

import (
	"testing"
	"time"
)

func TestTimeFlagsParse(t *testing.T) {
	tests := []struct {
		name        string
		since       string
		from        string
		to          string
		wantErr     bool
		checkDur    time.Duration // Expected duration between start and end
		description string
	}{
		{
			name:        "default since 1h",
			since:       "1h",
			from:        "",
			to:          "",
			wantErr:     false,
			checkDur:    time.Hour,
			description: "Default 1 hour range",
		},
		{
			name:        "since 5 minutes",
			since:       "5m",
			from:        "",
			to:          "",
			wantErr:     false,
			checkDur:    5 * time.Minute,
			description: "5 minute range",
		},
		{
			name:        "since 24 hours",
			since:       "24h",
			from:        "",
			to:          "",
			wantErr:     false,
			checkDur:    24 * time.Hour,
			description: "24 hour range",
		},
		{
			name:        "explicit from now to now",
			since:       "",
			from:        "now",
			to:          "now",
			wantErr:     false,
			checkDur:    0,
			description: "From and to both now",
		},
		{
			name:        "from with default to (now)",
			since:       "",
			from:        "2024-01-01T00:00:00Z",
			to:          "",
			wantErr:     false,
			description: "From specific time to now",
		},
		{
			name:        "invalid since duration",
			since:       "invalid",
			from:        "",
			to:          "",
			wantErr:     true,
			description: "Invalid duration format",
		},
		{
			name:        "invalid from time",
			since:       "",
			from:        "invalid-time",
			to:          "",
			wantErr:     true,
			description: "Invalid from time format",
		},
		{
			name:        "invalid to time",
			since:       "",
			from:        "now",
			to:          "invalid-time",
			wantErr:     true,
			description: "Invalid to time format",
		},
		{
			name:        "end before start",
			since:       "",
			from:        "2024-01-02T00:00:00Z",
			to:          "2024-01-01T00:00:00Z",
			wantErr:     true,
			description: "End time before start time",
		},
		{
			name:        "RFC3339 from and to",
			since:       "",
			from:        "2024-01-01T00:00:00Z",
			to:          "2024-01-01T01:00:00Z",
			wantErr:     false,
			checkDur:    time.Hour,
			description: "Explicit RFC3339 range",
		},
		{
			name:        "simple date format",
			since:       "",
			from:        "2024-01-01",
			to:          "2024-01-02",
			wantErr:     false,
			checkDur:    24 * time.Hour,
			description: "Simple date format",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			flags := &TimeFlags{
				Since: tt.since,
				From:  tt.from,
				To:    tt.to,
			}

			got, err := flags.Parse()
			if (err != nil) != tt.wantErr {
				t.Errorf("Parse() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if !tt.wantErr && got != nil {
				// Check duration if specified.
				if tt.checkDur > 0 {
					actualDur := got.End.Sub(got.Start)
					// Allow small tolerance for time differences.
					tolerance := time.Second
					if actualDur < tt.checkDur-tolerance || actualDur > tt.checkDur+tolerance {
						t.Errorf("Parse() duration = %v, want %v", actualDur, tt.checkDur)
					}
				}

				// Verify start is before or equal to end.
				if got.Start.After(got.End) {
					t.Errorf("Parse() start time after end time: start=%v, end=%v", got.Start, got.End)
				}
			}
		})
	}
}

func TestParseTime(t *testing.T) {
	now := time.Now()

	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{
			name:    "now keyword",
			input:   "now",
			wantErr: false,
		},
		{
			name:    "RFC3339 format",
			input:   "2024-01-01T00:00:00Z",
			wantErr: false,
		},
		{
			name:    "simple date format",
			input:   "2024-01-01",
			wantErr: false,
		},
		{
			name:    "datetime without timezone",
			input:   "2024-01-01T12:34:56",
			wantErr: false,
		},
		{
			name:    "invalid format",
			input:   "not-a-time",
			wantErr: true,
		},
		{
			name:    "empty string",
			input:   "",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseTime(tt.input, now)
			if (err != nil) != tt.wantErr {
				t.Errorf("parseTime() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if !tt.wantErr {
				if tt.input == "now" {
					// Should be close to the provided now time.
					if got.Unix() != now.Unix() {
						t.Errorf("parseTime('now') = %v, want %v", got, now)
					}
				} else {
					// Should return a valid time.
					if got.IsZero() {
						t.Errorf("parseTime() returned zero time for valid input: %s", tt.input)
					}
				}
			}
		})
	}
}
