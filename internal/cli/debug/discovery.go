package debug

import (
	"fmt"

	"github.com/spf13/cobra"
)

func NewSearchCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "search <query>",
		Short: "Search for functions (Not Implemented)",
		Long:  "Search for functions to debug. This feature is not yet implemented.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return fmt.Errorf("not implemented. Will be available in a future release (RFD 069)")
		},
	}
}

func NewInfoCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "info",
		Short: "Get function details (Not Implemented)",
		Long:  "Get details about a function. This feature is not yet implemented.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return fmt.Errorf("not implemented. Will be available in a future release (RFD 069)")
		},
	}
}

func NewProfileCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "profile",
		Short: "Auto-profile functions (Not Implemented)",
		Long:  "Automatically profile functions. This feature is not yet implemented.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return fmt.Errorf("not implemented. Will be available in a future release (RFD 069)")
		},
	}
}
