// Package proc provides utilities for process discovery on Linux systems.
// It parses /proc filesystem to find process information based on network ports.
package proc

import (
	"bufio"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
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
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return 0, err
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		pidStr := entry.Name()
		pid, err := strconv.Atoi(pidStr)
		if err != nil {
			continue // Not a PID directory
		}

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
				return int32(pid), nil
			}
		}
	}

	return 0, nil
}
