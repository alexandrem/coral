---
rfd: "008"
title: "Privilege Separation for TUN Device Creation"
state: "partial"
breaking_changes: false
testing_required: true
database_changes: false
api_changes: true
dependencies: [ "007" ]
database_migrations: [ ]
areas: [ "networking", "security", "cli", "configuration" ]
---

# RFD 008 - Privilege Separation for TUN Device Creation

**Status:** ✅ Implemented (Modified Approach)

## Implementation Status (December 2025)

### ✅ Completed

- **Config file ownership preservation** - Detects `SUDO_USER` and preserves ownership
- **Helper subprocess infrastructure** - Complete FD passing via SCM_RIGHTS
- **Automatic helper fallback** - Integrated into `CreateTUN()` (darwin/linux)
- **FD lifecycle management** - Explicit FD duplication for proper ownership transfer
- **Capability detection** - Runtime detection of Linux capabilities with graceful degradation

### ⚠️ Architecture Decision: No Privilege Dropping

**Original RFD proposal:** Drop privileges after TUN device creation.

**Implemented approach:** Maintain privileges throughout lifetime.

**Rationale:**
1. **Colony:** Requires `CAP_NET_ADMIN` continuously for dynamic route management
   - Routes added as agents connect: `route add -host <agent-ip> -interface utun11`
   - Cannot drop privileges without breaking mesh topology updates

2. **Agent:** Requires multiple capabilities continuously for eBPF operations
   - `CAP_SYS_ADMIN`, `CAP_SYS_PTRACE`, `CAP_BPF` needed throughout for Beyla telemetry
   - eBPF programs run continuously, not just at startup

**See:** `docs/PRIVILEGE.md` for detailed architecture explanation.

### Helper Subprocess Usage

**Current:**
- Colony/agent use direct TUN creation (running as root/with caps)
- Helper serves as fallback if direct creation fails
- Infrastructure ready for future use cases (CLI tools, embedded apps)

## Summary

This RFD addresses the privilege separation issue where creating WireGuard TUN
devices requires elevated privileges (root or capabilities), but configuration
files should remain owned by the regular user. Running `sudo coral init` or
`sudo coral colony start` previously created root-owned config files in
`~/.coral/`, causing permission errors when subsequent commands ran as a regular
user.

The solution implements an internal subprocess-based privilege escalation
mechanism within the single `coral` binary, allowing TUN device creation with
minimal privilege exposure while keeping all configuration and state files
user-owned.

**Note:** Phase 1 (file ownership preservation and error handling) is complete
and solves the immediate DevEx issue. The subprocess helper is implemented but
not yet auto-invoked.

## Problem

### Current Behavior

WireGuard TUN device creation requires elevated privileges:

- **Linux**: Requires `CAP_NET_ADMIN` capability or root access
- **macOS**: Requires root access or specific entitlements

When users run `coral colony start` with `sudo`, the entire process runs as
root, resulting in:

1. **Config files owned by root**:
   ```
   $ sudo coral init
   $ ls -la ~/.coral/
   drwx------  2 root  root  4096 config.yaml
   drwx------  2 root  root  4096 colonies/
   -rw-------  1 root  root   512 colonies/abc123.yaml
   ```

2. **Permission errors on subsequent runs**:
   ```
   $ coral colony status
   Error: open ~/.coral/colonies/abc123.yaml: permission denied
   ```

3. **Workarounds required**:
    - Users must run all commands with `sudo` (security risk)
    - Or manually `chown` config files back (tedious, error-prone)
    - Or use `sudo -E` to preserve environment (still creates root-owned files)

### Security Concerns

Running the entire `coral` binary as root violates the principle of least
privilege:

- Log files may be inaccessible to regular user
- RPC handlers run with unnecessary privileges
- Database operations execute as root
- Increased attack surface if vulnerabilities exist

### User Experience Impact

Current workflow forces users into a privilege escalation pattern:

