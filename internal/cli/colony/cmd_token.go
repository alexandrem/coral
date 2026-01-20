package colony

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/coral-mesh/coral/internal/auth"
	"github.com/coral-mesh/coral/internal/config"
)

// newTokenCmd creates the token command group for managing API tokens (RFD 031).
func newTokenCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "token",
		Short: "Manage API tokens for public endpoint access (RFD 031)",
		Long: `Manage API tokens for the colony's public HTTPS endpoint.

Tokens are used to authenticate CLI clients, external integrations (Slack bots,
CI/CD pipelines), and IDE extensions when connecting to the public endpoint.

Before using tokens, ensure the public endpoint is enabled in your colony config:

  public_endpoint:
    enabled: true
    host: 127.0.0.1  # localhost-only for dev, 0.0.0.0 for production
    port: 8443

Permissions:
  status  - Read colony status, agents, topology
  query   - Query metrics, traces, logs
  analyze - AI analysis (may trigger shell commands)
  debug   - Attach live eBPF probes
  admin   - Full administrative access`,
	}

	cmd.AddCommand(newTokenCreateCmd())
	cmd.AddCommand(newTokenListCmd())
	cmd.AddCommand(newTokenShowCmd())
	cmd.AddCommand(newTokenRevokeCmd())
	cmd.AddCommand(newTokenDeleteCmd())

	return cmd
}

func newTokenCreateCmd() *cobra.Command {
	var (
		colonyID    string
		permissions string
		rateLimit   string
		recreate    bool
	)

	cmd := &cobra.Command{
		Use:   "create <token-id>",
		Short: "Create a new API token",
		Long: `Create a new API token for the public endpoint.

The token will be displayed ONCE after creation. Save it securely - it cannot
be retrieved later.

Examples:
  # Create token with basic permissions
  coral colony token create cli-dev --permissions status,query

  # Create token with rate limit
  coral colony token create ci-cd --permissions status,query --rate-limit 100/hour

  # Create token with analyze permission (may run shell commands)
  coral colony token create ide-vscode --permissions status,query,analyze

  # Create admin token (full access)
  coral colony token create admin-token --permissions admin`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			tokenID := args[0]

			// Resolve colony ID.
			resolver, err := config.NewResolver()
			if err != nil {
				return fmt.Errorf("failed to create config resolver: %w", err)
			}

			if colonyID == "" {
				colonyID, err = resolver.ResolveColonyID()
				if err != nil {
					return fmt.Errorf("failed to resolve colony: %w", err)
				}
			}

			// Load colony config to get tokens file path.
			loader, err := config.NewLoader()
			if err != nil {
				return fmt.Errorf("failed to create config loader: %w", err)
			}

			colonyConfig, err := loader.LoadColonyConfig(colonyID)
			if err != nil {
				return fmt.Errorf("failed to load colony config: %w", err)
			}

			// Check if public endpoint is enabled.
			if !colonyConfig.PublicEndpoint.Enabled {
				return fmt.Errorf("public endpoint is not enabled for colony %q\n\nEnable it in your colony config:\n  public_endpoint:\n    enabled: true", colonyID)
			}

			// Get tokens file path.
			colonyDir := loader.ColonyDir(colonyID)
			tokensFile := colonyConfig.PublicEndpoint.Auth.TokensFile
			if tokensFile == "" {
				tokensFile = filepath.Join(colonyDir, "tokens.yaml")
			}

			// Parse permissions.
			perms := parsePermissions(permissions)
			if len(perms) == 0 {
				return fmt.Errorf("at least one permission required\n\nAvailable permissions: status, query, analyze, debug, admin")
			}

			// Initialize token store.
			tokenStore := auth.NewTokenStore(tokensFile)

			// If recreate is set, delete the token if it exists.
			if recreate {
				if _, exists := tokenStore.GetToken(tokenID); exists {
					if err := tokenStore.DeleteToken(tokenID); err != nil {
						return fmt.Errorf("failed to delete existing token for recreation: %w", err)
					}
				}
			}

			// Create token.
			tokenInfo, err := tokenStore.GenerateToken(tokenID, perms, rateLimit)
			if err != nil {
				return fmt.Errorf("failed to create token: %w", err)
			}

			fmt.Println("Token created successfully!")
			fmt.Println()
			fmt.Printf("Token ID:    %s\n", tokenInfo.TokenID)
			fmt.Printf("Permissions: %s\n", permissions)
			if rateLimit != "" {
				fmt.Printf("Rate Limit:  %s\n", rateLimit)
			}
			fmt.Println()
			fmt.Println("⚠️  Save this token - it will NOT be shown again!")
			fmt.Println()
			fmt.Printf("Token: %s\n", tokenInfo.Token)
			fmt.Println()
			fmt.Println("Usage:")
			fmt.Printf("  export CORAL_API_TOKEN=%s\n", tokenInfo.Token)
			fmt.Println("  coral status")

			return nil
		},
	}

	cmd.Flags().StringVar(&colonyID, "colony", "", "Colony ID (defaults to current colony)")
	cmd.Flags().StringVar(&permissions, "permissions", "status,query", "Comma-separated permissions (status,query,analyze,debug,admin)")
	cmd.Flags().StringVar(&rateLimit, "rate-limit", "", "Rate limit (e.g., 100/hour, 50/minute)")
	cmd.Flags().BoolVar(&recreate, "recreate", false, "Recreate token if it already exists (invalidates old token)")

	return cmd
}

