package ask

import (
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
)

// buildTestRoot creates a minimal Cobra command tree for reference generation tests.
func buildTestRoot() *cobra.Command {
	root := &cobra.Command{Use: "coral"}

	// query group with leaf commands.
	query := &cobra.Command{Use: "query", Short: "Query observability data"}
	summary := &cobra.Command{Use: "summary", Short: "Get a high-level health summary"}
	summary.Flags().String("since", "5m", "Time range")
	summary.Flags().String("format", "text", "Output format")
	query.AddCommand(summary)

	traces := &cobra.Command{Use: "traces", Short: "Query distributed traces"}
	traces.Flags().String("since", "1h", "Time range")
	query.AddCommand(traces)

	root.AddCommand(query)

	// debug group.
	debug := &cobra.Command{Use: "debug", Short: "Debug tools"}
	attach := &cobra.Command{Use: "attach", Short: "Attach a debug session"}
	attach.Flags().String("service", "", "Service name")
	debug.AddCommand(attach)
	root.AddCommand(debug)

	// service group.
	service := &cobra.Command{Use: "service", Short: "Service management"}
	list := &cobra.Command{Use: "list", Short: "List services"}
	service.AddCommand(list)
	root.AddCommand(service)

	// unrelated group — should be excluded.
	colony := &cobra.Command{Use: "colony", Short: "Colony management"}
	root.AddCommand(colony)

	return root
}

func TestGenerateCLIReference(t *testing.T) {
	root := buildTestRoot()
	ref := GenerateCLIReference(root)

	t.Run("includes query commands", func(t *testing.T) {
		assert.Contains(t, ref, "query summary")
		assert.Contains(t, ref, "query traces")
	})

	t.Run("includes debug commands", func(t *testing.T) {
		assert.Contains(t, ref, "debug attach")
	})

	t.Run("includes service commands", func(t *testing.T) {
		assert.Contains(t, ref, "service list")
	})

	t.Run("excludes unrelated top-level groups", func(t *testing.T) {
		assert.NotContains(t, ref, "colony")
	})

	t.Run("contains header note about --format json", func(t *testing.T) {
		assert.Contains(t, ref, "--format json")
	})
}
