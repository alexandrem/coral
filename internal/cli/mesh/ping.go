package mesh

import "fmt"

func formatAge(seconds int64) string {
	if seconds < 60 {
		return fmt.Sprintf("%ds ago", seconds)
	}
	return fmt.Sprintf("%dm%ds ago", seconds/60, seconds%60)
}
