package ask

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestAppendFormatJSON(t *testing.T) {
	t.Run("no format flag — appends --format json", func(t *testing.T) {
		args := []string{"query", "traces", "--service", "api", "--since", "10m"}
		result := appendFormatJSON(args)
		assert.Equal(t, append(args, "--format", "json"), result)
	})

	t.Run("--format json already present — no change", func(t *testing.T) {
		args := []string{"query", "traces", "--format", "json"}
		result := appendFormatJSON(args)
		assert.Equal(t, args, result)
	})

	t.Run("--format=json already present — no change", func(t *testing.T) {
		args := []string{"query", "traces", "--format=json"}
		result := appendFormatJSON(args)
		assert.Equal(t, args, result)
	})

	t.Run("-o json already present — no change", func(t *testing.T) {
		args := []string{"query", "traces", "-o", "json"}
		result := appendFormatJSON(args)
		assert.Equal(t, args, result)
	})

	t.Run("-o=json already present — no change", func(t *testing.T) {
		args := []string{"query", "traces", "-o=json"}
		result := appendFormatJSON(args)
		assert.Equal(t, args, result)
	})

	t.Run("--format table — replaces with json appended", func(t *testing.T) {
		// --format table is present but not json, so we append --format json
		// (the command will use the last --format flag, which is json)
		args := []string{"query", "traces", "--format", "table"}
		result := appendFormatJSON(args)
		assert.Equal(t, []string{"query", "traces", "--format", "table", "--format", "json"}, result)
	})

	t.Run("empty args — appends --format json", func(t *testing.T) {
		args := []string{}
		result := appendFormatJSON(args)
		assert.Equal(t, []string{"--format", "json"}, result)
	})
}

func TestCLICommandString(t *testing.T) {
	t.Run("simple args — no quoting", func(t *testing.T) {
		args := []string{"query", "traces", "--service", "api"}
		assert.Equal(t, "coral query traces --service api", cliCommandString(args))
	})

	t.Run("arg with spaces — quoted", func(t *testing.T) {
		args := []string{"query", "traces", "--service", "my service"}
		result := cliCommandString(args)
		assert.Contains(t, result, `"my service"`)
	})

	t.Run("empty args — just coral", func(t *testing.T) {
		assert.Equal(t, "coral ", cliCommandString([]string{}))
	})
}

func TestBuildCLITools(t *testing.T) {
	tools := buildCLITools()
	assert.Len(t, tools, 1)
	assert.Equal(t, "coral_cli", tools[0].Name)
	assert.NotEmpty(t, tools[0].Description)
	// Schema must include the args property.
	assert.NotEmpty(t, tools[0].RawInputSchema)
}
