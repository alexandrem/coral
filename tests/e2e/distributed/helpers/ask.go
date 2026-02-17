package helpers

import (
	"context"
)

// RunAsk runs the 'coral ask' command.
func RunAsk(ctx context.Context, env map[string]string, question, model, format string) *CLIResult {
	args := []string{"ask", question}

	if model != "" {
		args = append(args, "--model", model)
	}
	if format != "" {
		args = append(args, "--format", format)
	}

	// Disable streaming for consistent test output
	args = append(args, "--stream=false")

	return RunCLIWithEnv(ctx, env, args...)
}

// RunAskContinue runs 'coral ask' with the --continue flag.
func RunAskContinue(ctx context.Context, env map[string]string, question, model string) *CLIResult {
	args := []string{"ask", question, "--continue", "--stream=false"}

	if model != "" {
		args = append(args, "--model", model)
	}

	return RunCLIWithEnv(ctx, env, args...)
}

// RunAskConfig runs 'coral ask config' non-interactively (RFD 055).
func RunAskConfig(ctx context.Context, env map[string]string, provider, model, apiKeyEnv string, dryRun bool) *CLIResult {
	args := []string{"ask", "config", "--yes"}

	if provider != "" {
		args = append(args, "--provider", provider)
	}
	if model != "" {
		args = append(args, "--model", model)
	}
	if apiKeyEnv != "" {
		args = append(args, "--api-key-env", apiKeyEnv)
	}
	if dryRun {
		args = append(args, "--dry-run")
	}

	return RunCLIWithEnv(ctx, env, args...)
}

// RunAskConfigValidate runs 'coral ask config validate' (RFD 055).
func RunAskConfigValidate(ctx context.Context, env map[string]string) *CLIResult {
	return RunCLIWithEnv(ctx, env, "ask", "config", "validate")
}

// RunAskConfigShow runs 'coral ask config show' (RFD 055).
func RunAskConfigShow(ctx context.Context, env map[string]string) *CLIResult {
	return RunCLIWithEnv(ctx, env, "ask", "config", "show")
}
