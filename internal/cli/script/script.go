// Package script implements the coral script subcommands for managing
// TypeScript investigation scripts stored in ~/.coral/scripts/.
package script

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/coral-mesh/coral/internal/cli/helpers"
)

// NewScriptCmd creates the 'coral script' command group.
func NewScriptCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "script",
		Short: "Manage investigation scripts",
		Long: `Manage TypeScript investigation scripts stored in ~/.coral/scripts/.

Scripts let you encode multi-step investigations — parallel queries, conditional
logic, or custom aggregations — that a single coral CLI command cannot express.
Write a script once, run it any time.

See also: coral run --file ~/.coral/scripts/<name>.ts`,
	}

	cmd.AddCommand(newWriteCmd())
	cmd.AddCommand(newListCmd())
	cmd.AddCommand(newRemoveCmd())
	return cmd
}

// scriptsDir returns the path to the user scripts directory.
func scriptsDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to determine home directory: %w", err)
	}
	return filepath.Join(home, ".coral", "scripts"), nil
}

// ensureScriptsDir creates ~/.coral/scripts/ if it does not exist.
func ensureScriptsDir() (string, error) {
	dir, err := scriptsDir()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", fmt.Errorf("failed to create scripts directory: %w", err)
	}
	return dir, nil
}

// newWriteCmd creates the 'coral script write' command.
func newWriteCmd() *cobra.Command {
	var (
		name    string
		content string
		file    string
	)

	cmd := &cobra.Command{
		Use:   "write",
		Short: "Write a TypeScript script to ~/.coral/scripts/<name>.ts",
		Long: `Write a TypeScript script to ~/.coral/scripts/<name>.ts.

The script name must consist of letters, digits, hyphens, and underscores.
Use --content to provide the script inline, or --file to read from a local file.

Examples:
  coral script write --name latency-report --content "import {...} from '@coral/sdk'"
  coral script write --name latency-report --file report.ts
  coral run --file ~/.coral/scripts/latency-report.ts`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if !validName(name) {
				return fmt.Errorf("invalid script name %q: use only letters, digits, hyphens, and underscores", name)
			}

			// Read content from file if --file is provided.
			if file != "" {
				data, err := os.ReadFile(file) // #nosec G304
				if err != nil {
					return fmt.Errorf("failed to read %q: %w", file, err)
				}
				content = string(data)
			}

			if strings.TrimSpace(content) == "" {
				return fmt.Errorf("script content is empty: provide --content or --file")
			}

			dir, err := ensureScriptsDir()
			if err != nil {
				return err
			}

			scriptPath := filepath.Join(dir, name+".ts")
			if err := os.WriteFile(scriptPath, []byte(content), 0600); err != nil {
				return fmt.Errorf("failed to write script: %w", err)
			}

			cmd.Printf("Wrote %s\n", scriptPath)
			return nil
		},
	}

	cmd.Flags().StringVar(&name, "name", "", "Script name (required)")
	cmd.Flags().StringVar(&content, "content", "", "Script content (TypeScript source)")
	cmd.Flags().StringVar(&file, "file", "", "Read content from this file instead of --content")
	_ = cmd.MarkFlagRequired("name")
	return cmd
}

// scriptInfo holds metadata about a stored script for list output.
type scriptInfo struct {
	Name      string    `json:"name"       header:"NAME"`
	Path      string    `json:"path"       header:"PATH"`
	SizeBytes int64     `json:"size_bytes" header:"SIZE"`
	UpdatedAt time.Time `json:"updated_at" header:"UPDATED"`
}

// newListCmd creates the 'coral script list' command.
func newListCmd() *cobra.Command {
	var format string

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List scripts in ~/.coral/scripts/",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := helpers.ValidateFormat(format, []helpers.OutputFormat{
				helpers.FormatTable, helpers.FormatJSON,
			}); err != nil {
				return err
			}

			dir, err := scriptsDir()
			if err != nil {
				return err
			}

			entries, err := os.ReadDir(dir)
			if os.IsNotExist(err) {
				entries = nil
			} else if err != nil {
				return fmt.Errorf("failed to read scripts directory: %w", err)
			}

			var scripts []scriptInfo
			for _, e := range entries {
				if e.IsDir() || !strings.HasSuffix(e.Name(), ".ts") {
					continue
				}
				info, err := e.Info()
				if err != nil {
					continue
				}
				scripts = append(scripts, scriptInfo{
					Name:      strings.TrimSuffix(e.Name(), ".ts"),
					Path:      filepath.Join(dir, e.Name()),
					SizeBytes: info.Size(),
					UpdatedAt: info.ModTime(),
				})
			}

			if helpers.OutputFormat(format) == helpers.FormatJSON {
				if scripts == nil {
					scripts = []scriptInfo{}
				}
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(scripts)
			}

			// Table output.
			if len(scripts) == 0 {
				cmd.Println("No scripts. Use 'coral script write' to create one.")
				return nil
			}
			if _, err := fmt.Fprintf(cmd.OutOrStdout(), "%-30s  %8s  %s\n", "NAME", "SIZE", "UPDATED"); err != nil {
				return err
			}
			for _, s := range scripts {
				if _, err := fmt.Fprintf(cmd.OutOrStdout(), "%-30s  %8d  %s\n",
					s.Name, s.SizeBytes, s.UpdatedAt.Format("2006-01-02 15:04")); err != nil {
					return err
				}
			}
			return nil
		},
	}

	helpers.AddFormatFlag(cmd, &format, helpers.FormatTable, []helpers.OutputFormat{
		helpers.FormatTable, helpers.FormatJSON,
	})
	return cmd
}

// newRemoveCmd creates the 'coral script remove' command.
func newRemoveCmd() *cobra.Command {
	var name string

	cmd := &cobra.Command{
		Use:   "remove",
		Short: "Remove a script from ~/.coral/scripts/",
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, err := scriptsDir()
			if err != nil {
				return err
			}

			scriptPath := filepath.Join(dir, name+".ts")
			if err := os.Remove(scriptPath); err != nil {
				if os.IsNotExist(err) {
					return fmt.Errorf("script %q not found", name)
				}
				return fmt.Errorf("failed to remove script: %w", err)
			}

			cmd.Printf("Removed %s\n", scriptPath)
			return nil
		},
	}

	cmd.Flags().StringVar(&name, "name", "", "Script name (required)")
	_ = cmd.MarkFlagRequired("name")
	return cmd
}

// validName returns true if name consists only of letters, digits, hyphens,
// and underscores and is non-empty.
func validName(name string) bool {
	if name == "" {
		return false
	}
	for _, r := range name {
		isLetter := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
		isDigit := r >= '0' && r <= '9'
		isSep := r == '-' || r == '_'
		if !isLetter && !isDigit && !isSep {
			return false
		}
	}
	return true
}
