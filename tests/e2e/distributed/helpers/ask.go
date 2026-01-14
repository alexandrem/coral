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
