package database

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// ProfilingHotspot represents a top-K CPU hotspot (RFD 074).
type ProfilingHotspot struct {
	Rank        int32
	Frames      []string
	Percentage  float64
	SampleCount uint64
}

// ProfilingSummaryResult contains the top-K hotspots and total samples (RFD 074).
type ProfilingSummaryResult struct {
	Hotspots     []ProfilingHotspot
	TotalSamples uint64
}

// RegressionIndicatorResult represents a detected performance regression (RFD 074).
type RegressionIndicatorResult struct {
	Type               string  // "new_hotspot", "increased_hotspot", "decreased_hotspot".
	Message            string  // Human-readable description.
	BaselinePercentage float64 // Percentage in baseline deployment.
	CurrentPercentage  float64 // Percentage in current deployment.
	Delta              float64 // Percentage point change.
}

// GetTopKHotspots returns the top-K CPU hotspots for a service in the given time range (RFD 074).
// It aggregates cpu_profile_summaries by stack_hash, sums sample counts, and decodes frame IDs.
// Profiling infrastructure overhead stacks are filtered out to avoid Heisenberg effect.
func (d *Database) GetTopKHotspots(ctx context.Context, serviceName string, startTime, endTime time.Time, topK int) (*ProfilingSummaryResult, error) {
	if topK <= 0 {
		topK = 5
	}
	if topK > 20 {
		topK = 20
	}

	// Get total samples first.
	var totalSamples uint64
	err := d.db.QueryRowContext(ctx, `
		SELECT COALESCE(SUM(sample_count), 0)
		FROM cpu_profile_summaries
		WHERE service_name = ?
		  AND timestamp >= ? AND timestamp <= ?
	`, serviceName, startTime, endTime).Scan(&totalSamples)
	if err != nil {
		return nil, fmt.Errorf("failed to query total samples: %w", err)
	}

	if totalSamples == 0 {
		return &ProfilingSummaryResult{}, nil
	}

	// Fetch more rows than topK to allow filtering out profiling overhead.
	fetchLimit := topK * 3
	if fetchLimit < 15 {
		fetchLimit = 15
	}

	// Aggregate by stack_hash (which groups identical stacks), get candidates.
	rows, err := d.db.QueryContext(ctx, `
		SELECT stack_frame_ids, SUM(sample_count) as total_samples
		FROM cpu_profile_summaries
		WHERE service_name = ?
		  AND timestamp >= ? AND timestamp <= ?
		GROUP BY stack_hash, stack_frame_ids
		ORDER BY total_samples DESC
		LIMIT ?
	`, serviceName, startTime, endTime, fetchLimit)
	if err != nil {
		return nil, fmt.Errorf("failed to query top-K hotspots: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var hotspots []ProfilingHotspot
	rank := int32(1)

	for rows.Next() {
		var frameIDsRaw interface{}
		var sampleCount uint64

		if err := rows.Scan(&frameIDsRaw, &sampleCount); err != nil {
			return nil, fmt.Errorf("failed to scan hotspot row: %w", err)
		}

		frameIDs, err := convertArrayToInt64(frameIDsRaw)
		if err != nil {
			return nil, fmt.Errorf("failed to convert frame IDs: %w", err)
		}

		frameNames, err := d.DecodeStackFrames(ctx, frameIDs)
		if err != nil {
			return nil, fmt.Errorf("failed to decode stack frames: %w", err)
		}

		// Skip profiling infrastructure overhead to avoid Heisenberg effect.
		if IsProfilingOverheadStack(frameNames) {
			continue
		}

		hotspots = append(hotspots, ProfilingHotspot{
			Rank:        rank,
			Frames:      frameNames,
			Percentage:  float64(sampleCount) * 100.0 / float64(totalSamples),
			SampleCount: sampleCount,
		})
		rank++

		// Stop once we have enough.
		if len(hotspots) >= topK {
			break
		}
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating hotspot rows: %w", err)
	}

	return &ProfilingSummaryResult{
		Hotspots:     hotspots,
		TotalSamples: totalSamples,
	}, nil
}

// GetLatestBinaryMetadata returns the most recent deployment for a service (RFD 074).
func (d *Database) GetLatestBinaryMetadata(ctx context.Context, serviceName string) (*BinaryMetadata, error) {
	row := d.db.QueryRowContext(ctx, `
		SELECT build_id, service_name, binary_path, first_seen, last_seen, has_debug_info
		FROM binary_metadata_registry
		WHERE service_name = ?
		ORDER BY first_seen DESC
		LIMIT 1
	`, serviceName)

	var m BinaryMetadata
	err := row.Scan(&m.BuildID, &m.ServiceName, &m.BinaryPath, &m.FirstSeen, &m.LastSeen, &m.HasDebugInfo)
	if err != nil {
		return nil, err
	}
	return &m, nil
}

// GetPreviousBinaryMetadata returns the deployment before the current one (RFD 074).
func (d *Database) GetPreviousBinaryMetadata(ctx context.Context, serviceName, currentBuildID string) (*BinaryMetadata, error) {
	row := d.db.QueryRowContext(ctx, `
		SELECT build_id, service_name, binary_path, first_seen, last_seen, has_debug_info
		FROM binary_metadata_registry
		WHERE service_name = ?
		  AND build_id != ?
		ORDER BY first_seen DESC
		LIMIT 1
	`, serviceName, currentBuildID)

	var m BinaryMetadata
	err := row.Scan(&m.BuildID, &m.ServiceName, &m.BinaryPath, &m.FirstSeen, &m.LastSeen, &m.HasDebugInfo)
	if err != nil {
		return nil, err
	}
	return &m, nil
}

// CompareHotspotsWithBaseline detects regressions by comparing hotspots between builds (RFD 074).
// It compares the current build's hotspots with the baseline (previous) build's hotspots.
func (d *Database) CompareHotspotsWithBaseline(ctx context.Context, serviceName, currentBuildID, baselineBuildID string, startTime, endTime time.Time, topK int) ([]RegressionIndicatorResult, error) {
	if topK <= 0 {
		topK = 5
	}

	// Get current build hotspots.
	currentHotspots, err := d.getHotspotsByBuild(ctx, serviceName, currentBuildID, startTime, endTime, topK)
	if err != nil {
		return nil, fmt.Errorf("failed to get current hotspots: %w", err)
	}

	// Get baseline build hotspots (use full available range for baseline).
	baselineHotspots, err := d.getHotspotsByBuild(ctx, serviceName, baselineBuildID, time.Time{}, time.Now(), topK)
	if err != nil {
		return nil, fmt.Errorf("failed to get baseline hotspots: %w", err)
	}

	// Build baseline lookup by stack hash.
	baselineMap := make(map[string]float64) // stack_hash -> percentage.
	for _, h := range baselineHotspots {
		baselineMap[h.stackHash] = h.percentage
	}

	var indicators []RegressionIndicatorResult
	for _, curr := range currentHotspots {
		baselinePct, existed := baselineMap[curr.stackHash]
		delta := curr.percentage - baselinePct

		if !existed && curr.percentage > 5.0 {
			// New hotspot not in baseline top-K.
			indicators = append(indicators, RegressionIndicatorResult{
				Type:               "new_hotspot",
				Message:            fmt.Sprintf("%s (%.1f%%) was not in top-%d before this deployment", curr.topFrame, curr.percentage, topK),
				BaselinePercentage: 0,
				CurrentPercentage:  curr.percentage,
				Delta:              curr.percentage,
			})
		} else if existed && delta > 10.0 {
			// Increased hotspot.
			indicators = append(indicators, RegressionIndicatorResult{
				Type:               "increased_hotspot",
				Message:            fmt.Sprintf("%s increased from %.1f%% to %.1f%%", curr.topFrame, baselinePct, curr.percentage),
				BaselinePercentage: baselinePct,
				CurrentPercentage:  curr.percentage,
				Delta:              delta,
			})
		}
	}

	// Check for decreased hotspots (optimizations).
	currentMap := make(map[string]float64)
	for _, h := range currentHotspots {
		currentMap[h.stackHash] = h.percentage
	}
	for _, base := range baselineHotspots {
		currPct := currentMap[base.stackHash]
		delta := base.percentage - currPct
		if delta > 10.0 {
			indicators = append(indicators, RegressionIndicatorResult{
				Type:               "decreased_hotspot",
				Message:            fmt.Sprintf("%s decreased from %.1f%% to %.1f%%", base.topFrame, base.percentage, currPct),
				BaselinePercentage: base.percentage,
				CurrentPercentage:  currPct,
				Delta:              -delta,
			})
		}
	}

	return indicators, nil
}

// hotspotEntry is an internal type for regression comparison.
type hotspotEntry struct {
	stackHash  string
	topFrame   string
	percentage float64
}

// getHotspotsByBuild queries hotspots filtered by build_id.
func (d *Database) getHotspotsByBuild(ctx context.Context, serviceName, buildID string, startTime, endTime time.Time, topK int) ([]hotspotEntry, error) {
	query := `
		SELECT stack_hash, stack_frame_ids, SUM(sample_count) as total_samples
		FROM cpu_profile_summaries
		WHERE service_name = ?
		  AND build_id = ?
	`
	args := []interface{}{serviceName, buildID}

	if !startTime.IsZero() {
		query += " AND timestamp >= ?"
		args = append(args, startTime)
	}
	if !endTime.IsZero() {
		query += " AND timestamp <= ?"
		args = append(args, endTime)
	}

	query += `
		GROUP BY stack_hash, stack_frame_ids
		ORDER BY total_samples DESC
		LIMIT ?
	`
	args = append(args, topK)

	rows, err := d.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	// Get total samples for this build.
	var totalSamples uint64
	var entries []hotspotEntry
	type rawEntry struct {
		stackHash   string
		frameIDsRaw interface{}
		samples     uint64
	}
	var rawEntries []rawEntry

	for rows.Next() {
		var r rawEntry
		if err := rows.Scan(&r.stackHash, &r.frameIDsRaw, &r.samples); err != nil {
			return nil, err
		}
		totalSamples += r.samples
		rawEntries = append(rawEntries, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	if totalSamples == 0 {
		return nil, nil
	}

	for _, r := range rawEntries {
		frameIDs, err := convertArrayToInt64(r.frameIDsRaw)
		if err != nil {
			return nil, err
		}

		// Get the leaf frame (first in stack, leaf-to-root order) as representative name.
		topFrame := "unknown"
		if len(frameIDs) > 0 {
			frames, err := d.DecodeStackFrames(ctx, frameIDs)
			if err == nil && len(frames) > 0 {
				topFrame = SimplifyFrame(frames[0])
			}
		}

		entries = append(entries, hotspotEntry{
			stackHash:  r.stackHash,
			topFrame:   topFrame,
			percentage: float64(r.samples) * 100.0 / float64(totalSamples),
		})
	}

	return entries, nil
}

// SimplifyFrame strips internal package segments from a fully-qualified Go
// frame name (e.g., "crypto/internal/fips140/sha256.blockSHA2" becomes
// "crypto/sha256.blockSHA2"). This reduces noise for LLM consumers.
func SimplifyFrame(frame string) string {
	for {
		idx := strings.Index(frame, "/internal/")
		if idx < 0 {
			break
		}
		// Find the next "/" after "/internal/" to locate the resumed public path.
		rest := frame[idx+len("/internal/"):]
		slash := strings.Index(rest, "/")
		if slash < 0 {
			// No further slash: the internal segment leads directly to the
			// symbol (e.g., "runtime/internal/math.MulUintptr"). Remove only
			// the "/internal" part.
			frame = frame[:idx] + "/" + rest
		} else {
			frame = frame[:idx] + "/" + rest[slash+1:]
		}
	}
	return frame
}

// isBoilerplateFrame returns true for runtime and stdlib frames that add noise
// without actionable insight (goroutine entry, HTTP serving scaffolding, etc.).
func isBoilerplateFrame(frame string) bool {
	boilerplate := []string{
		"runtime.goexit",
		"runtime.main",
		"runtime.mcall",
		"runtime.mstart",
	}
	for _, b := range boilerplate {
		if frame == b {
			return true
		}
	}
	// net/http server internals and routing scaffolding are rarely actionable.
	if strings.HasPrefix(frame, "net/http.(*conn).") ||
		strings.HasPrefix(frame, "net/http.(*Server).") ||
		strings.HasPrefix(frame, "net/http.serverHandler.") ||
		frame == "net/http.HandlerFunc.ServeHTTP" ||
		frame == "net/http.(*ServeMux).ServeHTTP" {
		return true
	}
	return false
}

// IsProfilingOverheadStack returns true if the stack contains frames from the
// profiling infrastructure itself. These stacks represent observer overhead
// (Heisenberg effect) rather than actual application behavior.
// We specifically target pprof serialization paths, not general SDK usage.
func IsProfilingOverheadStack(frames []string) bool {
	for _, frame := range frames {
		// pprof write/serialization operations - the main source of overhead.
		if strings.HasPrefix(frame, "runtime/pprof.write") ||
			strings.HasPrefix(frame, "runtime/pprof.(*Profile).WriteTo") ||
			strings.HasPrefix(frame, "runtime/pprof.(*profileBuilder)") {
			return true
		}
		// net/http/pprof handlers serving profile requests.
		if strings.HasPrefix(frame, "net/http/pprof.") {
			return true
		}
		// compress/flate and compress/gzip when used by pprof serialization.
		// Only filter these if pprof frames are also present in the stack.
		if strings.HasPrefix(frame, "compress/flate.(*compressor)") ||
			strings.HasPrefix(frame, "compress/gzip.(*Writer)") {
			// Check if this is in a pprof context.
			for _, f := range frames {
				if strings.Contains(f, "runtime/pprof") || strings.Contains(f, "net/http/pprof") {
					return true
				}
			}
		}
	}
	return false
}

// CleanFrames simplifies package names and removes boilerplate frames from a
// leaf-to-root frame list. The result preserves the original leaf-to-root order
// for storage and API responses. Display-layer code can reverse for rendering.
func CleanFrames(frames []string) []string {
	var cleaned []string
	for _, f := range frames {
		simplified := SimplifyFrame(f)
		if !isBoilerplateFrame(simplified) {
			cleaned = append(cleaned, simplified)
		}
	}
	if len(cleaned) == 0 {
		// All frames were boilerplate — keep originals simplified.
		result := make([]string, len(frames))
		for i, f := range frames {
			result[i] = SimplifyFrame(f)
		}
		return result
	}
	return cleaned
}

// simplifyAndTrimFrames simplifies package names and removes boilerplate frames
// from a leaf-to-root frame list. Returns frames in caller→callee (root-to-leaf)
// order for display.
func simplifyAndTrimFrames(frames []string) []string {
	// Reverse to root-to-leaf order, simplify along the way.
	reversed := make([]string, len(frames))
	for i, f := range frames {
		reversed[len(frames)-1-i] = SimplifyFrame(f)
	}

	// Trim boilerplate from start (root side).
	start := 0
	for start < len(reversed) && isBoilerplateFrame(reversed[start]) {
		start++
	}

	// Trim boilerplate from end (leaf side).
	end := len(reversed)
	for end > start && isBoilerplateFrame(reversed[end-1]) {
		end--
	}

	if start >= end {
		return reversed
	}
	return reversed[start:end]
}

// ShortFunctionName extracts just the function name from a fully-qualified
// frame (e.g., "crypto/sha256.blockSHA2" → "blockSHA2").
func ShortFunctionName(frame string) string {
	if dot := strings.LastIndex(frame, "."); dot >= 0 {
		return frame[dot+1:]
	}
	return frame
}

// MinSamplesForSummary is the minimum number of total samples required to
// include profiling data. Below this threshold the data is too sparse to be
// actionable and would just add noise.
const MinSamplesForSummary = 20

// FormatCompactSummary formats the profiling summary in a compact,
// LLM-friendly format. It shows the hottest call path once and lists
// per-function sample percentages on a single line.
// Returns empty string when sample count is too low to be meaningful.
func FormatCompactSummary(period string, totalSamples uint64, hotspots []ProfilingHotspot) string {
	if len(hotspots) == 0 || totalSamples < MinSamplesForSummary {
		return ""
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("   CPU Profiling (%s, %d samples):\n", period, totalSamples))

	// Hot path: use the hottest stack (first hotspot).
	hotPath := simplifyAndTrimFrames(hotspots[0].Frames)
	if len(hotPath) > 0 {
		b.WriteString("     Hot path: ")
		b.WriteString(strings.Join(hotPath, " → "))
		b.WriteString("\n")
	}

	// Samples by function: leaf frame from each hotspot with percentage.
	b.WriteString("     Samples: ")
	for i, h := range hotspots {
		if i > 0 {
			b.WriteString(", ")
		}
		name := "unknown"
		if len(h.Frames) > 0 {
			name = ShortFunctionName(SimplifyFrame(h.Frames[0]))
		}
		b.WriteString(fmt.Sprintf("%s (%.1f%%)", name, h.Percentage))
	}
	b.WriteString("\n")

	return b.String()
}

// MinAllocBytesForSummary is the minimum total allocation bytes required to
// include memory profiling data. Below this threshold the data is too sparse
// to be actionable.
const MinAllocBytesForSummary = 1024 * 1024 // 1MB

// FormatCompactMemorySummary formats the memory profiling summary in a compact,
// LLM-friendly format. It shows the hottest allocation path once and lists
// per-function allocation percentages on a single line.
// Returns empty string when allocation bytes are too low to be meaningful.
func FormatCompactMemorySummary(period string, totalAllocBytes int64, hotspots []MemoryProfilingHotspot) string {
	if len(hotspots) == 0 || totalAllocBytes < MinAllocBytesForSummary {
		return ""
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("   Memory Profiling (%s, %s allocated):\n", period, formatBytes(totalAllocBytes)))

	// Hot path: use the hottest stack (first hotspot).
	hotPath := simplifyAndTrimFrames(hotspots[0].Frames)
	if len(hotPath) > 0 {
		b.WriteString("     Hot path: ")
		b.WriteString(strings.Join(hotPath, " → "))
		b.WriteString("\n")
	}

	// Allocations by function: leaf frame from each hotspot with percentage and bytes.
	b.WriteString("     Allocations: ")
	for i, h := range hotspots {
		if i > 0 {
			b.WriteString(", ")
		}
		name := "unknown"
		if len(h.Frames) > 0 {
			name = ShortFunctionName(SimplifyFrame(h.Frames[0]))
		}
		b.WriteString(fmt.Sprintf("%s (%.1f%%, %s)", name, h.Percentage, formatBytes(h.AllocBytes)))
	}
	b.WriteString("\n")

	return b.String()
}

// formatBytes formats bytes into a human-readable string.
func formatBytes(bytes int64) string {
	const (
		KB = 1024
		MB = KB * 1024
		GB = MB * 1024
	)

	switch {
	case bytes >= GB:
		return fmt.Sprintf("%.1fGB", float64(bytes)/float64(GB))
	case bytes >= MB:
		return fmt.Sprintf("%.1fMB", float64(bytes)/float64(MB))
	case bytes >= KB:
		return fmt.Sprintf("%.1fKB", float64(bytes)/float64(KB))
	default:
		return fmt.Sprintf("%dB", bytes)
	}
}