```bash
$ coral init                    # Fails - no permission to create TUN
$ sudo coral init              # Works but creates root-owned configs
$ coral colony status          # Fails - can't read root-owned configs
$ sudo coral colony status     # Must use sudo forever
```

## Solution

### Overview

Implement privilege separation using an internal subprocess command within the
single `coral` binary. The subprocess handles only TUN device creation, passing
the file descriptor back to the parent process via Unix domain sockets.

### Architecture

```
┌─────────────────────────────────────────────────────────────┐
│ coral colony start (runs as regular user)                   │
│                                                              │
│ 1. Read config from ~/.coral/ (user-owned)                  │
│ 2. Attempt direct TUN creation                              │
│                                                              │
│    ┌───────────────────────────────────────┐                │
│    │ Permission denied (no CAP_NET_ADMIN)  │                │
│    └───────────────────────────────────────┘                │
│                                                              │
│ 3. Spawn subprocess: sudo coral _tun-helper wg0 1420        │
│                                                              │
│    ┌──────────────────────────────────────────────────┐     │
│    │ coral _tun-helper (runs with elevated privileges) │    │
│    │                                                    │    │
│    │ 1. Create TUN device "wg0" with MTU 1420          │    │
│    │ 2. Get file descriptor (int)                      │    │
│    │ 3. Send FD to parent via Unix socket (SCM_RIGHTS) │    │
│    │ 4. Exit immediately                                │    │
│    └──────────────────────────────────────────────────┘     │
│                                                              │
│ 4. Receive FD from subprocess                                │
│ 5. Initialize WireGuard device with FD                       │
│ 6. Continue as regular user (RPC, database, etc.)           │
└─────────────────────────────────────────────────────────────┘
```

### Key Components

**1. Internal Helper Command**

A hidden CLI command `_tun-helper` (prefix indicates internal use):

```bash
# Not shown in help output, internal use only
coral _tun-helper <device-name> <mtu> <socket-path>
```

**2. File Descriptor Passing**

Using Unix domain socket with `SCM_RIGHTS` ancillary messages:

```
Parent Process                    Subprocess (privileged)
      │                                   │
      ├─ Create Unix socket               │
      ├─ Spawn subprocess ───────────────>│
      │                                   ├─ Connect to socket
      │                                   ├─ Create TUN device
      │                                   ├─ Get FD (e.g., 3)
      │                                   ├─ Send FD via SCM_RIGHTS
      │<───────────────────────────────── │
      ├─ Receive FD                       ├─ Exit (privilege dropped)
      ├─ Use FD for WireGuard device      │
      │                                   X
```

**3. Privilege Escalation Paths**

The solution supports multiple installation methods:

**Option A: Linux Capabilities (Recommended)**

```bash
$ sudo setcap cap_net_admin+ep /usr/local/bin/coral
$ coral colony start  # Works without sudo
```

**Option B: Setuid Binary (Linux/macOS)**

```bash
$ sudo chown root:root /usr/local/bin/coral
$ sudo chmod u+s /usr/local/bin/coral
$ coral colony start  # Works without sudo
```

**Option C: Runtime Sudo (Fallback)**

```bash
$ coral colony start  # Spawns: sudo coral _tun-helper wg0 1420
[sudo] password for user:
```

**Option D: Manual Sudo (Always Works)**

```bash
$ sudo coral colony start  # Entire process runs as root, but...
# Config files remain user-owned (subprocess detects SUDO_USER)
```

### Configuration File Ownership

Regardless of privilege escalation method, config files are always created with
user ownership:

```yaml
# ~/.coral/config.yaml - Always created with user ownership
---
version: "1.0"
colonies:
    -   id: "abc123"
        name: "production"
```

```bash
$ ls -la ~/.coral/
drwx------  2 user  user  4096 config.yaml
drwx------  2 user  user  4096 colonies/
-rw-------  1 user  user   512 colonies/abc123.yaml
```

Even when run with `sudo coral colony start`, the subprocess detects
`$SUDO_USER` and ensures files are owned by the original user.

