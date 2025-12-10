package debug

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"

	colonypb "github.com/coral-mesh/coral/coral/colony/v1"
	"github.com/coral-mesh/coral/coral/colony/v1/colonyv1connect"
)

func NewSearchCmd() *cobra.Command {
	var (
		serviceName string
		maxResults  int32
		format      string
	)

	cmd := &cobra.Command{
		Use:   "search <query>",
		Short: "Search for functions",
		Long:  "Search for functions to debug using semantic search.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			query := args[0]
			ctx := context.Background()

			colonyAddr, err := getColonyURL()
			if err != nil {
				return fmt.Errorf("failed to resolve colony address: %w", err)
			}

			client := colonyv1connect.NewDebugServiceClient(http.DefaultClient, colonyAddr)

			req := &colonypb.QueryFunctionsRequest{
				ServiceName:    serviceName,
				Query:          query,
				MaxResults:     maxResults,
				IncludeMetrics: true,
			}

			resp, err := client.QueryFunctions(ctx, connect.NewRequest(req))
			if err != nil {
				return fmt.Errorf("failed to query functions: %w", err)
			}

			if format == "json" {
				data, _ := json.MarshalIndent(resp.Msg, "", "  ")
				fmt.Println(string(data))
				return nil
			}

			// Text format output
			if len(resp.Msg.Results) == 0 {
				fmt.Println("No functions found matching query.")
				if resp.Msg.Suggestion != "" {
					fmt.Printf("\nSuggestion: %s\n", resp.Msg.Suggestion)
				}
				return nil
			}

			fmt.Printf("Found %d function(s):\n\n", len(resp.Msg.Results))
			for i, result := range resp.Msg.Results {
				fmt.Printf("%d. %s\n", i+1, result.Function.Name)
				if result.Function.Package != "" {
					fmt.Printf("   Package: %s\n", result.Function.Package)
				}
				if result.Function.File != "" {
					fmt.Printf("   File:    %s:%d\n", result.Function.File, result.Function.Line)
				}
				if result.Instrumentation != nil {
					fmt.Printf("   Probeable: %v\n", result.Instrumentation.IsProbeable)
				}
				if result.Metrics != nil {
					fmt.Printf("   P95: %s\n", result.Metrics.P95.AsDuration().String())
				}
				fmt.Println()
			}

			if resp.Msg.Suggestion != "" {
				fmt.Printf("Suggestion: %s\n", resp.Msg.Suggestion)
			}

			return nil
		},
	}

	cmd.Flags().StringVarP(&serviceName, "service", "s", "", "Filter by service name")
	cmd.Flags().Int32VarP(&maxResults, "max-results", "n", 20, "Maximum number of results (max: 50)")
	cmd.Flags().StringVar(&format, "format", "text", "Output format (text, json)")

	return cmd
}

func NewInfoCmd() *cobra.Command {
	var (
		serviceName  string
		functionName string
		format       string
	)

	cmd := &cobra.Command{
		Use:   "info",
		Short: "Get function details",
		Long:  "Get detailed information about a specific function including metrics.",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()

			colonyAddr, err := getColonyURL()
			if err != nil {
				return fmt.Errorf("failed to resolve colony address: %w", err)
			}

			client := colonyv1connect.NewDebugServiceClient(http.DefaultClient, colonyAddr)

			req := &colonypb.QueryFunctionsRequest{
				ServiceName:    serviceName,
				Query:          functionName,
				MaxResults:     1,
				IncludeMetrics: true,
			}

			resp, err := client.QueryFunctions(ctx, connect.NewRequest(req))
			if err != nil {
				return fmt.Errorf("failed to query function: %w", err)
			}

			if len(resp.Msg.Results) == 0 {
				return fmt.Errorf("function not found: %s", functionName)
			}

			if format == "json" {
				data, _ := json.MarshalIndent(resp.Msg.Results[0], "", "  ")
				fmt.Println(string(data))
				return nil
			}

			// Text format output
			result := resp.Msg.Results[0]
			fmt.Printf("Function: %s\n", result.Function.Name)
			if result.Function.Package != "" {
				fmt.Printf("Package:  %s\n", result.Function.Package)
			}
			if result.Function.File != "" {
				fmt.Printf("File:     %s:%d\n", result.Function.File, result.Function.Line)
			}
			if result.Function.Offset != "" {
				fmt.Printf("Offset:   %s\n", result.Function.Offset)
			}

			if result.Instrumentation != nil {
				fmt.Printf("\nInstrumentation:\n")
				fmt.Printf("  Probeable:       %v\n", result.Instrumentation.IsProbeable)
				fmt.Printf("  Has DWARF:       %v\n", result.Instrumentation.HasDwarf)
				fmt.Printf("  Currently Probed: %v\n", result.Instrumentation.CurrentlyProbed)
			}

			if result.Metrics != nil {
				fmt.Printf("\nMetrics:\n")
				fmt.Printf("  Source:          %s\n", result.Metrics.Source)
				if result.Metrics.P50 != nil {
					fmt.Printf("  P50:             %s\n", result.Metrics.P50.AsDuration().String())
				}
				if result.Metrics.P95 != nil {
					fmt.Printf("  P95:             %s\n", result.Metrics.P95.AsDuration().String())
				}
				if result.Metrics.P99 != nil {
					fmt.Printf("  P99:             %s\n", result.Metrics.P99.AsDuration().String())
				}
				fmt.Printf("  Calls/min:       %.2f\n", result.Metrics.CallsPerMin)
				fmt.Printf("  Error rate:      %.2f%%\n", result.Metrics.ErrorRate*100)
			} else {
				fmt.Printf("\nNo metrics available for this function.\n")
			}

			if result.Suggestion != "" {
				fmt.Printf("\n%s\n", result.Suggestion)
			}

			return nil
		},
	}

	cmd.Flags().StringVarP(&serviceName, "service", "s", "", "Service name (required)")
	cmd.Flags().StringVarP(&functionName, "function", "f", "", "Function name (required)")
	cmd.Flags().StringVar(&format, "format", "text", "Output format (text, json)")

	if err := cmd.MarkFlagRequired("service"); err != nil {
		fmt.Printf("failed to mark flag as required: %v\n", err)
	}
	if err := cmd.MarkFlagRequired("function"); err != nil {
		fmt.Printf("failed to mark flag as required: %v\n", err)
	}

	return cmd
}

