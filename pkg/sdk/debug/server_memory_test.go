package debug

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"

	"github.com/google/pprof/profile"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestServer(t *testing.T) *Server {
	t.Helper()
	logger := slog.Default()
	s, err := NewServer(logger, nil)
	require.NoError(t, err)
	return s
}

func TestMemStatsEndpoint(t *testing.T) {
	s := newTestServer(t)
	ts := httptest.NewServer(s)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/debug/memstats")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var stats map[string]interface{}
	err = json.NewDecoder(resp.Body).Decode(&stats)
	require.NoError(t, err)

	// Verify expected fields exist.
	for _, field := range []string{"alloc_bytes", "total_alloc_bytes", "sys_bytes", "num_gc", "heap_alloc", "heap_sys"} {
		_, ok := stats[field]
		assert.True(t, ok, "missing field: %s", field)
	}

	// Alloc bytes should be positive (test process has allocations).
	assert.Greater(t, stats["alloc_bytes"].(float64), float64(0))
}

func TestMemoryProfileConfigEndpoint(t *testing.T) {
	s := newTestServer(t)
	ts := httptest.NewServer(s)
	defer ts.Close()

	// Save and restore original rate.
	originalRate := runtime.MemProfileRate
	defer func() { runtime.MemProfileRate = originalRate }()

	body := strings.NewReader(`{"sample_rate_bytes": 4194304}`)
	resp, err := http.Post(ts.URL+"/debug/config/memory-profile", "application/json", body)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var result memoryProfileConfigResponse
	err = json.NewDecoder(resp.Body).Decode(&result)
	require.NoError(t, err)

	assert.Equal(t, originalRate, result.PreviousRate)
	assert.Equal(t, 4194304, result.CurrentRate)
	assert.Equal(t, 4194304, runtime.MemProfileRate)
}

func TestMemoryProfileConfigEndpoint_InvalidJSON(t *testing.T) {
	s := newTestServer(t)
	ts := httptest.NewServer(s)
	defer ts.Close()

	body := strings.NewReader(`not json`)
	resp, err := http.Post(ts.URL+"/debug/config/memory-profile", "application/json", body)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestMemoryProfileConfigEndpoint_InvalidRate(t *testing.T) {
	s := newTestServer(t)
	ts := httptest.NewServer(s)
	defer ts.Close()

	body := strings.NewReader(`{"sample_rate_bytes": 0}`)
	resp, err := http.Post(ts.URL+"/debug/config/memory-profile", "application/json", body)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestMemoryProfileConfigEndpoint_MethodNotAllowed(t *testing.T) {
	s := newTestServer(t)
	ts := httptest.NewServer(s)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/debug/config/memory-profile")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	assert.Equal(t, http.StatusMethodNotAllowed, resp.StatusCode)
}

func TestHeapEndpoint(t *testing.T) {
	s := newTestServer(t)
	ts := httptest.NewServer(s)
	defer ts.Close()

	// Allocate some memory to ensure profile has data.
	sink := make([]byte, 1024*1024)
	_ = sink

	resp, err := http.Get(ts.URL + "/debug/pprof/heap")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	// Should be parseable as a pprof profile.
	prof, err := profile.Parse(resp.Body)
	require.NoError(t, err)
	assert.NotNil(t, prof)
	assert.NotEmpty(t, prof.SampleType)
}

func TestAllocsEndpoint(t *testing.T) {
	s := newTestServer(t)
	ts := httptest.NewServer(s)
	defer ts.Close()

	// The allocs endpoint without ?seconds= returns cumulative allocations.
	resp, err := http.Get(ts.URL + "/debug/pprof/allocs")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	data, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.NotEmpty(t, data)
}
