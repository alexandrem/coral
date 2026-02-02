package debug

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/pprof/profile"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// buildTestProfile creates a synthetic pprof profile with allocation samples.
func buildTestProfile(t *testing.T, samples []struct {
	funcNames    []string
	allocBytes   int64
	allocObjects int64
}) []byte {
	t.Helper()

	prof := &profile.Profile{
		SampleType: []*profile.ValueType{
			{Type: "alloc_objects", Unit: "count"},
			{Type: "alloc_space", Unit: "bytes"},
		},
	}

	funcID := uint64(1)
	locID := uint64(1)
	funcs := make(map[string]*profile.Function)

	for _, s := range samples {
		var locs []*profile.Location
		for _, name := range s.funcNames {
			fn, ok := funcs[name]
			if !ok {
				fn = &profile.Function{ID: funcID, Name: name}
				prof.Function = append(prof.Function, fn)
				funcs[name] = fn
				funcID++
			}
			loc := &profile.Location{
				ID:   locID,
				Line: []profile.Line{{Function: fn}},
			}
			prof.Location = append(prof.Location, loc)
			locs = append(locs, loc)
			locID++
		}
		prof.Sample = append(prof.Sample, &profile.Sample{
			Location: locs,
			Value:    []int64{s.allocObjects, s.allocBytes},
		})
	}

	var buf bytes.Buffer
	require.NoError(t, prof.Write(&buf))
	return buf.Bytes()
}

func mockSDKServer(t *testing.T, pprofData []byte, memstats map[string]interface{}) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/debug/pprof/allocs" || r.URL.Path == "/debug/pprof/heap":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(pprofData)
		case r.URL.Path == "/debug/memstats":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(memstats)
		default:
			http.NotFound(w, r)
		}
	}))
}

func TestCollectMemoryProfile_Success(t *testing.T) {
	pprofData := buildTestProfile(t, []struct {
		funcNames    []string
		allocBytes   int64
		allocObjects int64
	}{
		{funcNames: []string{"main.ProcessOrder", "json.Marshal"}, allocBytes: 1024000, allocObjects: 500},
		{funcNames: []string{"main.HandleRequest"}, allocBytes: 512000, allocObjects: 100},
	})

	memstats := map[string]interface{}{
		"alloc_bytes":       float64(2048000),
		"total_alloc_bytes": float64(10000000),
		"sys_bytes":         float64(50000000),
		"num_gc":            float64(42),
	}

	srv := mockSDKServer(t, pprofData, memstats)
	defer srv.Close()

	addr := srv.Listener.Addr().String()

	result, err := CollectMemoryProfile(addr, 1, zerolog.Nop())
	require.NoError(t, err)
	require.NotNil(t, result)

	assert.NotEmpty(t, result.Samples)
	assert.Equal(t, int64(2048000), result.Stats.AllocBytes)
	assert.Equal(t, int64(10000000), result.Stats.TotalAllocBytes)
	assert.Equal(t, int64(42), result.Stats.NumGC)
	assert.NotEmpty(t, result.TopFunctions)
}

func TestCollectMemoryProfile_SDKError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "internal error", http.StatusInternalServerError)
	}))
	defer srv.Close()

	_, err := CollectMemoryProfile(srv.Listener.Addr().String(), 1, zerolog.Nop())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "500")
}

func TestCollectMemoryProfile_InvalidPprof(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/debug/pprof/allocs" {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("not a valid pprof"))
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	_, err := CollectMemoryProfile(srv.Listener.Addr().String(), 1, zerolog.Nop())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parse")
}

func TestCollectHeapSnapshot_Success(t *testing.T) {
	pprofData := buildTestProfile(t, []struct {
		funcNames    []string
		allocBytes   int64
		allocObjects int64
	}{
		{funcNames: []string{"cache.Store"}, allocBytes: 2048000, allocObjects: 1000},
	})

	memstats := map[string]interface{}{
		"alloc_bytes":       float64(2048000),
		"total_alloc_bytes": float64(5000000),
		"sys_bytes":         float64(20000000),
		"num_gc":            float64(10),
	}

	srv := mockSDKServer(t, pprofData, memstats)
	defer srv.Close()

	result, err := CollectHeapSnapshot(srv.Listener.Addr().String(), zerolog.Nop())
	require.NoError(t, err)
	require.NotNil(t, result)

	assert.Len(t, result.Samples, 1)
	assert.Equal(t, int64(2048000), result.Samples[0].AllocBytes)
	assert.Equal(t, "cache.Store", result.Samples[0].FrameNames[0])
}

