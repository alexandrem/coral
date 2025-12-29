package profile

import (
	"fmt"
	"os"

	debugpb "github.com/coral-mesh/coral/coral/colony/v1"
	"github.com/coral-mesh/coral/internal/cli/helpers"
)

// getColonyURL returns the colony URL using shared config resolution.
func getColonyURL() (string, error) {
	return helpers.GetColonyURL("")
}

// printCPUProfileFolded prints the profile in folded stack format.
func printCPUProfileFolded(profile *debugpb.ProfileCPUResponse) error {
	// Print summary to stderr.
	fmt.Fprintf(os.Stderr, "Total samples: %d\n", profile.TotalSamples)
	if profile.LostSamples > 0 {
		fmt.Fprintf(os.Stderr, "Warning: Lost %d samples due to map overflow\n", profile.LostSamples)
	}
	fmt.Fprintf(os.Stderr, "Unique stacks: %d\n", len(profile.Samples))
	fmt.Fprintf(os.Stderr, "\n")

	// Print folded stacks to stdout (for piping to flamegraph.pl).
	for _, sample := range profile.Samples {
		if len(sample.FrameNames) == 0 {
			continue
		}

		// Folded format: frame1;frame2;frame3 count
		// Stack frames should be from outermost (root) to innermost (leaf).
		// Reverse the order since BPF captures innermost first.
		for i := len(sample.FrameNames) - 1; i >= 0; i-- {
			fmt.Print(sample.FrameNames[i])
			if i > 0 {
				fmt.Print(";")
			}
		}
		fmt.Printf(" %d\n", sample.Count)
	}

	return nil
}

// printCPUProfileJSON prints the profile in JSON format.
func printCPUProfileJSON(profile *debugpb.ProfileCPUResponse) error {
	// Simple JSON output without external dependencies.
	fmt.Println("{")
	fmt.Printf("  \"total_samples\": %d,\n", profile.TotalSamples)
	fmt.Printf("  \"lost_samples\": %d,\n", profile.LostSamples)
	fmt.Printf("  \"unique_stacks\": %d,\n", len(profile.Samples))
	fmt.Println("  \"samples\": [")

	for i, sample := range profile.Samples {
		fmt.Println("    {")
		fmt.Println("      \"frames\": [")
		for j, frame := range sample.FrameNames {
			fmt.Printf("        %q", frame)
			if j < len(sample.FrameNames)-1 {
				fmt.Println(",")
			} else {
				fmt.Println()
			}
		}
		fmt.Println("      ],")
		fmt.Printf("      \"count\": %d\n", sample.Count)
		if i < len(profile.Samples)-1 {
			fmt.Println("    },")
		} else {
			fmt.Println("    }")
		}
	}

	fmt.Println("  ]")
	fmt.Println("}")

	return nil
}