## API Changes

### New CLI Command (Internal)

**Command**: `coral _tun-helper`

**Usage** (internal only, not documented in help):

```bash
coral _tun-helper <device-name> <mtu> <socket-path>
```

**Arguments**:

- `device-name`: TUN device name (e.g., "wg0" on Linux, "utun" on macOS)
- `mtu`: Maximum transmission unit (typically 1420)
- `socket-path`: Unix socket path for FD passing

**Exit Codes**:

- `0`: Success (FD sent to parent)
- `1`: Failed to create TUN device
- `2`: Failed to send FD to parent
- `3`: Invalid arguments

**Example Output** (stderr, for debugging):

```
[tun-helper] Creating TUN device: wg0 (MTU: 1420)
[tun-helper] Device created successfully (FD: 3)
[tun-helper] Sending FD to parent via /tmp/coral-tun-abc123.sock
[tun-helper] FD sent successfully, exiting
```

### Updated CLI Commands

**Existing commands with improved behavior**:

**`coral init`**

```bash
$ coral init

Created colony: production (ID: abc123)
Config: ~/.coral/colonies/abc123.yaml

Note: Starting the colony requires creating a TUN device.
Run one of the following:

  # Option 1: Install capabilities (recommended, Linux only)
  sudo setcap cap_net_admin+ep $(which coral)

  # Option 2: Run with sudo (works on all platforms)
  sudo coral colony start

  # Option 3: Make binary setuid (use with caution)
  sudo chown root:root $(which coral) && sudo chmod u+s $(which coral)
```

**`coral colony start`**

*Without privileges*:

```bash
$ coral colony start

Error: Failed to create TUN device: operation not permitted

TUN device creation requires elevated privileges. Choose one of:

  1. Install capabilities (Linux only, recommended):
     sudo setcap cap_net_admin+ep /usr/local/bin/coral

  2. Run with sudo:
     sudo coral colony start

  3. Make binary setuid (use with caution):
     sudo chown root:root /usr/local/bin/coral
     sudo chmod u+s /usr/local/bin/coral

For more information, see: docs/INSTALLATION.md
```

*With capabilities installed*:

```bash
$ coral colony start

Starting colony: production (ID: abc123)
Creating TUN device: wg0 (MTU: 1420)
WireGuard mesh: 10.42.0.1/16 (IPv4), fd42::1/48 (IPv6)
Listening on: 0.0.0.0:41580 (UDP)
gRPC server: localhost:9000

Colony started successfully.
```

*With sudo*:

```bash
$ sudo coral colony start

[tun-helper] Creating TUN device with elevated privileges
Starting colony: production (ID: abc123)
WireGuard mesh: 10.42.0.1/16 (IPv4), fd42::1/48 (IPv6)
Listening on: 0.0.0.0:41580 (UDP)
gRPC server: localhost:9000

Colony started successfully.

Note: Config files in ~/.coral/ are owned by user (not root).
```

### Environment Variables

**`CORAL_SKIP_TUN_HELPER`**

Skip subprocess-based TUN creation and fail immediately if direct creation
fails:

```bash
$ CORAL_SKIP_TUN_HELPER=1 coral colony start
Error: Failed to create TUN device: operation not permitted
(Helper subprocess disabled via CORAL_SKIP_TUN_HELPER)
```

Use case: CI/CD environments, containerized deployments, testing.

**`CORAL_TUN_HELPER_PATH`**

Override the path to the helper binary:

```bash
$ CORAL_TUN_HELPER_PATH=/opt/coral/bin/coral coral colony start
```

Use case: Custom installations, testing, development.

## Implementation Plan

### Phase 1: Core Subprocess Communication

**Files to create**:

- `internal/wireguard/helper.go` - Subprocess spawning and FD passing
- `internal/wireguard/privilege.go` - Privilege detection and escalation
- `internal/cli/tun_helper/tun_helper.go` - Internal helper command

**Files to modify**:

- `internal/wireguard/device.go` - Integrate helper fallback logic
- `cmd/coral/main.go` - Register `_tun-helper` command

**Key functions**:

- `createTUNWithHelper(name, mtu) (fd int, error)` - Spawn subprocess, receive
  FD
- `sendFDOverSocket(fd, socketPath) error` - Send FD via Unix socket
- `receiveFDFromSocket(socketPath) (int, error)` - Receive FD via Unix socket
- `detectPrivilegeMethod() PrivilegeMethod` - Detect capabilities, setuid, sudo

### Phase 2: Privilege Detection

**Privilege methods** (priority order):

1. **Direct** - Binary has capabilities or running as root
2. **Capabilities** - Binary has `CAP_NET_ADMIN` (Linux only)
3. **Setuid** - Binary is setuid root
4. **Sudo subprocess** - Spawn `sudo coral _tun-helper`
5. **Fail** - No privilege escalation available

**Detection logic**:

```
START
  │
  ├─ Is effective UID == 0? ───────────> [Direct] Create TUN
  │
  ├─ Linux && has CAP_NET_ADMIN? ─────> [Direct] Create TUN
  │
  ├─ Binary is setuid root? ──────────> [Direct] Create TUN
  │
  ├─ Can spawn sudo subprocess? ──────> [Subprocess] sudo coral _tun-helper
  │
  └─ Otherwise ──────────────────────> [Fail] Error with instructions
```

### Phase 3: Configuration Ownership

**User detection**:

- If `$SUDO_USER` set: Use that user for file ownership
- If `$SUDO_UID` and `$SUDO_GID` set: Use those IDs for `chown`
- Otherwise: Use current user

**File creation wrapper**:

```go
// Ensure config files are owned by the original user
func (l *Loader) SaveColonyConfig(config *ColonyConfig) error {
    // ... write file ...

    // Fix ownership if running as root via sudo
    if os.Geteuid() == 0 {
        originalUser := detectOriginalUser()
        os.Chown(path, originalUser.UID, originalUser.GID)
    }
}
```

### Phase 4: Installation Documentation

**Update documentation**:

- `docs/INSTALLATION.md` - Installation methods for each platform
- `docs/SECURITY.md` - Security implications of each method
- `README.md` - Quick start with privilege requirements

**Installation script** (optional):

```bash
#!/bin/bash
# install.sh - Install coral with appropriate privileges

if [ "$(uname)" = "Linux" ]; then
    echo "Installing with Linux capabilities (recommended)"
    sudo setcap cap_net_admin+ep /usr/local/bin/coral
else
    echo "Installing with setuid (macOS)"
    sudo chown root:root /usr/local/bin/coral
    sudo chmod u+s /usr/local/bin/coral
fi
```

### Implementation Status Summary

**✅ Phase 1: Partially Complete**
- Helper infrastructure (`internal/wireguard/helper.go`) - Complete
- Helper command (`internal/cli/tun_helper/tun_helper.go`) - Complete
- Command registration (`internal/cli/root.go`) - Complete
- ⚠️ **Not integrated**: Device startup flow does not automatically invoke helper

**✅ Phase 2: Not Implemented**
- Privilege method detection logic not implemented
- Automatic fallback sequence not implemented

**✅ Phase 3: Complete**
- User detection via `SUDO_USER`/`SUDO_UID`/`SUDO_GID` - Complete
  (`internal/privilege/privilege.go`)
- File ownership preservation in config loader - Complete
  (`internal/config/loader.go`)

**✅ Phase 4: Complete**
- README.md updated with installation instructions
- Security tradeoffs documented
- Platform-specific guidance provided

**Remaining Work (Future Enhancement):**

1. **FD-to-Interface Conversion** - Implement clean conversion from received
   file descriptor to `*wireguard.Interface` wrapper. The challenge is that
   `CreateTUN` returns an `Interface` but the helper returns a raw FD.