func NewProfileCmd() *cobra.Command {
	var (
		serviceName  string
		query        string
		strategy     string
		maxFunctions int32
		duration     string
		async        bool
		sampleRate   float64
		format       string
	)

	cmd := &cobra.Command{
		Use:   "profile",
		Short: "Auto-profile functions",
		Long:  "Automatically profile multiple functions with batch instrumentation.",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()

			colonyAddr, err := getColonyURL()
			if err != nil {
				return fmt.Errorf("failed to resolve colony address: %w", err)
			}

			client := colonyv1connect.NewDebugServiceClient(http.DefaultClient, colonyAddr)

			// Parse duration
			durationProto, err := parseDuration(duration)
			if err != nil {
				return fmt.Errorf("invalid duration: %w", err)
			}

			req := &colonypb.ProfileFunctionsRequest{
				ServiceName:  serviceName,
				Query:        query,
				Strategy:     strategy,
				MaxFunctions: maxFunctions,
				Duration:     durationProto,
				Async:        async,
				SampleRate:   sampleRate,
			}

			fmt.Printf("Starting profiling session for query '%s' in service '%s'...\n", query, serviceName)

			resp, err := client.ProfileFunctions(ctx, connect.NewRequest(req))
			if err != nil {
				return fmt.Errorf("failed to profile functions: %w", err)
			}

			if format == "json" {
				data, _ := json.MarshalIndent(resp.Msg, "", "  ")
				fmt.Println(string(data))
				return nil
			}

			// Text format output
			fmt.Printf("\nSession ID: %s\n", resp.Msg.SessionId)
			fmt.Printf("Status:     %s\n", resp.Msg.Status)
			if resp.Msg.Summary != nil {
				fmt.Printf("\nSummary:\n")
				fmt.Printf("  Functions Selected: %d\n", resp.Msg.Summary.FunctionsSelected)
				fmt.Printf("  Functions Probed:   %d\n", resp.Msg.Summary.FunctionsProbed)
				if resp.Msg.Summary.ProbesFailed > 0 {
					fmt.Printf("  Probes Failed:      %d\n", resp.Msg.Summary.ProbesFailed)
				}
				if resp.Msg.Summary.Duration != nil {
					fmt.Printf("  Duration:           %s\n", resp.Msg.Summary.Duration.AsDuration().String())
				}
			}

			if len(resp.Msg.Bottlenecks) > 0 {
				fmt.Printf("\nBottlenecks:\n")
				for _, b := range resp.Msg.Bottlenecks {
					fmt.Printf("  - %s (%s): %s\n", b.Function, b.Severity, b.P95.AsDuration().String())
					fmt.Printf("    %s\n", b.Recommendation)
				}
			}

			if resp.Msg.Recommendation != "" {
				fmt.Printf("\nRecommendation: %s\n", resp.Msg.Recommendation)
			}

			if len(resp.Msg.NextSteps) > 0 {
				fmt.Printf("\nNext Steps:\n")
				for _, step := range resp.Msg.NextSteps {
					fmt.Printf("  - %s\n", step)
				}
			}

			return nil
		},
	}

	cmd.Flags().StringVarP(&serviceName, "service", "s", "", "Service name (required)")
	cmd.Flags().StringVarP(&query, "query", "q", "", "Search query (required)")
	cmd.Flags().StringVar(&strategy, "strategy", "critical-path", "Selection strategy (critical-path, all, entry-points, leaf-functions)")
	cmd.Flags().Int32VarP(&maxFunctions, "max-functions", "n", 20, "Maximum functions to profile (max: 50)")
	cmd.Flags().StringVarP(&duration, "duration", "d", "60s", "Duration of profiling session")
	cmd.Flags().BoolVar(&async, "async", false, "Return immediately without waiting")
	cmd.Flags().Float64Var(&sampleRate, "sample-rate", 1.0, "Event sampling rate (0.1-1.0)")
	cmd.Flags().StringVar(&format, "format", "text", "Output format (text, json)")

	if err := cmd.MarkFlagRequired("service"); err != nil {
		fmt.Printf("failed to mark flag as required: %v\n", err)
	}
	if err := cmd.MarkFlagRequired("query"); err != nil {
		fmt.Printf("failed to mark flag as required: %v\n", err)
	}

	return cmd
}
