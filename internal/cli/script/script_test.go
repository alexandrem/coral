package script

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setHome overrides HOME so all commands write to a temp directory.
func setHome(t *testing.T) string {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	return tmp
}

func TestValidName(t *testing.T) {
	cases := []struct {
		name  string
		valid bool
	}{
		{"latency-report", true},
		{"my_script", true},
		{"Script123", true},
		{"", false},
		{"has space", false},
		{"has/slash", false},
		{"has.dot", false},
	}
	for _, tc := range cases {
		assert.Equal(t, tc.valid, validName(tc.name), "name=%q", tc.name)
	}
}

func TestWrite_CreatesScript(t *testing.T) {
	home := setHome(t)
	cmd := newWriteCmd()
	cmd.Flags().Parse([]string{"--name", "hello", "--content", "console.log('hi')"}) //nolint:errcheck
	err := cmd.RunE(cmd, nil)
	require.NoError(t, err)

	data, err := os.ReadFile(filepath.Join(home, ".coral", "scripts", "hello.ts"))
	require.NoError(t, err)
	assert.Equal(t, "console.log('hi')", string(data))
}

func TestWrite_FromFile(t *testing.T) {
	home := setHome(t)
	src := filepath.Join(t.TempDir(), "src.ts")
	require.NoError(t, os.WriteFile(src, []byte("// source"), 0600))

	cmd := newWriteCmd()
	require.NoError(t, cmd.Flags().Parse([]string{"--name", "from-file", "--file", src}))
	require.NoError(t, cmd.RunE(cmd, nil))

	data, err := os.ReadFile(filepath.Join(home, ".coral", "scripts", "from-file.ts"))
	require.NoError(t, err)
	assert.Equal(t, "// source", string(data))
}

func TestWrite_EmptyContent_Error(t *testing.T) {
	setHome(t)
	cmd := newWriteCmd()
	require.NoError(t, cmd.Flags().Parse([]string{"--name", "empty", "--content", ""}))
	err := cmd.RunE(cmd, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty")
}

func TestWrite_InvalidName_Error(t *testing.T) {
	setHome(t)
	cmd := newWriteCmd()
	require.NoError(t, cmd.Flags().Parse([]string{"--name", "bad name", "--content", "x"}))
	err := cmd.RunE(cmd, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid script name")
}

func TestList_Empty(t *testing.T) {
	setHome(t)
	var out bytes.Buffer
	cmd := newListCmd()
	cmd.SetOut(&out)
	require.NoError(t, cmd.Flags().Parse([]string{}))
	require.NoError(t, cmd.RunE(cmd, nil))
	assert.Contains(t, out.String(), "No scripts")
}

func TestList_JSON_Empty(t *testing.T) {
	setHome(t)
	var out bytes.Buffer
	cmd := newListCmd()
	cmd.SetOut(&out)
	require.NoError(t, cmd.Flags().Parse([]string{"--format", "json"}))
	require.NoError(t, cmd.RunE(cmd, nil))

	var scripts []scriptInfo
	require.NoError(t, json.Unmarshal(out.Bytes(), &scripts))
	assert.Empty(t, scripts)
}

func TestList_ShowsWrittenScript(t *testing.T) {
	home := setHome(t)
	dir := filepath.Join(home, ".coral", "scripts")
	require.NoError(t, os.MkdirAll(dir, 0700))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "myscript.ts"), []byte("x"), 0600))

	var out bytes.Buffer
	cmd := newListCmd()
	cmd.SetOut(&out)
	require.NoError(t, cmd.Flags().Parse([]string{"--format", "json"}))
	require.NoError(t, cmd.RunE(cmd, nil))

	var scripts []scriptInfo
	require.NoError(t, json.Unmarshal(out.Bytes(), &scripts))
	require.Len(t, scripts, 1)
	assert.Equal(t, "myscript", scripts[0].Name)
}

func TestRemove_Existing(t *testing.T) {
	home := setHome(t)
	dir := filepath.Join(home, ".coral", "scripts")
	require.NoError(t, os.MkdirAll(dir, 0700))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "todelete.ts"), []byte("x"), 0600))

	cmd := newRemoveCmd()
	require.NoError(t, cmd.Flags().Parse([]string{"--name", "todelete"}))
	require.NoError(t, cmd.RunE(cmd, nil))

	_, err := os.Stat(filepath.Join(dir, "todelete.ts"))
	assert.True(t, os.IsNotExist(err))
}

func TestRemove_NotFound_Error(t *testing.T) {
	setHome(t)
	cmd := newRemoveCmd()
	require.NoError(t, cmd.Flags().Parse([]string{"--name", "nope"}))
	err := cmd.RunE(cmd, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}