2. **Automatic Fallback Integration** - Modify `device.go:Start()` to:
   ```go
   iface, err := CreateTUN("wg0", mtu)
   if isPermissionError(err) {
       fd, helperErr := createTUNWithHelper("wg0", mtu)
       if helperErr == nil {
           iface = createInterfaceFromFD(fd, "wg0", mtu)
       }
   }
   ```

3. **Privilege Method Detection** - Implement `detectPrivilegeMethod()` to
   determine the best escalation approach before attempting TUN creation.

## Testing Strategy

### Unit Tests

**Privilege detection** (`internal/wireguard/privilege_test.go`):

- `TestDetectPrivilegeMethod` - Verify correct method detection
- `TestDetectOriginalUser` - Test SUDO_USER parsing
- `TestIsCapable` - Test capability checking (Linux)

**FD passing** (`internal/wireguard/helper_test.go`):

- `TestSendReceiveFD` - Verify FD passing over Unix socket
- `TestSubprocessCommunication` - Test subprocess lifecycle
- `TestFDOwnership` - Verify FD remains valid after subprocess exit

### Integration Tests

**TUN creation paths** (`internal/wireguard/device_integration_test.go`):

- `TestDirectTUNCreation` - Direct creation (requires privileges)
- `TestSubprocessTUNCreation` - Subprocess-based creation
- `TestFallbackToHelper` - Verify fallback when direct fails

**Config ownership** (`internal/config/loader_integration_test.go`):

- `TestConfigOwnershipWithSudo` - Verify files owned by SUDO_USER
- `TestConfigPermissions` - Verify 0600 permissions on sensitive files

### Manual Testing

**Test matrix**:

| Platform | Privilege Method | Expected Result            |
|----------|------------------|----------------------------|
| Linux    | No privileges    | Error with helpful message |
| Linux    | CAP_NET_ADMIN    | Direct TUN creation        |
| Linux    | Setuid root      | Direct TUN creation        |
| Linux    | Sudo subprocess  | Helper creates TUN         |
| Linux    | Sudo full binary | SUDO_USER ownership        |
| macOS    | No privileges    | Error with helpful message |
| macOS    | Setuid root      | Direct TUN creation        |
| macOS    | Sudo subprocess  | Helper creates TUN         |
| macOS    | Sudo full binary | SUDO_USER ownership        |

**Test scenarios**:

1. Fresh install, no privileges → Clear error
2. Install with `setcap` → Works without sudo
3. Run with `sudo coral colony start` → Configs owned by user
4. Subprocess spawn with `sudo` password prompt → Works
5. Multiple colonies started with different privilege methods → All work

## Security Considerations

### Privilege Minimization

**Subprocess lifetime**: Helper process lives only ~50-100ms:

```
[0ms]   Spawn subprocess
[10ms]  Create TUN device
[20ms]  Send FD to parent
[30ms]  Exit and drop privileges
```

**Subprocess isolation**: Helper performs only one operation:

- No network access
- No filesystem writes (except TUN device creation)
- No IPC except FD passing to parent
- Immediate exit after FD transfer

### Attack Surface Analysis

**Threat: Malicious subprocess invocation**

If an attacker can control subprocess arguments:

```bash
coral _tun-helper "../../../evil" 1420 /tmp/socket
```

Mitigation:

- Validate device name format (alphanumeric + hyphens only)
- Validate MTU range (68-65535)
- Validate socket path (must be in `/tmp` or `/run`)
- Reject paths with `..` or absolute paths outside allowed directories

**Threat: Race condition on Unix socket**

Attacker creates socket before subprocess:

```bash
touch /tmp/coral-tun-abc123.sock  # Attacker pre-creates
coral colony start                # Connects to attacker's socket
```

Mitigation:

- Generate random socket names: `/tmp/coral-tun-{uuid}.sock`
- Set socket permissions to `0600` (owner-only)
- Unlink socket before creation
- Verify socket ownership after connection

**Threat: Privilege persistence**

Subprocess runs as root but doesn't exit:

```
[tun-helper] Created TUN device (FD: 3)
[tun-helper] Sending FD...
[tun-helper] Sleeping forever... (attacker keeps root process)
```

Mitigation:

- Parent process monitors subprocess with timeout (5s max)
- Subprocess uses `defer os.Exit(0)` to ensure exit
- Parent kills subprocess if timeout exceeded

**Threat: Setuid binary exploitation**

If binary is setuid root, any code execution vulnerability = root:

Mitigation:

- Prefer capabilities over setuid (Linux)
- Document setuid as "use with caution"
- Recommend sudo subprocess for untrusted environments
- Code audit for buffer overflows, injection attacks

### Privilege Escalation Methods Comparison

| Method          | Security    | UX              | Platforms    |
|-----------------|-------------|-----------------|--------------|
| Capabilities    | ⭐⭐⭐⭐⭐ Best  | ⭐⭐⭐⭐ Good       | Linux only   |
| Sudo subprocess | ⭐⭐⭐⭐ Good   | ⭐⭐⭐ OK          | Linux, macOS |
| Setuid binary   | ⭐⭐ Moderate | ⭐⭐⭐⭐⭐ Excellent | Linux, macOS |
| Run as root     | ⭐ Poor      | ⭐⭐⭐⭐ Good       | Linux, macOS |

**Recommendation**: Default to capabilities (Linux) or sudo subprocess (macOS).

### Audit Trail

**Logging privilege escalation**:

```
2025-10-30T12:00:00Z INFO Starting colony: production (ID: abc123)
2025-10-30T12:00:00Z WARN Direct TUN creation failed: operation not permitted
2025-10-30T12:00:00Z INFO Attempting TUN creation via helper subprocess
2025-10-30T12:00:01Z INFO Spawning: sudo /usr/local/bin/coral _tun-helper wg0 1420 /tmp/coral-tun-550e8400.sock
2025-10-30T12:00:02Z INFO TUN device created successfully (FD: 3)
2025-10-30T12:00:02Z INFO Helper subprocess exited (PID: 12345, exit code: 0)
```

**Syslog integration** (Linux):

```
Oct 30 12:00:02 host coral[12345]: TUN device wg0 created by user alice (via sudo)
```

## Migration Strategy

### Backward Compatibility

**No breaking changes**:

- Existing `coral init` and `coral colony start` commands unchanged
- Existing config files remain valid
- No database migrations required

**Existing workflows still work**:

```bash
# Old workflow (still works, but not recommended)
$ sudo coral colony start

# New workflow (recommended after capabilities installed)
$ coral colony start
```

### Migration Path

**For existing installations**:

1. **No action required** - Existing `sudo coral` commands continue working
2. **Optional improvement** - Install capabilities for better UX:
   ```bash
   sudo setcap cap_net_admin+ep $(which coral)
   ```
3. **Fix root-owned configs** (if created before this RFD):
   ```bash
   # One-time fix for configs created as root
   sudo chown -R $USER:$USER ~/.coral/
   ```

### Rollback Plan

If issues arise, users can revert to full-sudo workflow:

```bash
# Disable helper subprocess
export CORAL_SKIP_TUN_HELPER=1
sudo coral colony start
```

No code removal required - feature is opt-in via privilege detection.

## Future Enhancements

### Capability-based Permissions (Linux)

Future RFD could add ambient capabilities for fine-grained control:

```go
// Drop all capabilities except CAP_NET_ADMIN
capability.Drop(capability.ALL)
capability.Add(capability.CAP_NET_ADMIN)
```

### Windows Support

Future RFD for Windows TUN device creation:

- WinTUN driver from WireGuard project
- Requires Administrator privileges (similar challenge)
- May need separate helper service

### Containerized Environments

Future RFD for running colony in containers:

- Use `--cap-add=NET_ADMIN` instead of privileged containers
- Pre-created TUN devices passed into container
- Helper subprocess not needed (TUN created by orchestrator)

### Installation Automation

Future tooling to automate privilege setup:

```bash
# Automatic detection and installation
coral install-privileges --method auto
```