func newTokenListCmd() *cobra.Command {
	var colonyID string

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List all API tokens",
		Long: `List all API tokens for the colony's public endpoint.

Note: Token values are never displayed - only metadata.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Resolve colony ID.
			resolver, err := config.NewResolver()
			if err != nil {
				return fmt.Errorf("failed to create config resolver: %w", err)
			}

			if colonyID == "" {
				colonyID, err = resolver.ResolveColonyID()
				if err != nil {
					return fmt.Errorf("failed to resolve colony: %w", err)
				}
			}

			// Load colony config.
			loader, err := config.NewLoader()
			if err != nil {
				return fmt.Errorf("failed to create config loader: %w", err)
			}

			colonyConfig, err := loader.LoadColonyConfig(colonyID)
			if err != nil {
				return fmt.Errorf("failed to load colony config: %w", err)
			}

			// Get tokens file path.
			colonyDir := loader.ColonyDir(colonyID)
			tokensFile := colonyConfig.PublicEndpoint.Auth.TokensFile
			if tokensFile == "" {
				tokensFile = filepath.Join(colonyDir, "tokens.yaml")
			}

			// Load tokens.
			tokenStore := auth.NewTokenStore(tokensFile)
			tokens := tokenStore.ListTokens()

			if len(tokens) == 0 {
				fmt.Println("No API tokens found.")
				fmt.Println()
				fmt.Println("Create a token with:")
				fmt.Println("  coral colony token create <token-id> --permissions status,query")
				return nil
			}

			fmt.Printf("API Tokens for colony %q:\n\n", colonyID)

			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			_, _ = fmt.Fprintln(w, "TOKEN ID\tPERMISSIONS\tRATE LIMIT\tCREATED\tLAST USED")
			_, _ = fmt.Fprintln(w, "--------\t-----------\t----------\t-------\t---------")

			for _, t := range tokens {
				perms := make([]string, len(t.Permissions))
				for i, p := range t.Permissions {
					perms[i] = string(p)
				}

				rateLimit := "-"
				if t.RateLimit != "" {
					rateLimit = t.RateLimit
				}

				lastUsed := "-"
				if t.LastUsedAt != nil {
					lastUsed = t.LastUsedAt.Format("2006-01-02 15:04")
				}

				_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n",
					t.TokenID,
					strings.Join(perms, ","),
					rateLimit,
					t.CreatedAt.Format("2006-01-02 15:04"),
					lastUsed,
				)
			}

			if err = w.Flush(); err != nil {
				return fmt.Errorf("failed to flush output: %w", err)
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&colonyID, "colony", "", "Colony ID (defaults to current colony)")

	return cmd
}

func newTokenShowCmd() *cobra.Command {
	var colonyID string

	cmd := &cobra.Command{
		Use:   "show <token-id>",
		Short: "Show API token metadata",
		Long: `Show metadata for a specific API token.

Note: The actual token value is hashed and cannot be retrieved.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			tokenID := args[0]

			// Resolve colony ID.
			resolver, err := config.NewResolver()
			if err != nil {
				return fmt.Errorf("failed to create config resolver: %w", err)
			}

			if colonyID == "" {
				colonyID, err = resolver.ResolveColonyID()
				if err != nil {
					return fmt.Errorf("failed to resolve colony: %w", err)
				}
			}

			// Load colony config.
			loader, err := config.NewLoader()
			if err != nil {
				return fmt.Errorf("failed to create config loader: %w", err)
			}

			colonyConfig, err := loader.LoadColonyConfig(colonyID)
			if err != nil {
				return fmt.Errorf("failed to load colony config: %w", err)
			}

			// Get tokens file path.
			colonyDir := loader.ColonyDir(colonyID)
			tokensFile := colonyConfig.PublicEndpoint.Auth.TokensFile
			if tokensFile == "" {
				tokensFile = filepath.Join(colonyDir, "tokens.yaml")
			}

			// Load tokens.
			tokenStore := auth.NewTokenStore(tokensFile)
			token, exists := tokenStore.GetToken(tokenID)
			if !exists {
				return fmt.Errorf("token %q not found", tokenID)
			}

			fmt.Printf("Token ID:    %s\n", token.TokenID)

			perms := make([]string, len(token.Permissions))
			for i, p := range token.Permissions {
				perms[i] = string(p)
			}
			fmt.Printf("Permissions: %s\n", strings.Join(perms, ","))

			rateLimit := "-"
			if token.RateLimit != "" {
				rateLimit = token.RateLimit
			}
			fmt.Printf("Rate Limit:  %s\n", rateLimit)

			fmt.Printf("Created:     %s\n", token.CreatedAt.Format("2006-01-02 15:04:05"))

			lastUsed := "-"
			if token.LastUsedAt != nil {
				lastUsed = token.LastUsedAt.Format("2006-01-02 15:04:05")
			}
			fmt.Printf("Last Used:   %s\n", lastUsed)

			status := "active"
			if token.Revoked {
				status = "revoked"
			}
			fmt.Printf("Status:      %s\n", status)

			return nil
		},
	}

	cmd.Flags().StringVar(&colonyID, "colony", "", "Colony ID (defaults to current colony)")

	return cmd
}

