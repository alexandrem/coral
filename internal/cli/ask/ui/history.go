package ui

import "strings"

// recordHistory appends a submitted line to the in-session history and, if
// configured, persists it to disk. Called once per Enter submission, before
// the line is interpreted as a question or inline command.
func (m *Model) recordHistory(entry string) {
	if len(m.history) == 0 || m.history[len(m.history)-1] != entry {
		m.history = append(m.history, entry)
	}
	m.historyIdx = -1
	m.historyDraft = ""
	if m.appendHistory != nil {
		m.appendHistory(entry)
	}
}

// historyPrev navigates one step further back in input history (older
// entries), remembering the in-progress draft on first entry.
func (m Model) historyPrev() Model {
	if len(m.history) == 0 {
		return m
	}
	if m.historyIdx == -1 {
		m.historyDraft = m.input.Value()
		m.historyIdx = len(m.history)
	}
	if m.historyIdx == 0 {
		return m
	}
	m.historyIdx--
	m.input.SetValue(m.history[m.historyIdx])
	m.input.CursorEnd()
	return m
}

// historyNext navigates one step forward (newer entries), restoring the
// in-progress draft once the end of history is reached.
func (m Model) historyNext() Model {
	if m.historyIdx == -1 {
		return m
	}
	m.historyIdx++
	if m.historyIdx >= len(m.history) {
		m.historyIdx = -1
		m.input.SetValue(m.historyDraft)
	} else {
		m.input.SetValue(m.history[m.historyIdx])
	}
	m.input.CursorEnd()
	return m
}

// findHistoryMatch returns the index of the most recent entry at or before
// fromIdx whose text contains query as a case-insensitive fuzzy subsequence,
// or -1 if none match. Searching backward from fromIdx and returning the
// first hit mirrors shell reverse-incremental search: repeated calls with a
// decreasing fromIdx step further back through matches.
func (m Model) findHistoryMatch(query string, fromIdx int) int {
	if query == "" {
		return -1
	}
	q := strings.ToLower(query)
	for i := fromIdx; i >= 0 && i < len(m.history); i-- {
		if isSubsequence(q, strings.ToLower(m.history[i])) {
			return i
		}
	}
	return -1
}

// isSubsequence reports whether every rune in q appears in s in order, not
// necessarily contiguously — a lightweight fuzzy match with no dependency.
func isSubsequence(q, s string) bool {
	qr := []rune(q)
	qi := 0
	for _, r := range s {
		if qi < len(qr) && r == qr[qi] {
			qi++
		}
	}
	return qi == len(qr)
}