### Audit Mode

Enhanced logging for compliance:

```bash
# Log all privilege escalations to syslog
coral colony start --audit-mode
```

## Appendix

### Related RFDs

- **RFD 007**: WireGuard Mesh Implementation - Context for TUN device creation
- **RFD 002**: Application Identity - User credential management

### Platform-Specific Details

**Linux Capabilities**

Check if binary has `CAP_NET_ADMIN`:

```bash
$ getcap /usr/local/bin/coral
/usr/local/bin/coral = cap_net_admin+ep
```

Install capabilities:

```bash
$ sudo setcap cap_net_admin+ep /usr/local/bin/coral
```

Remove capabilities:

```bash
$ sudo setcap -r /usr/local/bin/coral
```

**macOS Entitlements**

WireGuard on macOS requires either:

- Setuid root binary (simpler, used by this RFD)
- Network Extension entitlement (requires Apple Developer Program)

For distribution:

```bash
# Sign with entitlements (requires paid Apple Developer)
codesign --entitlements coral.entitlements -s "Developer ID" coral
```

**File Descriptor Passing**

Unix domain socket with `SCM_RIGHTS`:

```go
import "golang.org/x/sys/unix"

// Send FD
rights := unix.UnixRights(int(fd))
unix.Sendmsg(socketFD, nil, rights, nil, 0)

// Receive FD
oob := make([]byte, unix.CmsgSpace(4))
_, _, _, _, err := unix.Recvmsg(socketFD, nil, oob, 0)
scm, err := unix.ParseSocketControlMessage(oob)
fds, err := unix.ParseUnixRights(&scm[0])
```

### Error Message Reference

**E001: No TUN creation privileges**

```
Error: Failed to create TUN device: operation not permitted

TUN device creation requires elevated privileges. Choose one of:

  1. Install capabilities (Linux only, recommended):
     sudo setcap cap_net_admin+ep /usr/local/bin/coral

  2. Run with sudo:
     sudo coral colony start

  3. Make binary setuid (use with caution):
     sudo chown root:root /usr/local/bin/coral
     sudo chmod u+s /usr/local/bin/coral

For more information, see: docs/INSTALLATION.md
```

**E002: Helper subprocess failed**

```
Error: TUN helper subprocess failed

The privileged helper subprocess could not create the TUN device.
This may indicate a platform incompatibility or kernel configuration issue.

Debug information:
  Command: sudo /usr/local/bin/coral _tun-helper wg0 1420 /tmp/coral-tun-abc123.sock
  Exit code: 1
  Stderr: Failed to create TUN device: no such device

Try running the helper command manually for more details.
```

**E003: Socket communication failure**

```
Error: Failed to receive file descriptor from helper subprocess

The subprocess created the TUN device but could not pass it to the parent.
This may indicate an IPC configuration issue.

Debug information:
  Socket: /tmp/coral-tun-abc123.sock
  Error: connection refused

Check system logs for more details.
```

### Performance Considerations

**Subprocess overhead**:

- Spawn time: ~10ms (fork + exec)
- TUN creation: ~5ms (kernel syscall)
- FD passing: ~1ms (Unix socket)
- Total: ~16ms additional latency

**Comparison to direct creation**:

- Direct TUN creation: ~5ms
- Via subprocess: ~16ms
- Overhead: ~11ms (3x slower, but acceptable for startup operation)

**Memory overhead**:

- Subprocess memory: ~10MB (Go runtime)
- Lifetime: <100ms
- Impact: Negligible for colony startup

### References

- WireGuard protocol specification: https://www.wireguard.com/protocol/
- Linux capabilities: https://man7.org/linux/man-pages/man7/capabilities.7.html
- Unix domain sockets: https://man7.org/linux/man-pages/man7/unix.7.html
- SCM_RIGHTS: https://man7.org/linux/man-pages/man3/cmsg.3.html
- Go syscall package: https://pkg.go.dev/golang.org/x/sys/unix
