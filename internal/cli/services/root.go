// Package services provides the root-level service management commands.
package services

import (
	"github.com/spf13/cobra"

	"github.com/coral-mesh/coral/internal/cli/agent"
)

// NewCmd creates the coral services command. With no subcommand it lists the
// services visible to the current colony.
func NewCmd() *cobra.Command {
	options := &listOptions{}
	cmd := &cobra.Command{
		Use:   "services",
		Short: "List and manage services",
		Long:  "List services across the colony, on a specific agent, or on the local agent.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runList(cmd, options)
		},
	}
	addListFlags(cmd, options)

	list := &cobra.Command{
		Use:   "list",
		Short: "List services",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runList(cmd, options)
		},
	}
	cmd.AddCommand(list)
	cmd.AddCommand(NewWatchCmd())
	return cmd
}

// NewWatchCmd creates the service enrichment command. The implementation is
// shared with the established connect command so both entry points remain
// behaviorally identical.
func NewWatchCmd() *cobra.Command {
	cmd := agent.NewConnectCmd()
	cmd.Use = "watch <service-spec>..."
	cmd.Short = "Watch and enrich one or more services"
	cmd.Long = "Register services for enriched observation by a running Coral agent."
	return cmd
}

// NewConnectAliasCmd creates the permanent hidden coral connect alias.
func NewConnectAliasCmd() *cobra.Command {
	cmd := NewWatchCmd()
	cmd.Use = "connect <service-spec>..."
	cmd.Hidden = true
	return cmd
}

// NewLegacyServiceCmd creates the permanent hidden coral colony service alias.
func NewLegacyServiceCmd() *cobra.Command {
	options := &listOptions{}
	cmd := &cobra.Command{Use: "service", Hidden: true}
	list := &cobra.Command{
		Use:    "list",
		Short:  "List services",
		Hidden: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runList(cmd, options)
		},
	}
	addListFlags(list, options)
	cmd.AddCommand(list)
	return cmd
}
