# eBPF Implementation Guide

This document contains detailed implementation guidance for eBPF-based
application introspection described in RFD 013. It provides code examples, error
handling patterns, symbolization details, and operational procedures for
developers implementing eBPF collectors in Coral agents.

## Table of Contents

- [Capability Detection](#capability-detection)
- [Symbolization & Stack Unwinding](#symbolization--stack-unwinding)
- [DuckDB Query Patterns](#duckdb-query-patterns)
- [Failure Modes & Error Handling](#failure-modes--error-handling)

---

## Capability Detection

Agents must detect kernel capabilities at startup to determine which eBPF
collectors can run.

### Detection Implementation

```go
// Agent startup checks
func detectEbpfCapabilities() Capabilities {
caps := Capabilities{}
caps.KernelVersion = parseKernelVersion()
caps.HasBTF = checkBTFSupport()
caps.HasCapBPF = checkCapability(CAP_BPF)
caps.HasCapSysAdmin = checkCapability(CAP_SYS_ADMIN)

// Map collectors to capability requirements
if caps.KernelVersion >= KernelVersion{5, 8, 0} {
caps.SupportedCollectors = []string{"http_latency", "tcp_metrics", "cpu_profile", "syscall_stats"}
} else if caps.KernelVersion >= KernelVersion{4, 7, 0} {
caps.SupportedCollectors = []string{"http_latency", "tcp_metrics"}
log.Warn("Kernel < 5.8: limited eBPF support")
} else {
caps.SupportedCollectors = []string{}
log.Error("Kernel < 4.7: eBPF disabled")
}

return caps
}

func parseKernelVersion() KernelVersion {
var uname syscall.Utsname
if err := syscall.Uname(&uname); err != nil {
log.Error("Failed to get kernel version", "error", err)
return KernelVersion{0, 0, 0}
}

release := string(uname.Release[:])
// Parse "5.8.0-63-generic" → {5, 8, 0}
parts := strings.Split(release, ".")
if len(parts) < 3 {
return KernelVersion{0, 0, 0}
}

major, _ := strconv.Atoi(parts[0])
minor, _ := strconv.Atoi(parts[1])
patch, _ := strconv.Atoi(strings.Split(parts[2], "-")[0])

return KernelVersion{major, minor, patch}
}

func checkBTFSupport() bool {
// Check if /sys/kernel/btf/vmlinux exists
_, err := os.Stat("/sys/kernel/btf/vmlinux")
return err == nil
}

func checkCapability(cap uintptr) bool {
// Linux capability check
var data [2]capHeader
data[0].version = _LINUX_CAPABILITY_VERSION_3
data[0].pid = 0

if _, _, errno := syscall.Syscall(syscall.SYS_CAPGET, uintptr(unsafe.Pointer(&data[0])), uintptr(unsafe.Pointer(&data[1])), 0); errno != 0 {
return false
}

capMask := uint32(1 << (cap & 31))
if cap < 32 {
return (data[1].effective & capMask) != 0
} else {
return (data[1].effective2 & capMask) != 0
}
}
```

### Distro-Specific Detection

**Ubuntu 20.04+ (5.4 kernel)**:

- Partial BTF support (backported in some cases)
- Check for `/sys/kernel/btf/vmlinux` existence
- Some collectors may work with non-CO-RE bytecode

**RHEL 8+ (4.18 kernel)**:

- Red Hat backports eBPF features
- Kernel version alone is insufficient
- Check for specific features via `/proc/kallsyms`:
  ```go
  func checkRHELBackports() bool {
    // Check if bpf_probe_read_kernel exists (backported to RHEL 8)
    syms, _ := os.ReadFile("/proc/kallsyms")
    return bytes.Contains(syms, []byte("bpf_probe_read_kernel"))
  }
  ```

**Alpine Linux**:

- Stripped kernels may lack BTF
- Ship fallback non-CO-RE programs
- Detect Alpine: `cat /etc/os-release | grep Alpine`

---

## Symbolization & Stack Unwinding

eBPF provides raw instruction pointers; symbolization converts these to
human-readable function names.

### Symbolization Pipeline

```
eBPF → Raw IPs → /proc/<pid>/maps → ELF parsing → Symbol table → Function names
         ↓                                              ↓
    [IP1, IP2, IP3]                           [main, handleRequest, malloc]
```

### Language-Specific Approaches

#### Go Binaries

```go
func symbolizeGoStack(pid int, ips []uint64) ([]string, error) {
// Read /proc/<pid>/maps to find binary path
maps, err := readProcMaps(pid)
if err != nil {
return nil, err
}

// Find which binary each IP belongs to
symbols := make([]string, len(ips))
for i, ip := range ips {
mapping := findMapping(maps, ip)
if mapping == nil {
symbols[i] = fmt.Sprintf("0x%x", ip)
continue
}

// Open ELF binary
elf, err := elf.Open(mapping.Path)
if err != nil {
symbols[i] = fmt.Sprintf("%s+0x%x", mapping.Path, ip-mapping.Start)
continue
}
defer elf.Close()

// Read symbol table
syms, err := elf.Symbols()
if err != nil {
// Try DWARF if symbols stripped
symbols[i] = symbolizeWithDWARF(elf, ip-mapping.Start)
continue
}

// Find symbol containing this IP
offset := ip - mapping.Start
for _, sym := range syms {
if sym.Value <= offset && offset < sym.Value+sym.Size {
symbols[i] = sym.Name
break
}
}

if symbols[i] == "" {
symbols[i] = fmt.Sprintf("%s+0x%x", filepath.Base(mapping.Path), offset)
}
}

return symbols, nil
}

func symbolizeWithDWARF(elf *elf.File, offset uint64) string {
dwarfData, err := elf.DWARF()
if err != nil {
return fmt.Sprintf("0x%x", offset)
}

reader := dwarfData.Reader()
for {
entry, err := reader.Next()
if err != nil || entry == nil {
break
}

if entry.Tag == dwarf.TagSubprogram {
lowPC, ok := entry.Val(dwarf.AttrLowpc).(uint64)
if !ok {
continue
}
highPC, ok := entry.Val(dwarf.AttrHighpc).(uint64)
if !ok {
continue
}

if lowPC <= offset && offset < highPC {
name, _ := entry.Val(dwarf.AttrName).(string)
return name
}
}
}

return fmt.Sprintf("0x%x", offset)
}

func readProcMaps(pid int) ([]MemoryMapping, error) {
data, err := os.ReadFile(fmt.Sprintf("/proc/%d/maps", pid))
if err != nil {
return nil, err
}

var mappings []MemoryMapping
for _, line := range bytes.Split(data, []byte("\n")) {
// Parse: "7f8a2c000000-7f8a2c001000 r-xp 00000000 08:01 12345 /usr/bin/app"
fields := bytes.Fields(line)
if len(fields) < 6 {
continue
}

addrs := bytes.Split(fields[0], []byte("-"))
start, _ := strconv.ParseUint(string(addrs[0]), 16, 64)
end, _ := strconv.ParseUint(string(addrs[1]), 16, 64)

mappings = append(mappings, MemoryMapping{
Start: start,
End:   end,
Path:  string(fields[5]),
})
}

return mappings, nil
}
```

#### C/C++/Rust Binaries

```go
func symbolizeCppStack(pid int, ips []uint64) ([]string, error) {
// Similar to Go, but handle external debug symbols

symbols := make([]string, len(ips))
maps, _ := readProcMaps(pid)

for i, ip := range ips {
mapping := findMapping(maps, ip)
if mapping == nil {
symbols[i] = fmt.Sprintf("0x%x", ip)
continue
}

// Check for separate debug symbols
debugPath := findDebugSymbols(mapping.Path)
if debugPath != "" {
sym := symbolizeWithDebugFile(debugPath, ip-mapping.Start)
if sym != "" {
symbols[i] = sym
continue
}
}

// Fallback to binary itself
symbols[i] = symbolizeELF(mapping.Path, ip-mapping.Start)
}

return symbols, nil
}

func findDebugSymbols(binaryPath string) string {
// Standard locations for debug symbols
candidates := []string{
binaryPath + ".debug",
"/usr/lib/debug" + binaryPath + ".debug",
filepath.Dir(binaryPath) + "/.debug/" + filepath.Base(binaryPath) + ".debug",
}

for _, path := range candidates {
if _, err := os.Stat(path); err == nil {
return path
}
}

return ""
}
```

#### Python/Node.js/Ruby (Interpreted Languages)

```go
// Phase 1: Skip interpreted languages
// eBPF sees interpreter stacks, not Python/JS/Ruby frames

func symbolizeInterpretedStack(language string, pid int, ips []uint64) ([]string, error) {
switch language {
case "python":
// Future: Integrate py-spy logic
// Walk Python frame objects in interpreter heap
return nil, errors.New("Python symbolization not yet implemented")

case "node":
// Future: Integrate V8 stack walking
return nil, errors.New("Node.js symbolization not yet implemented")

default:
// Show interpreter frames only
return symbolizeGoStack(pid, ips) // Fallback to native symbolization
}
}

// Phase 2 implementation (reference):
//
// Python: Use libpython internals to walk PyFrameObject chain
// Node.js: Use V8 API to walk JavaScript stack frames
// Ruby: Use RubyVM internals
//
// All require language-specific unwinders and are complex.
```

### Container Environments

```go
func symbolizeContainerStack(containerID string, pid int, ips []uint64) ([]string, error) {
// Get container root filesystem via Docker/containerd API
runtime := detectContainerRuntime()
rootfs, err := runtime.GetRootFS(containerID)
if err != nil {
return nil, err
}

// Rewrite /proc/<pid>/maps paths to container filesystem
maps, err := readProcMaps(pid)
if err != nil {
return nil, err
}

for i := range maps {
// /usr/bin/app in container → /var/lib/docker/overlay2/.../usr/bin/app on host
maps[i].Path = filepath.Join(rootfs, maps[i].Path)
}

// Symbolize using container binaries
return symbolizeWithMappings(maps, ips)
}

type ContainerRuntime interface {
GetRootFS(containerID string) (string, error)
}

type DockerRuntime struct{}

func (d *DockerRuntime) GetRootFS(containerID string) (string, error) {
// Use Docker API: GET /containers/{id}/json
// Extract GraphDriver.Data["MergedDir"]
resp, err := http.Get(fmt.Sprintf("http://localhost/containers/%s/json", containerID))
if err != nil {
return "", err
}
defer resp.Body.Close()

var data struct {
GraphDriver struct {
Data map[string]string
}
}
json.NewDecoder(resp.Body).Decode(&data)

return data.GraphDriver.Data["MergedDir"], nil
}
```

### Failure Handling

```go
func symbolizeWithFallback(pid int, ips []uint64) []string {
symbols := make([]string, len(ips))

for i, ip := range ips {
// Try full symbolization
sym, err := symbolizeFull(pid, ip)
if err == nil && sym != "" {
symbols[i] = sym
continue
}

// Fallback 1: Show module + offset
mapping := findMapping(procMaps[pid], ip)
if mapping != nil {
symbols[i] = fmt.Sprintf("%s+0x%x", filepath.Base(mapping.Path), ip-mapping.Start)
continue
}

// Fallback 2: Raw address
symbols[i] = fmt.Sprintf("0x%x", ip)
}

return symbols
}
```

---

## DuckDB Query Patterns

### Retention and Cleanup

```sql
-- Automated retention (run daily via colony scheduler)
DELETE
FROM ebpf_http_latency
WHERE timestamp < now() - INTERVAL '7 days';
DELETE
FROM ebpf_tcp_metrics
WHERE timestamp < now() - INTERVAL '30 days';
DELETE
FROM ebpf_cpu_profile
WHERE timestamp < now() - INTERVAL '24 hours';
DELETE
FROM ebpf_syscall_stats
WHERE timestamp < now() - INTERVAL '3 days';
```

### Downsampling

```sql
-- Aggregate to hourly after 24h
CREATE TABLE ebpf_http_latency_hourly AS
SELECT time_bucket('1 hour', timestamp) AS hour,
  service_name,
  http_route,
  http_status,
  bucket_ms,
  SUM(count) AS count
FROM ebpf_http_latency
WHERE timestamp < now() - INTERVAL '24 hours'
GROUP BY hour, service_name, http_route, http_status, bucket_ms;

-- Delete detailed data after aggregation
DELETE
FROM ebpf_http_latency
WHERE timestamp < now() - INTERVAL '24 hours';
```

### Anomaly Detection

```sql
-- Syscall anomaly detection
CREATE VIEW ebpf_syscall_anomalies AS
SELECT service_name,
       syscall_name,
       call_count,
       AVG(call_count) OVER (
    PARTITION BY service_name, syscall_name
    ORDER BY timestamp
    ROWS BETWEEN 12 PRECEDING AND 1 PRECEDING
  ) AS avg_count, CASE
                                                  WHEN call_count > 3 * avg_count
                                                      THEN 'SPIKE'
                                                  ELSE 'NORMAL'
    END AS anomaly
FROM ebpf_syscall_stats
WHERE
    timestamp > now() - INTERVAL '1 hour';
```

### Flamegraph Generation

```sql
-- Generate flamegraph data (folded stacks)
SELECT service_name,
       array_to_string(stack, ';') AS stack_folded,
       SUM(sample_count)           AS samples
FROM ebpf_cpu_profile
WHERE service_name = 'payments-api'
  AND timestamp
    > now() - INTERVAL '5 minutes'
GROUP BY service_name, stack
ORDER BY samples DESC;
```

### Top Functions

```sql
-- Materialized view for top functions
CREATE
MATERIALIZED VIEW ebpf_cpu_hotspots AS
SELECT service_name,
       time_bucket('1 hour', timestamp) AS hour,
  unnest(stack) AS function_name,
  SUM(sample_count) AS total_samples
FROM ebpf_cpu_profile
WHERE timestamp > now() - INTERVAL '7 days'
GROUP BY service_name, hour, function_name
ORDER BY total_samples DESC;

-- Query top CPU hotspots
SELECT function_name,
       SUM(total_samples)                                                 AS samples,
       100.0 * SUM(total_samples) / (SELECT SUM(total_samples)
                                     FROM ebpf_cpu_hotspots
                                     WHERE service_name = 'payments-api') AS percent
FROM ebpf_cpu_hotspots
WHERE service_name = 'payments-api'
          AND hour > now() - INTERVAL '1 hour'
GROUP BY function_name
ORDER BY samples DESC
    LIMIT 20;
```

### Latency Percentiles

```sql
-- Calculate P50, P95, P99 from histograms
WITH histogram AS (SELECT bucket_ms,
                          SUM(count) AS count
FROM ebpf_http_latency
WHERE
    service_name = 'payments-api'
  AND http_route = '/validate'
  AND timestamp
    > now() - INTERVAL '1 hour'
GROUP BY bucket_ms
ORDER BY bucket_ms
    ),
    cumulative AS (
SELECT
    bucket_ms, count, SUM (count) OVER (ORDER BY bucket_ms) AS cumulative_count, SUM (count) OVER () AS total_count
FROM histogram
    )
SELECT MAX(CASE
               WHEN cumulative_count >= total_count * 0.50
                   THEN bucket_ms END) AS p50_ms,
       MAX(CASE
               WHEN cumulative_count >= total_count * 0.95
                   THEN bucket_ms END) AS p95_ms,
       MAX(CASE
               WHEN cumulative_count >= total_count * 0.99
                   THEN bucket_ms END) AS p99_ms
FROM cumulative;
```

---

## Failure Modes & Error Handling

Comprehensive error handling ensures eBPF failures don't block operations.

### Verifier Rejection

**Scenario**: BPF program invalid (memory access violation, unbounded loop,
etc.).

**Error**:

```
ERROR: eBPF program rejected by kernel verifier
Reason: invalid memory access at instruction 42
Verifier log:
  0: (bf) r6 = r1
  1: (79) r7 = *(u64 *)(r6 +0)
  ...
  42: (79) r2 = *(u64 *)(r7 +8)  ← invalid memory access
Action: Falling back to packet capture only
```

**Handling**:

```go
func loadEbpfProgram(path string) error {
prog, err := ebpf.LoadProgram(path)
if err != nil {
if strings.Contains(err.Error(), "verifier") {
log.Error("eBPF program rejected by kernel verifier",
"path", path,
"error", err)

// Report to colony for tracking
reportVerifierFailure(path, err)

// Disable this collector
disableCollector(getCollectorName(path))

return fmt.Errorf("verifier rejected program: %w", err)
}
return err
}

return nil
}
```

### Probe Attachment Failure

**Scenario**: Symbol not found, permission denied, kprobe API change.

**Error**:

```
WARN: Failed to attach kprobe to tcp_sendmsg
Kernel version: 4.9.0 (kprobe API changed)
Action: Retrying with fallback probe point...
```

**Handling**:

```go
func attachKprobe(symbol string) error {
// Try primary symbol
err := tryAttachKprobe(symbol)
if err == nil {
return nil
}

log.Warn("Failed to attach kprobe", "symbol", symbol, "error", err)

// Fallback probe points
fallbacks := []string{
"__" + symbol,      // Older kernels prefix with __
symbol + ".isra.0", // Compiler optimization variants
symbol + ".part.0",
}

for _, fallback := range fallbacks {
log.Info("Trying fallback kprobe", "symbol", fallback)
if err := tryAttachKprobe(fallback); err == nil {
log.Info("Attached to fallback kprobe", "symbol", fallback)
return nil
}
}

// All attempts failed
log.Error("Failed to attach kprobe to any variant",
"symbol", symbol,
"fallbacks", fallbacks)

return fmt.Errorf("kprobe attachment failed for %s", symbol)
}

func tryAttachKprobe(symbol string) error {
// Retry with exponential backoff (3 attempts)
backoff := time.Second
for i := 0; i < 3; i++ {
if err := kprobe.Attach(symbol); err == nil {
return nil
}
time.Sleep(backoff)
backoff *= 2
}

return errors.New("attachment failed after retries")
}
```

### Symbolization Failure

**Scenario**: Stripped binary, missing debug symbols.

**Output**:

```
INFO: Symbols unavailable for /usr/bin/app
Stack trace:
  /usr/bin/app+0x12af0
  /usr/bin/app+0x8c20
  libc.so.6+0x29d90 (__libc_start_main)
```

**Handling**:

```go
func formatStack(pid int, ips []uint64) []string {
symbols, err := symbolizeStack(pid, ips)
if err != nil {
log.Warn("Symbolization failed", "pid", pid, "error", err)

// Suggest manual symbol upload
fmt.Fprintf(os.Stderr,
"Symbols unavailable for process %d\n"+
"Consider: coral symbols upload <service> --binary /path/to/debug/binary\n",
pid)
}

// Mix symbolized frames with addresses
formatted := make([]string, len(ips))
for i, ip := range ips {
if symbols != nil && symbols[i] != "" {
formatted[i] = symbols[i]
} else {
// Show module + offset
mapping := findMapping(procMaps[pid], ip)
if mapping != nil {
formatted[i] = fmt.Sprintf("%s+0x%x",
filepath.Base(mapping.Path),
ip-mapping.Start)
} else {
formatted[i] = fmt.Sprintf("0x%x", ip)
}
}
}

return formatted
}
```

### Event Buffer Overflow

**Scenario**: eBPF producing events faster than userspace can consume.

**Error**:

```
WARN: eBPF event buffer full (65536 events)
Dropped: 1240 events in last 1s
Action: Reducing sampling rate from 100Hz to 50Hz
```

**Handling**:

```go
func monitorEventBuffer(collector *Collector) {
ticker := time.NewTicker(1 * time.Second)
defer ticker.Stop()

for range ticker.C {
stats := collector.GetStats()

if stats.DroppedEvents > 0 {
dropRate := float64(stats.DroppedEvents) / float64(stats.TotalEvents)

log.Warn("eBPF event buffer overflow",
"collector", collector.Name,
"dropped", stats.DroppedEvents,
"drop_rate", fmt.Sprintf("%.1f%%", dropRate*100))

// Increment metric
metrics.EbpfEventsDropped.Add(stats.DroppedEvents)

// Dynamically reduce sampling rate
if dropRate > 0.05 { // >5% drop rate
currentRate := collector.GetSamplingRate()
newRate := currentRate / 2

log.Warn("Reducing sampling rate",
"collector", collector.Name,
"old_rate", currentRate,
"new_rate", newRate)

collector.SetSamplingRate(newRate)
}

// Alert if severe
if dropRate > 0.20 { // >20% drop rate
alertOps(fmt.Sprintf("eBPF %s dropping >20%% events", collector.Name))
}
}
}
}
```

### Resource Quota Exceeded

**Scenario**: eBPF collectors exceeding CPU or memory quotas.

**Error**:

```
ERROR: eBPF collectors exceeding CPU quota (8% > 5%)
Action: Disabling lowest-priority collector (syscall_stats)
Remaining: http_latency, tcp_metrics
```

**Handling**:

```go
func enforceResourceQuotas(collectors []*Collector, limits ResourceLimits) {
// Measure current usage
cpuUsage := measureCPUUsage(collectors)
memUsage := measureMemoryUsage(collectors)

if cpuUsage > limits.MaxCPUPercent {
log.Error("eBPF CPU quota exceeded",
"usage", fmt.Sprintf("%.1f%%", cpuUsage),
"limit", fmt.Sprintf("%.1f%%", limits.MaxCPUPercent))

// Disable lowest priority collector
disableLowestPriority(collectors)
}

if memUsage > limits.MaxMemoryMB {
log.Error("eBPF memory quota exceeded",
"usage", fmt.Sprintf("%d MB", memUsage),
"limit", fmt.Sprintf("%d MB", limits.MaxMemoryMB))

disableLowestPriority(collectors)
}
}

func disableLowestPriority(collectors []*Collector) {
// Priority order: continuous > on-demand > user-triggered
sort.Slice(collectors, func (i, j int) bool {
return collectors[i].Priority < collectors[j].Priority
})

for i := len(collectors) - 1; i >= 0; i-- {
if collectors[i].Running {
log.Warn("Disabling collector to meet quota",
"collector", collectors[i].Name,
"priority", collectors[i].Priority)

collectors[i].Stop()

// Re-check quotas
if checkQuotasMet(collectors) {
return
}
}
}
}
```

### Kernel Version Incompatibility

**Scenario**: Kernel too old for requested collector.

**Error**:

```
WARN: Kernel 4.4.0 detected; eBPF support limited
Supported collectors: (none)
Recommendation: Upgrade to kernel 5.8+ for full eBPF support
```

**Handling**:

```go
func validateCollectorSupport(collector string, caps Capabilities) error {
requirements := map[string]KernelVersion{
"http_latency":  {4, 7, 0},
"tcp_metrics":   {4, 7, 0},
"cpu_profile":   {5, 2, 0}, // Needs BTF
"syscall_stats": {4, 1, 0},
}

required, exists := requirements[collector]
if !exists {
return fmt.Errorf("unknown collector: %s", collector)
}

if caps.KernelVersion.LessThan(required) {
return fmt.Errorf(
"collector %s requires kernel %s (current: %s)\n"+
"Recommendation: Upgrade to kernel 5.8+ for full eBPF support",
collector,
required.String(),
caps.KernelVersion.String())
}

return nil
}

func (kv KernelVersion) LessThan(other KernelVersion) bool {
if kv.Major != other.Major {
return kv.Major < other.Major
}
if kv.Minor != other.Minor {
return kv.Minor < other.Minor
}
return kv.Patch < other.Patch
}

func (kv KernelVersion) String() string {
return fmt.Sprintf("%d.%d.%d", kv.Major, kv.Minor, kv.Patch)
}
```

### Graceful Degradation

When eBPF fails, fall back to alternative data sources:

```go
func startTapSession(service string, options TapOptions) (*TapSession, error) {
session := &TapSession{
Service: service,
DataSources: []string{},
}

// Try eBPF collectors first
if options.HTTPLatency {
if err := session.StartEbpfCollector("http_latency"); err != nil {
log.Warn("eBPF http_latency unavailable, falling back to packet analysis",
"error", err)
session.DataSources = append(session.DataSources, "packets")
} else {
session.DataSources = append(session.DataSources, "http-latency (eBPF)")
}
}

if options.CPUProfile {
if err := session.StartEbpfCollector("cpu_profile"); err != nil {
log.Warn("eBPF cpu_profile unavailable, falling back to pprof",
"error", err)
session.DataSources = append(session.DataSources, "pprof")
} else {
session.DataSources = append(session.DataSources, "cpu-profile (eBPF)")
}
}

// Always works: packet capture
session.StartPacketCapture()

fmt.Printf("📊 Data sources: %s\n", strings.Join(session.DataSources, ", "))

return session, nil
}
```
