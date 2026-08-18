package ask

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadInputHistory_NoFile(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	assert.Nil(t, LoadInputHistory())
}

func TestAppendAndLoadInputHistory(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	AppendInputHistory("first question")
	AppendInputHistory("second question")

	assert.Equal(t, []string{"first question", "second question"}, LoadInputHistory())
}

func TestAppendInputHistory_SkipsConsecutiveDuplicates(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	AppendInputHistory("same")
	AppendInputHistory("same")
	AppendInputHistory("same")

	assert.Equal(t, []string{"same"}, LoadInputHistory())
}

func TestAppendInputHistory_AllowsNonConsecutiveDuplicates(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	AppendInputHistory("a")
	AppendInputHistory("b")
	AppendInputHistory("a")

	assert.Equal(t, []string{"a", "b", "a"}, LoadInputHistory())
}

func TestAppendInputHistory_SkipsEmpty(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	AppendInputHistory("   ")
	assert.Nil(t, LoadInputHistory())
}

func TestAppendInputHistory_CapsAtMax(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	const overflow = 10
	for i := 0; i < maxHistoryEntries+overflow; i++ {
		AppendInputHistory(fmt.Sprintf("entry-%d", i))
	}

	entries := LoadInputHistory()
	require.Len(t, entries, maxHistoryEntries)
	assert.Equal(t, fmt.Sprintf("entry-%d", overflow), entries[0], "oldest entries should be dropped")
	assert.Equal(t, fmt.Sprintf("entry-%d", maxHistoryEntries+overflow-1), entries[len(entries)-1])
}

func TestAppendInputHistory_RestrictivePermissions(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	AppendInputHistory("secret query")

	info, err := os.Stat(filepath.Join(home, ".coral", "history"))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0600), info.Mode().Perm())
}
