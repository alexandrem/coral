package helpers

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

// AddFormatFlag adds a standard --format/-o flag to a command.
// Validates that the format is in the supportedFormats list.
func AddFormatFlag(cmd *cobra.Command, formatVar *string, defaultFormat OutputFormat, supportedFormats []OutputFormat) {
	formatNames := make([]string, len(supportedFormats))
	for i, f := range supportedFormats {
		formatNames[i] = string(f)
	}

	description := fmt.Sprintf("Output format (%s)", strings.Join(formatNames, ", "))
	cmd.Flags().StringVarP(formatVar, "format", "o", string(defaultFormat), description)

	// Add shell completion for format flag.
	_ = cmd.RegisterFlagCompletionFunc("format", func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		return formatNames, cobra.ShellCompDirectiveNoFileComp
	})
}

// AddColonyFlag adds a standard --colony/-c flag for colony ID selection.
func AddColonyFlag(cmd *cobra.Command, colonyVar *string) {
	cmd.Flags().StringVarP(colonyVar, "colony", "c", "", "Colony ID (overrides auto-detection)")

	// Add shell completion for colony IDs.
	_ = cmd.RegisterFlagCompletionFunc("colony", func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		// TODO: Load colony IDs from config and return for completion.
		return nil, cobra.ShellCompDirectiveNoFileComp
	})
}

// AddVerboseFlag adds a standard --verbose/-v flag.
func AddVerboseFlag(cmd *cobra.Command, verboseVar *bool) {
	cmd.Flags().BoolVarP(verboseVar, "verbose", "v", false, "Verbose output (show additional details)")
}

// ValidateFormat checks if the format is in the supported list.
func ValidateFormat(format string, supported []OutputFormat) error {
	for _, s := range supported {
		if format == string(s) {
			return nil
		}
	}

	supportedNames := make([]string, len(supported))
	for i, s := range supported {
		supportedNames[i] = string(s)
	}

	return fmt.Errorf("unsupported format %q, must be one of: %s",
		format, strings.Join(supportedNames, ", "))
}