func newTokenRevokeCmd() *cobra.Command {
	var (
		colonyID string
		force    bool
	)

	cmd := &cobra.Command{
		Use:   "revoke <token-id>",
		Short: "Revoke an API token",
		Long: `Revoke an API token, immediately invalidating it.

Revoked tokens can no longer authenticate to the public endpoint.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			tokenID := args[0]

			// Resolve colony ID.
			resolver, err := config.NewResolver()
			if err != nil {
				return fmt.Errorf("failed to create config resolver: %w", err)
			}

			if colonyID == "" {
				colonyID, err = resolver.ResolveColonyID()
				if err != nil {
					return fmt.Errorf("failed to resolve colony: %w", err)
				}
			}

			// Load colony config.
			loader, err := config.NewLoader()
			if err != nil {
				return fmt.Errorf("failed to create config loader: %w", err)
			}

			colonyConfig, err := loader.LoadColonyConfig(colonyID)
			if err != nil {
				return fmt.Errorf("failed to load colony config: %w", err)
			}

			// Get tokens file path.
			colonyDir := loader.ColonyDir(colonyID)
			tokensFile := colonyConfig.PublicEndpoint.Auth.TokensFile
			if tokensFile == "" {
				tokensFile = filepath.Join(colonyDir, "tokens.yaml")
			}

			// Load tokens.
			tokenStore := auth.NewTokenStore(tokensFile)

			// Check if token exists.
			token, exists := tokenStore.GetToken(tokenID)
			if !exists {
				return fmt.Errorf("token %q not found", tokenID)
			}

			if token.Revoked {
				return fmt.Errorf("token %q is already revoked", tokenID)
			}

			// Confirm unless force flag is set.
			if !force {
				fmt.Printf("Are you sure you want to revoke token %q? This cannot be undone.\n", tokenID)
				fmt.Print("Type 'yes' to confirm: ")

				var confirm string
				if _, err := fmt.Scanln(&confirm); err != nil {
					return fmt.Errorf("failed to read user confirmation: %w", err)
				}
				if confirm != "yes" {
					fmt.Println("Cancelled.")
					return nil
				}
			}

			// Revoke the token.
			if err := tokenStore.RevokeToken(tokenID); err != nil {
				return fmt.Errorf("failed to revoke token: %w", err)
			}

			fmt.Printf("Token %q has been revoked.\n", tokenID)
			return nil
		},
	}

	cmd.Flags().StringVar(&colonyID, "colony", "", "Colony ID (defaults to current colony)")
	cmd.Flags().BoolVar(&force, "force", false, "Skip confirmation prompt")

	return cmd
}

func newTokenDeleteCmd() *cobra.Command {
	var (
		colonyID string
		force    bool
	)

	cmd := &cobra.Command{
		Use:   "delete <token-id>",
		Short: "Permanently delete an API token",
		Long: `Permanently delete an API token from the store.
 
Unlike revoke, delete removes the token record entirely.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			tokenID := args[0]

			// Resolve colony ID.
			resolver, err := config.NewResolver()
			if err != nil {
				return fmt.Errorf("failed to create config resolver: %w", err)
			}

			if colonyID == "" {
				colonyID, err = resolver.ResolveColonyID()
				if err != nil {
					return fmt.Errorf("failed to resolve colony: %w", err)
				}
			}

			// Load colony config.
			loader, err := config.NewLoader()
			if err != nil {
				return fmt.Errorf("failed to create config loader: %w", err)
			}

			colonyConfig, err := loader.LoadColonyConfig(colonyID)
			if err != nil {
				return fmt.Errorf("failed to load colony config: %w", err)
			}

			// Get tokens file path.
			colonyDir := loader.ColonyDir(colonyID)
			tokensFile := colonyConfig.PublicEndpoint.Auth.TokensFile
			if tokensFile == "" {
				tokensFile = filepath.Join(colonyDir, "tokens.yaml")
			}

			// Load tokens.
			tokenStore := auth.NewTokenStore(tokensFile)

			// Check if token exists.
			if _, exists := tokenStore.GetToken(tokenID); !exists {
				return fmt.Errorf("token %q not found", tokenID)
			}

			// Confirm unless force flag is set.
			if !force {
				fmt.Printf("Are you sure you want to PERMANENTLY DELETE token %q? This cannot be undone.\n", tokenID)
				fmt.Print("Type 'yes' to confirm: ")

				var confirm string
				if _, err := fmt.Scanln(&confirm); err != nil {
					return fmt.Errorf("failed to read user confirmation: %w", err)
				}
				if confirm != "yes" {
					fmt.Println("Cancelled.")
					return nil
				}
			}

			// Delete the token.
			if err := tokenStore.DeleteToken(tokenID); err != nil {
				return fmt.Errorf("failed to delete token: %w", err)
			}

			fmt.Printf("Token %q has been deleted.\n", tokenID)
			return nil
		},
	}

	cmd.Flags().StringVar(&colonyID, "colony", "", "Colony ID (defaults to current colony)")
	cmd.Flags().BoolVar(&force, "force", false, "Skip confirmation prompt")

	return cmd
}

// parsePermissions parses a comma-separated string of permissions.
func parsePermissions(s string) []auth.Permission {
	parts := strings.Split(s, ",")
	perms := make([]auth.Permission, 0, len(parts))

	for _, p := range parts {
		p = strings.TrimSpace(strings.ToLower(p))
		perm := auth.ParsePermission(p)
		if perm != "" {
			perms = append(perms, perm)
		}
	}

	return perms
}