func TestCollectHeapSnapshot_GzipResponse(t *testing.T) {
	pprofData := buildTestProfile(t, []struct {
		funcNames    []string
		allocBytes   int64
		allocObjects int64
	}{
		{funcNames: []string{"main.Alloc"}, allocBytes: 100, allocObjects: 1},
	})

	// Gzip the pprof data.
	var gzBuf bytes.Buffer
	gzw := gzip.NewWriter(&gzBuf)
	_, err := gzw.Write(pprofData)
	require.NoError(t, err)
	require.NoError(t, gzw.Close())

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/debug/pprof/heap":
			w.Header().Set("Content-Encoding", "gzip")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(gzBuf.Bytes())
		case "/debug/memstats":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"alloc_bytes":0,"total_alloc_bytes":0,"sys_bytes":0,"num_gc":0}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	result, err := CollectHeapSnapshot(srv.Listener.Addr().String(), zerolog.Nop())
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Len(t, result.Samples, 1)
}

func TestCollectHeapSnapshot_MemstatsFails(t *testing.T) {
	pprofData := buildTestProfile(t, []struct {
		funcNames    []string
		allocBytes   int64
		allocObjects int64
	}{
		{funcNames: []string{"main.Alloc"}, allocBytes: 100, allocObjects: 1},
	})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/debug/pprof/heap":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(pprofData)
		case "/debug/memstats":
			http.Error(w, "unavailable", http.StatusServiceUnavailable)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	logger := zerolog.Nop()
	result, err := CollectHeapSnapshot(srv.Listener.Addr().String(), logger)
	require.NoError(t, err)
	require.NotNil(t, result)

	// Stats should be zero but snapshot still succeeds.
	assert.Equal(t, int64(0), result.Stats.AllocBytes)
	assert.Len(t, result.Samples, 1)
}

func TestParseProfile_TopFunctions(t *testing.T) {
	prof := &profile.Profile{
		SampleType: []*profile.ValueType{
			{Type: "alloc_objects", Unit: "count"},
			{Type: "alloc_space", Unit: "bytes"},
		},
	}

	// Add 3 functions with different allocation weights.
	funcs := []*profile.Function{
		{ID: 1, Name: "big.Alloc"},
		{ID: 2, Name: "medium.Alloc"},
		{ID: 3, Name: "small.Alloc"},
	}
	prof.Function = funcs

	locs := []*profile.Location{
		{ID: 1, Line: []profile.Line{{Function: funcs[0]}}},
		{ID: 2, Line: []profile.Line{{Function: funcs[1]}}},
		{ID: 3, Line: []profile.Line{{Function: funcs[2]}}},
	}
	prof.Location = locs

	prof.Sample = []*profile.Sample{
		{Location: []*profile.Location{locs[0]}, Value: []int64{1000, 5000000}},
		{Location: []*profile.Location{locs[1]}, Value: []int64{500, 2000000}},
		{Location: []*profile.Location{locs[2]}, Value: []int64{100, 500000}},
	}

	result := parseProfile(prof, nil)

	require.Len(t, result.TopFunctions, 3)
	// Sorted by bytes descending.
	assert.Equal(t, "big.Alloc", result.TopFunctions[0].Function)
	assert.Equal(t, int64(5000000), result.TopFunctions[0].Bytes)
	assert.Equal(t, "medium.Alloc", result.TopFunctions[1].Function)
	assert.Equal(t, "small.Alloc", result.TopFunctions[2].Function)

	// Percentages should add up to ~100.
	totalPct := 0.0
	for _, f := range result.TopFunctions {
		totalPct += f.Pct
	}
	assert.InDelta(t, 100.0, totalPct, 0.1)
}

