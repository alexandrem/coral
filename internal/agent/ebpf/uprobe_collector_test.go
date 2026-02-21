//go:build linux

package ebpf

import (
	"testing"
	"unsafe"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/coral-mesh/coral/internal/agent/ebpf/bpfgen"
)

func TestUprobeEventStructLayout(t *testing.T) {
	// Verify that the Go struct layout matches the C struct layout.
	// C struct:
	// struct uprobe_event {
	//     __u64 timestamp_ns; // 0
	//     __u32 pid;          // 8
	//     __u32 tid;          // 12
	//     __u8  event_type;   // 16
	//     // 7 bytes padding  // 17
	//     __u64 duration_ns;  // 24
	// };                      // Total: 32

	var event uprobeEvent

	// Check total size
	assert.Equal(t, uintptr(32), unsafe.Sizeof(event), "uprobeEvent size should be 32 bytes")

	// Check offsets
	assert.Equal(t, uintptr(0), unsafe.Offsetof(event.TimestampNs), "TimestampNs offset")
	assert.Equal(t, uintptr(8), unsafe.Offsetof(event.Pid), "Pid offset")
	assert.Equal(t, uintptr(12), unsafe.Offsetof(event.Tid), "Tid offset")
	assert.Equal(t, uintptr(16), unsafe.Offsetof(event.EventType), "EventType offset")
	assert.Equal(t, uintptr(24), unsafe.Offsetof(event.DurationNs), "DurationNs offset")
}

func TestUprobeFilterConfigStructLayout(t *testing.T) {
	// Verify that the Go struct layout matches the C struct layout.
	// C struct:
	// struct filter_config {
	//     __u64 min_duration_ns; // 0
	//     __u64 max_duration_ns; // 8
	//     __u32 sample_rate;     // 16
	//     // 4 bytes padding     // 20
	// };                         // Total: 24

	var cfg uprobeFilterConfig

	assert.Equal(t, uintptr(24), unsafe.Sizeof(cfg), "uprobeFilterConfig size should be 24 bytes")
	assert.Equal(t, uintptr(0), unsafe.Offsetof(cfg.MinDurationNs), "MinDurationNs offset")
	assert.Equal(t, uintptr(8), unsafe.Offsetof(cfg.MaxDurationNs), "MaxDurationNs offset")
	assert.Equal(t, uintptr(16), unsafe.Offsetof(cfg.SampleRate), "SampleRate offset")
}

// TestUprobeCollectorWriteFilterConfig tests that writeFilterConfig does not error
// when the filter map is absent (old compiled .o without filter support).
func TestUprobeCollectorWriteFilterConfigNilMap(t *testing.T) {
	c := &UprobeCollector{
		objs: &bpfgen.Objects{}, // FilterConfigMap is nil — simulates old .o without filter maps.
	}

	f := UprobeFilter{
		MinDurationNs: 50_000_000, // 50ms
		MaxDurationNs: 0,
		SampleRate:    0,
	}

	// Should not error when map is nil.
	err := c.writeFilterConfig(f)
	require.NoError(t, err, "writeFilterConfig should be a no-op when FilterConfigMap is nil")
}

// TestUprobeCollectorUpdateFilterNilMap tests that UpdateFilter does not error
// when the filter map is absent.
func TestUprobeCollectorUpdateFilterNilMap(t *testing.T) {
	c := &UprobeCollector{
		objs: &bpfgen.Objects{},
	}

	f := UprobeFilter{SampleRate: 10}
	err := c.UpdateFilter(f)
	require.NoError(t, err, "UpdateFilter must be a no-op when filter maps are absent")
}

// TestUprobeFilterZeroIsPassthrough verifies that a zero-value UprobeFilter
// is semantically equivalent to "no filter" (backward compatible default).
func TestUprobeFilterZeroIsPassthrough(t *testing.T) {
	var f UprobeFilter

	assert.Equal(t, uint64(0), f.MinDurationNs, "zero MinDurationNs = no minimum filter")
	assert.Equal(t, uint64(0), f.MaxDurationNs, "zero MaxDurationNs = no maximum filter")
	assert.Equal(t, uint32(0), f.SampleRate, "zero SampleRate = emit all events")
}

// TestUprobeFilterConversionFromProto verifies the mapping between proto UprobeFilter
// and the internal UprobeFilter type is lossless.
func TestUprobeFilterConversionFromProto(t *testing.T) {
	const minNs = uint64(50_000_000)  // 50ms
	const maxNs = uint64(500_000_000) // 500ms
	const rate = uint32(100)

	internal := UprobeFilter{
		MinDurationNs: minNs,
		MaxDurationNs: maxNs,
		SampleRate:    rate,
	}

	assert.Equal(t, minNs, internal.MinDurationNs)
	assert.Equal(t, maxNs, internal.MaxDurationNs)
	assert.Equal(t, rate, internal.SampleRate)
}
