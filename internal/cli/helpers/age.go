package helpers

import (
	"fmt"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"
)

// FormatAge displays the age of an observation, preserving unknown timestamps.
func FormatAge(timestamp *timestamppb.Timestamp, now time.Time) string {
	if timestamp == nil || timestamp.CheckValid() != nil || timestamp.AsTime().IsZero() {
		return "unknown"
	}
	elapsed := max(now.Sub(timestamp.AsTime()), 0)
	if elapsed < time.Minute {
		return fmt.Sprintf("%ds ago", int(elapsed.Seconds()))
	}
	if elapsed < time.Hour {
		return fmt.Sprintf("%dm ago", int(elapsed.Minutes()))
	}
	if elapsed < 24*time.Hour {
		return fmt.Sprintf("%dh ago", int(elapsed.Hours()))
	}
	return fmt.Sprintf("%dd ago", int(elapsed.Hours()/24))
}
