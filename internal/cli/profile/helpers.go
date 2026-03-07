package profile

import (
	"fmt"
	"os"

	agentpb "github.com/coral-mesh/coral/coral/agent/v1"
	debugpb "github.com/coral-mesh/coral/coral/colony/v1"
	"github.com/coral-mesh/coral/coral/colony/v1/colonyv1connect"
	"github.com/coral-mesh/coral/internal/cli/helpers"
	"github.com/coral-mesh/coral/internal/flamegraph"
)

// getColonyDebugClient returns a colony debug client.
func getColonyDebugClient() (colonyv1connect.ColonyDebugServiceClient, error) {
	return helpers.GetColonyDebugClient("")
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

// printCPUProfileSVG renders the profile as an interactive SVG flame graph.
func printCPUProfileSVG(profile *debugpb.ProfileCPUResponse) error {
	stacks := cpuSamplesToFolded(profile.Samples)
	return flamegraph.Render(os.Stdout, stacks, flamegraph.Options{
		Title:     "CPU Flame Graph",
		CountName: "samples",
		Colors:    flamegraph.PaletteHot,
	})
}

// printMemoryProfileSVG renders the memory profile as an interactive SVG flame graph.
func printMemoryProfileSVG(profile *debugpb.ProfileMemoryResponse) error {
	stacks := memorySamplesToFolded(profile.Samples)
	return flamegraph.Render(os.Stdout, stacks, flamegraph.Options{
		Title:     "Memory Flame Graph",
		CountName: "bytes",
		Colors:    flamegraph.PaletteMem,
	})
}

// cpuSamplesToFolded converts CPU stack samples to flamegraph FoldedStack format.
func cpuSamplesToFolded(samples []*agentpb.StackSample) []flamegraph.FoldedStack {
	stacks := make([]flamegraph.FoldedStack, 0, len(samples))
	for _, s := range samples {
		if len(s.FrameNames) == 0 {
			continue
		}
		// Reverse frames: BPF captures innermost first, flamegraph expects root first.
		frames := make([]string, len(s.FrameNames))
		for i, f := range s.FrameNames {
			frames[len(s.FrameNames)-1-i] = f
		}
		stacks = append(stacks, flamegraph.FoldedStack{
			Frames: frames,
			Value:  int64(s.Count),
		})
	}
	return stacks
}

// memorySamplesToFolded converts memory stack samples to flamegraph FoldedStack format.
func memorySamplesToFolded(samples []*agentpb.MemoryStackSample) []flamegraph.FoldedStack {
	stacks := make([]flamegraph.FoldedStack, 0, len(samples))
	for _, s := range samples {
		if len(s.FrameNames) == 0 {
			continue
		}
		frames := make([]string, len(s.FrameNames))
		for i, f := range s.FrameNames {
			frames[len(s.FrameNames)-1-i] = f
		}
		stacks = append(stacks, flamegraph.FoldedStack{
			Frames: frames,
			Value:  s.AllocBytes,
		})
	}
	return stacks
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
