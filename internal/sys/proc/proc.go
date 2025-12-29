// Package proc provides utilities for process discovery on Linux systems.
// It parses /proc filesystem to find process information based on network ports.
package proc

import (
	"bufio"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// FindPidByPort finds the PID of the process listening on the given port.
// This is a simplified implementation parsing /proc/net/tcp.
func FindPidByPort(port int) (int32, error) {
	// Check both IPv4 and IPv6
	inode, err := findSocketInode(port, "/proc/net/tcp")
	if err != nil || inode == "" {
		inode, err = findSocketInode(port, "/proc/net/tcp6")
	}

	if err != nil {
		return 0, err
	}
	if inode == "" {
		return 0, nil // Not found
	}

	return findPidByInode(inode)
}

// findSocketInode parses /proc/net/tcp(6) to find the inode for a listening port.
func findSocketInode(port int, procPath string) (string, error) {
	//nolint:gosec // G304: Path is from /proc filesystem for system information.
	f, err := os.Open(procPath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	defer f.Close() // nolint:errcheck

	scanner := bufio.NewScanner(f)
	// Skip header
	if scanner.Scan() {
		_ = scanner.Text()
	}

	targetHexPort := fmt.Sprintf("%04X", port)

	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 10 {
			continue
		}

		// Field 1: local_address (IP:Port)
		localAddr := fields[1]
		parts := strings.Split(localAddr, ":")
		if len(parts) != 2 {
			continue
		}

		hexPort := parts[1]
		if hexPort != targetHexPort {
			continue
		}

		// Field 3: st (state). 0A is LISTEN.
		state := fields[3]
		if state != "0A" {
			continue
		}

		// Field 9: inode
		return fields[9], nil
	}

	return "", nil
}

// findPidByInode scans /proc/[pid]/fd/ to find the process owning the socket inode.
func findPidByInode(inode string) (int32, error) {
	socketLink := "socket:[" + inode + "]"

	// Iterate over all PIDs in /proc
	pids, err := ListPids()
	if err != nil {
		return 0, err
	}

	for _, pid := range pids {
		pidStr := strconv.Itoa(pid)
		fdDir := filepath.Join("/proc", pidStr, "fd")
		fds, err := os.ReadDir(fdDir)
		if err != nil {
			continue // Can't read fd dir (permission denied, etc.)
		}

		for _, fd := range fds {
			info, err := fd.Info()
			if err != nil {
				continue
			}
			// Optimization: check if it's a symlink
			if info.Mode()&fs.ModeSymlink == 0 {
				continue
			}

			linkPath, err := os.Readlink(filepath.Join(fdDir, fd.Name()))
			if err != nil {
				continue
			}

			if linkPath == socketLink {
				//nolint:gosec // G109: PID conversion is safe, validated by Atoi
				return int32(pid), nil
			}
		}
	}

	return 0, nil
}

// GetKernelVersion reads the kernel version from /proc/version.
func GetKernelVersion() string {
	data, err := os.ReadFile("/proc/version")
	if err != nil {
		return "unknown"
	}

	// Parse version from output like "Linux version 5.15.0-xxx...".
	version := string(data)
	if idx := strings.Index(version, "Linux version "); idx >= 0 {
		version = version[idx+14:] // Skip "Linux version ".
		if idx := strings.Index(version, " "); idx >= 0 {
			version = version[:idx]
		}
		return version
	}

	return "unknown"
}

// GetBinaryPath returns the path to the executable for the given PID.
func GetBinaryPath(pid int) (string, error) {
	return os.Readlink(fmt.Sprintf("/proc/%d/exe", pid))
}

// ListPids returns a list of all running process IDs from /proc.
// Pids are sorted in ascending order.
func ListPids() ([]int, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, fmt.Errorf("failed to read /proc: %w", err)
	}

	var pids []int
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		// Parse PID from directory name.
		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue // Not a numeric directory.
		}

		if pid > 0 {
			pids = append(pids, pid)
		}
	}
	// Sort PIDs (lowest first).
	sort.Ints(pids)

	return pids, nil
}

// KernelSymbol represents a kernel symbol from /proc/kallsyms.
type KernelSymbol struct {
	Address uint64
	Type    byte
	Name    string
	Module  string // Empty for core kernel, module name for loadable modules
}

// ReadKallsyms reads and parses /proc/kallsyms.
// It returns a list of symbols and the count of zero addresses found (indicating permission issues).
func ReadKallsyms() ([]KernelSymbol, int, error) {
	file, err := os.Open("/proc/kallsyms")
	if err != nil {
		return nil, 0, fmt.Errorf("failed to open /proc/kallsyms: %w", err)
	}
	defer file.Close() // nolint:errcheck

	var symbols []KernelSymbol
	scanner := bufio.NewScanner(file)
	zeroAddresses := 0

	for scanner.Scan() {
		line := scanner.Text()
		parts := strings.Fields(line)
		if len(parts) < 3 {
			continue
		}

		// Parse address
		var addr uint64
		if _, err := fmt.Sscanf(parts[0], "%x", &addr); err != nil {
			continue
		}

		// Check for zero addresses (means insufficient permissions)
		if addr == 0 {
			zeroAddresses++
			continue
		}

		// Parse symbol type and name
		symType := parts[1][0]
		symName := parts[2]

		// Parse optional module name [module_name]
		var module string
		if len(parts) > 3 && strings.HasPrefix(parts[3], "[") && strings.HasSuffix(parts[3], "]") {
			module = strings.Trim(parts[3], "[]")
		}

		symbols = append(symbols, KernelSymbol{
			Address: addr,
			Type:    symType,
			Name:    symName,
			Module:  module,
		})
	}

	if err := scanner.Err(); err != nil {
		return nil, zeroAddresses, fmt.Errorf("failed to read /proc/kallsyms: %w", err)
	}

	return symbols, zeroAddresses, nil
}
