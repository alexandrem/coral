package ask

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// GenerateCLIReference walks the Cobra command tree and returns a compact
// plain-text reference of agent-facing coral commands (RFD 100).
// This is served as the coral://cli/reference resource in CLI dispatch mode.
func GenerateCLIReference(root *cobra.Command) string {
	var sb strings.Builder
	sb.WriteString("coral CLI reference — --format json is appended automatically by coral_cli.\n\n")

	// Include only the command groups the agent is likely to call.
	relevant := map[string]bool{
		"query": true, "debug": true, "service": true,
		"script": true, "run": true,
	}

	for _, cmd := range root.Commands() {
		if !relevant[cmd.Name()] || cmd.Hidden {
			continue
		}
		writeRefGroup(&sb, cmd)
	}

	return sb.String()
}

// writeRefGroup writes a group of leaf commands under a parent.
func writeRefGroup(sb *strings.Builder, parent *cobra.Command) {
	for _, cmd := range parent.Commands() {
		if cmd.Hidden {
			continue
		}
		if cmd.HasSubCommands() {
			writeRefGroup(sb, cmd)
			continue
		}
		writeRefLeaf(sb, cmd)
	}
}

// writeRefLeaf writes one leaf command entry.
func writeRefLeaf(sb *strings.Builder, cmd *cobra.Command) {
	// Command path without the binary name.
	path := strings.TrimPrefix(cmd.CommandPath(), "coral ")

	sb.WriteString(path)

	// Collect non-trivial flags (skip format, help, verbose, colony).
	var flags []string
	cmd.Flags().VisitAll(func(f *pflag.Flag) {
		switch f.Name {
		case "format", "help", "verbose", "colony":
			return
		}
		typ := f.Value.Type()
		switch typ {
		case "string":
			flags = append(flags, fmt.Sprintf("[--%s STR]", f.Name))
		case "int":
			flags = append(flags, fmt.Sprintf("[--%s INT]", f.Name))
		case "bool":
			flags = append(flags, fmt.Sprintf("[--%s]", f.Name))
		case "duration":
			flags = append(flags, fmt.Sprintf("[--%s DUR]", f.Name))
		case "float64":
			flags = append(flags, fmt.Sprintf("[--%s NUM]", f.Name))
		default:
			flags = append(flags, fmt.Sprintf("[--%s]", f.Name))
		}
	})
	if len(flags) > 0 {
		sb.WriteString("  ")
		sb.WriteString(strings.Join(flags, " "))
	}
	sb.WriteString("\n")

	if cmd.Short != "" {
		sb.WriteString("  → " + cmd.Short + "\n")
	}
	sb.WriteString("\n")
}
