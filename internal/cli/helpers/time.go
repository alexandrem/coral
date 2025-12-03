package helpers

import (
	"fmt"
	"time"

	"github.com/spf13/pflag"
)

// TimeRange represents a start and end time for a query.
type TimeRange struct {
	Start time.Time
	End   time.Time
}

// TimeFlags holds the flag values for time range parsing.
type TimeFlags struct {
	Since string
	From  string
	To    string
}

// AddFlags adds time range flags to a FlagSet.
func (f *TimeFlags) AddFlags(flags *pflag.FlagSet) {
	flags.StringVar(&f.Since, "since", "1h", "Show results since duration (e.g. 5m, 1h)")
	flags.StringVar(&f.From, "from", "", "Start time (RFC3339 or 'now')")
	flags.StringVar(&f.To, "to", "", "End time (RFC3339 or 'now')")
}

// Parse returns a TimeRange based on the flag values.
// Priority:
// 1. --from and --to (explicit range)
// 2. --since (relative to now)
func (f *TimeFlags) Parse() (*TimeRange, error) {
	now := time.Now()

	// Case 1: Explicit range using --from (and optionally --to)
	if f.From != "" {
		start, err := parseTime(f.From, now)
		if err != nil {
			return nil, fmt.Errorf("invalid --from time: %w", err)
		}

		end := now
		if f.To != "" {
			end, err = parseTime(f.To, now)
			if err != nil {
				return nil, fmt.Errorf("invalid --to time: %w", err)
			}
		}

		if end.Before(start) {
			return nil, fmt.Errorf("end time cannot be before start time")
		}

		return &TimeRange{Start: start, End: end}, nil
	}

	// Case 2: Relative range using --since
	if f.Since != "" {
		duration, err := time.ParseDuration(f.Since)
		if err != nil {
			return nil, fmt.Errorf("invalid --since duration: %w", err)
		}
		return &TimeRange{Start: now.Add(-duration), End: now}, nil
	}

	// Default to 1 hour if nothing specified (should be covered by default value of --since)
	return &TimeRange{Start: now.Add(-1 * time.Hour), End: now}, nil
}

func parseTime(s string, now time.Time) (time.Time, error) {
	if s == "now" {
		return now, nil
	}
	// Try RFC3339 first
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	// Try other formats if needed, e.g., simple date
	if t, err := time.Parse("2006-01-02", s); err == nil {
		return t, nil
	}
	if t, err := time.Parse("2006-01-02T15:04:05", s); err == nil {
		return t, nil
	}
	return time.Time{}, fmt.Errorf("unsupported time format (use RFC3339)")
}
