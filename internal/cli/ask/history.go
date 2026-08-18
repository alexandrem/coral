package ask

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
)

// maxHistoryEntries caps the persisted input history file, oldest entries
// are dropped first once the cap is exceeded.
const maxHistoryEntries = 1000

// getHistoryPath returns the path to the input-history file shared by
// coral ask and coral terminal (RFD 051 follow-up: persistent input history).
func getHistoryPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".coral", "history"), nil
}

// LoadInputHistory reads persisted input history, oldest entry first.
// Returns nil (not an error) if no history file exists yet, so callers can
// treat a missing file the same as an empty one.
func LoadInputHistory() []string {
	path, err := getHistoryPath()
	if err != nil {
		return nil
	}

	f, err := os.Open(path) // #nosec G304 - fixed path under the user's home directory
	if err != nil {
		return nil
	}
	defer f.Close() //nolint:errcheck // read-only handle, nothing actionable on close failure

	var entries []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		if line := scanner.Text(); strings.TrimSpace(line) != "" {
			entries = append(entries, line)
		}
	}
	return entries
}

// AppendInputHistory persists one input entry, skipping immediate
// consecutive duplicates and capping the file at maxHistoryEntries. This is
// best-effort: failures are silently ignored since history is a convenience,
// not state the session depends on.
func AppendInputHistory(entry string) {
	entry = strings.TrimSpace(entry)
	if entry == "" || strings.Contains(entry, "\n") {
		return
	}

	path, err := getHistoryPath()
	if err != nil {
		return
	}
	//nolint:gosec // G301: directory needs standard permissions for traversal
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return
	}

	entries := LoadInputHistory()
	if len(entries) > 0 && entries[len(entries)-1] == entry {
		return // Skip consecutive duplicates, matching shell history convention.
	}
	entries = append(entries, entry)
	if len(entries) > maxHistoryEntries {
		entries = entries[len(entries)-maxHistoryEntries:]
	}

	data := strings.Join(entries, "\n") + "\n"
	// Restrictive permissions - history may contain sensitive query content.
	_ = os.WriteFile(path, []byte(data), 0600)
}
