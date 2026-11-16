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

	// services group — the real root command is named "services", not "service".
	services := &cobra.Command{Use: "services", Short: "List and manage services"}
	list := &cobra.Command{Use: "list", Short: "List services"}
	services.AddCommand(list)
	root.AddCommand(services)

	// triage — a top-level leaf, not a group (RFD 114).
	triage := &cobra.Command{Use: "triage [service]", Short: "Diagnose the worst degraded service"}
	root.AddCommand(triage)

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

	t.Run("includes services commands", func(t *testing.T) {
		assert.Contains(t, ref, "services list")
	})

	t.Run("includes the triage top-level leaf", func(t *testing.T) {
		assert.Contains(t, ref, "triage")
		assert.Contains(t, ref, "Diagnose the worst degraded service")
	})

	t.Run("excludes unrelated top-level groups", func(t *testing.T) {
		assert.NotContains(t, ref, "colony")
	})

	t.Run("contains header note about --format json", func(t *testing.T) {
		assert.Contains(t, ref, "--format json")
	})
}