func TestParseProfile_EmptySamples(t *testing.T) {
	prof := &profile.Profile{
		SampleType: []*profile.ValueType{
			{Type: "alloc_objects", Unit: "count"},
			{Type: "alloc_space", Unit: "bytes"},
		},
	}

	result := parseProfile(prof, nil)
	assert.Empty(t, result.Samples)
	assert.Empty(t, result.TopFunctions)
}

func TestParseProfile_TruncatesTop20(t *testing.T) {
	prof := &profile.Profile{
		SampleType: []*profile.ValueType{
			{Type: "alloc_objects", Unit: "count"},
			{Type: "alloc_space", Unit: "bytes"},
		},
	}

	// Create 25 distinct functions.
	for i := 0; i < 25; i++ {
		fn := &profile.Function{ID: uint64(i + 1), Name: "func" + string(rune('A'+i))}
		prof.Function = append(prof.Function, fn)
		loc := &profile.Location{
			ID:   uint64(i + 1),
			Line: []profile.Line{{Function: fn}},
		}
		prof.Location = append(prof.Location, loc)
		prof.Sample = append(prof.Sample, &profile.Sample{
			Location: []*profile.Location{loc},
			Value:    []int64{int64(100 - i), int64((100 - i) * 1000)},
		})
	}

	result := parseProfile(prof, nil)
	assert.Len(t, result.TopFunctions, 20)
}

func TestParseProfile_TopTypes(t *testing.T) {
	prof := &profile.Profile{
		SampleType: []*profile.ValueType{
			{Type: "alloc_objects", Unit: "count"},
			{Type: "alloc_space", Unit: "bytes"},
		},
	}

	// Create samples with runtime allocator functions as leaf frames.
	runtimeFuncs := []*profile.Function{
		{ID: 1, Name: "runtime.makeslice"},
		{ID: 2, Name: "runtime.makemap_small"},
		{ID: 3, Name: "runtime.newobject"},
		{ID: 4, Name: "runtime.concatstrings"},
	}
	prof.Function = runtimeFuncs

	for i, fn := range runtimeFuncs {
		loc := &profile.Location{
			ID:   uint64(i + 1),
			Line: []profile.Line{{Function: fn}},
		}
		prof.Location = append(prof.Location, loc)
		prof.Sample = append(prof.Sample, &profile.Sample{
			Location: []*profile.Location{loc},
			Value:    []int64{int64((4 - i) * 100), int64((4 - i) * 1000000)},
		})
	}

	result := parseProfile(prof, nil)

	require.NotEmpty(t, result.TopTypes)

	// Verify type classification.
	typeMap := make(map[string]bool)
	for _, tt := range result.TopTypes {
		typeMap[tt.TypeName] = true
	}
	assert.True(t, typeMap["slice"], "expected 'slice' type from runtime.makeslice")
	assert.True(t, typeMap["map"], "expected 'map' type from runtime.makemap_small")
	assert.True(t, typeMap["object"], "expected 'object' type from runtime.newobject")
	assert.True(t, typeMap["string"], "expected 'string' type from runtime.concatstrings")

	// Sorted by bytes descending.
	assert.Equal(t, "slice", result.TopTypes[0].TypeName)
}

func TestClassifyAllocType(t *testing.T) {
	tests := []struct {
		funcName string
		expected string
	}{
		{"runtime.makeslice", "slice"},
		{"runtime.growslice", "slice"},
		{"runtime.makemap", "map"},
		{"runtime.makemap_small", "map"},
		{"runtime.mapassign_faststr", "map"},
		{"runtime.newobject", "object"},
		{"runtime.mallocgc", "object"},
		{"runtime.concatstrings", "string"},
		{"runtime.slicebytetostring", "string"},
		{"runtime.makechan", "channel"},
		{"myapp.ProcessOrder", "myapp.ProcessOrder"},
	}

	for _, tt := range tests {
		t.Run(tt.funcName, func(t *testing.T) {
			assert.Equal(t, tt.expected, classifyAllocType(tt.funcName))
		})
	}
}
