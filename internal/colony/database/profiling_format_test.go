package database

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSimplifyFrame(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "no internal segment",
			input: "crypto/sha256.Sum256",
			want:  "crypto/sha256.Sum256",
		},
		{
			name:  "single internal segment",
			input: "crypto/internal/fips140/sha256.blockSHA2",
			want:  "crypto/sha256.blockSHA2",
		},
		{
			name:  "internal segment without further slash",
			input: "runtime/internal/math.MulUintptr",
			want:  "runtime/math.MulUintptr",
		},
		{
			name:  "nested internal segments",
			input: "crypto/internal/fips140/internal/impl/sha256.block",
			want:  "crypto/sha256.block",
		},
		{
			name:  "plain function",
			input: "main.handler",
			want:  "main.handler",
		},
		{
			name:  "method receiver",
			input: "crypto/internal/fips140/sha256.(*Digest).Write",
			want:  "crypto/sha256.(*Digest).Write",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, SimplifyFrame(tt.input))
		})
	}
}

func TestIsBoilerplateFrame(t *testing.T) {
	boilerplate := []string{
		"runtime.goexit",
		"runtime.main",
		"runtime.mcall",
		"runtime.mstart",
		"net/http.(*conn).serve",
		"net/http.(*Server).Serve.gowrap3",
		"net/http.serverHandler.ServeHTTP",
		"net/http.HandlerFunc.ServeHTTP",
		"net/http.(*ServeMux).ServeHTTP",
	}
	for _, frame := range boilerplate {
		assert.True(t, isBoilerplateFrame(frame), "expected %q to be boilerplate", frame)
	}

	notBoilerplate := []string{
		"main.handler",
		"runtime.duffzero",
		"crypto/sha256.Sum256",
	}
	for _, frame := range notBoilerplate {
		assert.False(t, isBoilerplateFrame(frame), "expected %q to not be boilerplate", frame)
	}
}

func TestShortFunctionName(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"crypto/sha256.blockSHA2", "blockSHA2"},
		{"main.handler", "handler"},
		{"runtime.goexit", "goexit"},
		{"net/http.(*ServeMux).ServeHTTP", "ServeHTTP"},
		{"singleword", "singleword"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			assert.Equal(t, tt.want, ShortFunctionName(tt.input))
		})
	}
}

func TestCleanFrames(t *testing.T) {
	tests := []struct {
		name   string
		frames []string // leaf-to-root order.
		want   []string // leaf-to-root order, cleaned.
	}{
		{
			name: "removes boilerplate and simplifies",
			frames: []string{
				"crypto/internal/fips140/sha256.blockSHA2",
				"main.handler",
				"net/http.(*conn).serve",
				"runtime.goexit",
			},
			want: []string{"crypto/sha256.blockSHA2", "main.handler"},
		},
		{
			name: "preserves leaf-to-root order",
			frames: []string{
				"crypto/sha256.Sum256",
				"main.cpuWork",
				"main.handler",
			},
			want: []string{"crypto/sha256.Sum256", "main.cpuWork", "main.handler"},
		},
		{
			name: "all boilerplate returns simplified originals",
			frames: []string{
				"runtime.goexit",
				"runtime.main",
			},
			want: []string{"runtime.goexit", "runtime.main"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, CleanFrames(tt.frames))
		})
	}
}

func TestSimplifyAndTrimFrames(t *testing.T) {
	tests := []struct {
		name   string
		frames []string // leaf-to-root order.
		want   []string // root-to-leaf order, trimmed.
	}{
		{
			name: "trims boilerplate from both ends",
			frames: []string{
				"crypto/sha256.Sum256",
				"main.handler",
				"net/http.(*conn).serve",
				"runtime.goexit",
			},
			want: []string{"main.handler", "crypto/sha256.Sum256"},
		},
		{
			name: "simplifies internal packages",
			frames: []string{
				"crypto/internal/fips140/sha256.blockSHA2",
				"main.cpuWork",
			},
			want: []string{"main.cpuWork", "crypto/sha256.blockSHA2"},
		},
		{
			name:   "single frame no boilerplate",
			frames: []string{"main.handler"},
			want:   []string{"main.handler"},
		},
		{
			name: "all boilerplate returns all reversed",
			frames: []string{
				"runtime.goexit",
				"runtime.main",
			},
			want: []string{"runtime.main", "runtime.goexit"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, simplifyAndTrimFrames(tt.frames))
		})
	}
}

