package ui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHistoryPrevNext_NavigatesAndRestoresDraft(t *testing.T) {
	m := newTestModel(t, &spyAgent{}, nil)
	m.SetHistory([]string{"first", "second", "third"}, nil)
	m.input.SetValue("draft in progress")

	m = m.historyPrev()
	assert.Equal(t, "third", m.input.Value())

	m = m.historyPrev()
	assert.Equal(t, "second", m.input.Value())

	m = m.historyPrev()
	assert.Equal(t, "first", m.input.Value())

	// At the oldest entry, further Prev is a no-op.
	m = m.historyPrev()
	assert.Equal(t, "first", m.input.Value())

	m = m.historyNext()
	assert.Equal(t, "second", m.input.Value())
	m = m.historyNext()
	assert.Equal(t, "third", m.input.Value())

	// Past the newest entry, the original draft is restored.
	m = m.historyNext()
	assert.Equal(t, "draft in progress", m.input.Value())
	assert.Equal(t, -1, m.historyIdx)
}

func TestHistoryPrev_EmptyHistoryIsNoop(t *testing.T) {
	m := newTestModel(t, &spyAgent{}, nil)
	m.input.SetValue("hello")

	m = m.historyPrev()
	assert.Equal(t, "hello", m.input.Value())
}

func TestFindHistoryMatch_FuzzySubsequenceMostRecentFirst(t *testing.T) {
	m := newTestModel(t, &spyAgent{}, nil)
	m.SetHistory([]string{
		"show me error logs",
		"what is cpu usage",
		"show error rate for checkout",
	}, nil)

	idx := m.findHistoryMatch("shwerr", len(m.history)-1)
	require.GreaterOrEqual(t, idx, 0)
	assert.Equal(t, "show error rate for checkout", m.history[idx], "most recent matching entry should win")

	// Stepping back from before that match should find the older one.
	idx2 := m.findHistoryMatch("shwerr", idx-1)
	require.GreaterOrEqual(t, idx2, 0)
	assert.Equal(t, "show me error logs", m.history[idx2])
}

func TestFindHistoryMatch_NoMatch(t *testing.T) {
	m := newTestModel(t, &spyAgent{}, nil)
	m.SetHistory([]string{"hello world"}, nil)

	assert.Equal(t, -1, m.findHistoryMatch("zzz", 0))
	assert.Equal(t, -1, m.findHistoryMatch("", 0))
}

func TestUpDownKeys_NavigateHistoryThroughUpdate(t *testing.T) {
	m := newTestModel(t, &spyAgent{}, nil)
	m.SetHistory([]string{"alpha", "beta"}, nil)

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyUp})
	result := updated.(Model)
	assert.Equal(t, "beta", result.input.Value())

	updated, _ = result.Update(tea.KeyMsg{Type: tea.KeyUp})
	result = updated.(Model)
	assert.Equal(t, "alpha", result.input.Value())
}

func TestRecordHistory_PersistsEveryCallAndDedupesInSession(t *testing.T) {
	var appended []string
	m := newTestModel(t, &spyAgent{}, nil)
	m.appendHistory = func(e string) { appended = append(appended, e) }

	m.recordHistory("first")
	m.recordHistory("first") // consecutive duplicate should not be added to in-session history

	assert.Equal(t, []string{"first"}, m.history)
	assert.Equal(t, []string{"first", "first"}, appended, "persistence callback fires every submission regardless of dedup")
}

func TestCtrlR_EntersSearchMode(t *testing.T) {
	m := newTestModel(t, &spyAgent{}, nil)
	m.SetHistory([]string{"show traces for api"}, nil)

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlR})
	result := updated.(Model)
	assert.Equal(t, stateHistorySearch, result.currentState)
}

func TestHistorySearch_TypeAndEnterFillsInput(t *testing.T) {
	m := newTestModel(t, &spyAgent{}, nil)
	m.SetHistory([]string{"show traces for api", "show error logs"}, nil)
	m.currentState = stateHistorySearch

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("trac")})
	result := updated.(Model)
	assert.Equal(t, "trac", result.searchQuery)
	require.GreaterOrEqual(t, result.searchMatchIdx, 0)
	assert.Equal(t, "show traces for api", result.history[result.searchMatchIdx])

	updated, _ = result.Update(tea.KeyMsg{Type: tea.KeyEnter})
	result = updated.(Model)
	assert.Equal(t, stateIdle, result.currentState)
	assert.Equal(t, "show traces for api", result.input.Value())
}

func TestHistorySearch_EscCancelsWithoutFillingInput(t *testing.T) {
	m := newTestModel(t, &spyAgent{}, nil)
	m.SetHistory([]string{"show traces for api"}, nil)
	m.currentState = stateHistorySearch
	m.searchQuery = "trac"

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	result := updated.(Model)
	assert.Equal(t, stateIdle, result.currentState)
	assert.Empty(t, result.searchQuery)
	assert.Empty(t, result.input.Value(), "Esc should not fill the input")
}

func TestSetInputValue(t *testing.T) {
	m := newTestModel(t, &spyAgent{}, nil)
	result := m.SetInputValue("hello from browser")
	assert.Equal(t, "hello from browser", result.input.Value())
}
