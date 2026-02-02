// Package debug provides debug instrumentation for the agent.
package debug

import (
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"time"

	"github.com/google/pprof/profile"
	"github.com/rs/zerolog"
)

// MemoryProfileResult holds the parsed results of a memory profile collection.
type MemoryProfileResult struct {
	Samples      []MemoryStackSample
	Stats        MemoryStatsResult
	TopFunctions []TopAllocFunctionResult
	TopTypes     []TopAllocTypeResult
}

// MemoryStackSample represents a unique allocation stack with byte/object counts.
type MemoryStackSample struct {
	FrameNames   []string
	AllocBytes   int64
	AllocObjects int64
}

// MemoryStatsResult holds heap statistics from the SDK.
type MemoryStatsResult struct {
	AllocBytes            int64
	TotalAllocBytes       int64
	SysBytes              int64
	NumGC                 int64
	HeapGrowthBytesPerSec float64
}

// TopAllocFunctionResult represents a top allocating function.
type TopAllocFunctionResult struct {
	Function string
	Bytes    int64
	Objects  int64
	Pct      float64
}

// TopAllocTypeResult represents a top allocated type.
type TopAllocTypeResult struct {
	TypeName string
	Bytes    int64
	Objects  int64
	Pct      float64
}

// CollectMemoryProfile fetches a heap profile from the SDK and parses it.
func CollectMemoryProfile(sdkAddr string, durationSec int, logger zerolog.Logger) (*MemoryProfileResult, error) {
	// Fetch allocation profile from SDK pprof endpoint.
	url := fmt.Sprintf("http://%s/debug/pprof/allocs?seconds=%d", sdkAddr, durationSec)
	logger.Debug().Str("url", url).Int("duration", durationSec).Msg("Fetching memory profile from SDK")

	client := &http.Client{
		Timeout: time.Duration(durationSec+30) * time.Second,
	}

	resp, err := client.Get(url) //nolint:noctx // Internal SDK call, no user-controlled URL.
	if err != nil {
		return nil, fmt.Errorf("failed to fetch memory profile: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("SDK returned status %d: %s", resp.StatusCode, string(body))
	}

	// Parse pprof protobuf response.
	prof, err := profile.Parse(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to parse pprof profile: %w", err)
	}

	// Also fetch memstats for heap statistics.
	stats, err := fetchMemStats(sdkAddr, client)
	if err != nil {
		logger.Warn().Err(err).Msg("Failed to fetch memstats, using zeros")
	}

	result := parseProfile(prof, stats)
	return result, nil
}

// CollectHeapSnapshot fetches an instant heap profile (for continuous profiling).
func CollectHeapSnapshot(sdkAddr string, logger zerolog.Logger) (*MemoryProfileResult, error) {
	url := fmt.Sprintf("http://%s/debug/pprof/heap", sdkAddr)

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Get(url) //nolint:noctx
	if err != nil {
		return nil, fmt.Errorf("failed to fetch heap snapshot: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("SDK returned status %d: %s", resp.StatusCode, string(body))
	}

	// pprof heap profiles may be gzip-compressed.
	var reader io.Reader = resp.Body
	if resp.Header.Get("Content-Encoding") == "gzip" {
		gzReader, err := gzip.NewReader(resp.Body)
		if err != nil {
			return nil, fmt.Errorf("failed to create gzip reader: %w", err)
		}
		defer func() { _ = gzReader.Close() }()
		reader = gzReader
	}

	prof, err := profile.Parse(reader)
	if err != nil {
		return nil, fmt.Errorf("failed to parse pprof profile: %w", err)
	}

	stats, err := fetchMemStats(sdkAddr, client)
	if err != nil {
		// Not fatal for heap snapshots.
		stats = &MemoryStatsResult{}
	}

	return parseProfile(prof, stats), nil
}

// fetchMemStats fetches runtime.MemStats from the SDK.
func fetchMemStats(sdkAddr string, client *http.Client) (*MemoryStatsResult, error) {
	url := fmt.Sprintf("http://%s/debug/memstats", sdkAddr)
	resp, err := client.Get(url) //nolint:noctx
	if err != nil {
		return nil, fmt.Errorf("failed to fetch memstats: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("memstats returned status %d", resp.StatusCode)
	}

	var raw map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("failed to parse memstats: %w", err)
	}

	return &MemoryStatsResult{
		AllocBytes:      int64(getFloat(raw, "alloc_bytes")),
		TotalAllocBytes: int64(getFloat(raw, "total_alloc_bytes")),
		SysBytes:        int64(getFloat(raw, "sys_bytes")),
		NumGC:           int64(getFloat(raw, "num_gc")),
	}, nil
}

func getFloat(m map[string]interface{}, key string) float64 {
	if v, ok := m[key]; ok {
		if f, ok := v.(float64); ok {
			return f
		}
	}
	return 0
}

// parseProfile extracts allocation stacks, top functions, and top types from a pprof profile.
func parseProfile(prof *profile.Profile, stats *MemoryStatsResult) *MemoryProfileResult {
	if stats == nil {
		stats = &MemoryStatsResult{}
	}

	// Find the alloc_space and alloc_objects sample type indices.
	allocBytesIdx := -1
	allocObjectsIdx := -1
	for i, st := range prof.SampleType {
		switch st.Type {
		case "alloc_space":
			allocBytesIdx = i
		case "alloc_objects":
			allocObjectsIdx = i
		}
	}

	// Fallback to first two sample types if not found.
	if allocBytesIdx < 0 && len(prof.SampleType) > 0 {
		allocBytesIdx = 0
	}
	if allocObjectsIdx < 0 && len(prof.SampleType) > 1 {
		allocObjectsIdx = 1
	}

	var totalBytes int64
	var samples []MemoryStackSample

	// Aggregate by function to compute top functions.
	funcBytes := make(map[string]int64)
	funcObjects := make(map[string]int64)

	for _, s := range prof.Sample {
		var allocBytes, allocObjects int64
		if allocBytesIdx >= 0 && allocBytesIdx < len(s.Value) {
			allocBytes = s.Value[allocBytesIdx]
		}
		if allocObjectsIdx >= 0 && allocObjectsIdx < len(s.Value) {
			allocObjects = s.Value[allocObjectsIdx]
		}

		if allocBytes == 0 && allocObjects == 0 {
			continue
		}

		totalBytes += allocBytes

		// Extract frame names.
		frames := make([]string, 0, len(s.Location))
		for _, loc := range s.Location {
			for _, line := range loc.Line {
				if line.Function != nil {
					frames = append(frames, line.Function.Name)

					// Aggregate by leaf function (first in location list).
					funcBytes[line.Function.Name] += allocBytes
					funcObjects[line.Function.Name] += allocObjects
				}
			}
		}

		samples = append(samples, MemoryStackSample{
			FrameNames:   frames,
			AllocBytes:   allocBytes,
			AllocObjects: allocObjects,
		})
	}

	// Compute top functions.
	var topFunctions []TopAllocFunctionResult
	for fn, bytes := range funcBytes {
		pct := 0.0
		if totalBytes > 0 {
			pct = float64(bytes) / float64(totalBytes) * 100
		}
		topFunctions = append(topFunctions, TopAllocFunctionResult{
			Function: fn,
			Bytes:    bytes,
			Objects:  funcObjects[fn],
			Pct:      pct,
		})
	}
	sort.Slice(topFunctions, func(i, j int) bool {
		return topFunctions[i].Bytes > topFunctions[j].Bytes
	})
	if len(topFunctions) > 20 {
		topFunctions = topFunctions[:20]
	}

	return &MemoryProfileResult{
		Samples:      samples,
		Stats:        *stats,
		TopFunctions: topFunctions,
		TopTypes:     nil, // Type attribution requires deeper pprof analysis.
	}
}