func TestFormatCompactSummary(t *testing.T) {
	t.Run("empty hotspots", func(t *testing.T) {
		result := FormatCompactSummary("5m", 100, nil)
		assert.Empty(t, result)
	})

	t.Run("too few samples", func(t *testing.T) {
		hotspots := []ProfilingHotspot{{
			Rank: 1, Frames: []string{"main.work"}, Percentage: 100.0, SampleCount: 16,
		}}
		result := FormatCompactSummary("20m", 16, hotspots)
		assert.Empty(t, result, "should suppress profiling with fewer than 20 samples")
	})

	t.Run("single hotspot single frame", func(t *testing.T) {
		hotspots := []ProfilingHotspot{{
			Rank: 1, Frames: []string{"main.work"}, Percentage: 100.0, SampleCount: 50,
		}}
		result := FormatCompactSummary("5m", 50, hotspots)
		assert.Contains(t, result, "CPU Profiling (5m, 50 samples):")
		assert.Contains(t, result, "Samples: work (100.0%)")
		// Single frame — hot path should still show.
		assert.Contains(t, result, "Hot path: main.work")
	})

	t.Run("sha256 call chain deduplication", func(t *testing.T) {
		// Simulates the real scenario: multiple hotspots within the same sha256 call chain.
		hotspots := []ProfilingHotspot{
			{
				Rank: 1, Percentage: 34.5, SampleCount: 20,
				Frames: []string{
					"crypto/internal/fips140/sha256.blockSHA2",
					"crypto/internal/fips140/sha256.(*Digest).Write",
					"crypto/internal/fips140/sha256.(*Digest).checkSum",
					"crypto/internal/fips140/sha256.(*Digest).Sum",
					"crypto/sha256.Sum256",
					"main.cpuIntensiveWork",
					"main.handler",
					"net/http.HandlerFunc.ServeHTTP",
					"net/http.(*ServeMux).ServeHTTP",
					"net/http.serverHandler.ServeHTTP",
					"net/http.(*conn).serve",
					"net/http.(*Server).Serve.gowrap3",
					"runtime.goexit",
				},
			},
			{
				Rank: 2, Percentage: 19.0, SampleCount: 11,
				Frames: []string{
					"crypto/sha256.Sum256",
					"main.cpuIntensiveWork",
					"main.handler",
					"net/http.HandlerFunc.ServeHTTP",
					"net/http.(*ServeMux).ServeHTTP",
					"net/http.serverHandler.ServeHTTP",
					"net/http.(*conn).serve",
					"net/http.(*Server).Serve.gowrap3",
					"runtime.goexit",
				},
			},
			{
				Rank: 3, Percentage: 8.6, SampleCount: 5,
				Frames: []string{
					"crypto/internal/fips140/sha256.(*Digest).Write",
					"crypto/internal/fips140/sha256.(*Digest).checkSum",
					"crypto/internal/fips140/sha256.(*Digest).Sum",
					"crypto/sha256.Sum256",
					"main.cpuIntensiveWork",
					"main.handler",
					"net/http.HandlerFunc.ServeHTTP",
					"net/http.(*ServeMux).ServeHTTP",
					"net/http.serverHandler.ServeHTTP",
					"net/http.(*conn).serve",
					"net/http.(*Server).Serve.gowrap3",
					"runtime.goexit",
				},
			},
		}

		result := FormatCompactSummary("2m", 58, hotspots)

		// Header.
		assert.Contains(t, result, "CPU Profiling (2m, 58 samples):")

		// Hot path: boilerplate trimmed, internals simplified.
		assert.Contains(t, result, "Hot path:")
		assert.NotContains(t, result, "runtime.goexit")
		assert.NotContains(t, result, "net/http.(*conn).serve")
		assert.NotContains(t, result, "/internal/fips140/")
		// Should contain the meaningful path.
		assert.Contains(t, result, "main.handler")
		assert.Contains(t, result, "main.cpuIntensiveWork")
		assert.Contains(t, result, "crypto/sha256.blockSHA2")

		// Samples line: short function names with percentages.
		assert.Contains(t, result, "Samples: blockSHA2 (34.5%), Sum256 (19.0%), Write (8.6%)")

		// Should be concise: only 3 lines (header, hot path, samples).
		lines := strings.Split(strings.TrimSpace(result), "\n")
		assert.Len(t, lines, 3)
	})

	t.Run("diverse hotspots from different paths", func(t *testing.T) {
		// Different call chains — not all part of one call tree.
		hotspots := []ProfilingHotspot{
			{
				Rank: 1, Percentage: 42.5, SampleCount: 2834,
				Frames: []string{
					"crypto/rsa.VerifyPKCS1v15",
					"main.validateSignature",
					"main.processOrder",
					"runtime.goexit",
				},
			},
			{
				Rank: 2, Percentage: 12.0, SampleCount: 800,
				Frames: []string{
					"runtime.gcBgMarkWorker",
					"runtime.goexit",
				},
			},
			{
				Rank: 3, Percentage: 8.5, SampleCount: 567,
				Frames: []string{
					"proto.Marshal",
					"main.saveOrder",
					"main.processOrder",
					"runtime.goexit",
				},
			},
		}

		result := FormatCompactSummary("5m", 6667, hotspots)

		// Hot path from the hottest stack.
		assert.Contains(t, result, "main.processOrder → main.validateSignature → crypto/rsa.VerifyPKCS1v15")
		assert.NotContains(t, result, "runtime.goexit")

		// All three function names in samples.
		assert.Contains(t, result, "VerifyPKCS1v15 (42.5%)")
		assert.Contains(t, result, "gcBgMarkWorker (12.0%)")
		assert.Contains(t, result, "Marshal (8.5%)")
	})
}
