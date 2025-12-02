package ebpf

import (
	"testing"
	"unsafe"

	"github.com/stretchr/testify/assert"
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
