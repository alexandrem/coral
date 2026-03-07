// Package flamegraph generates interactive SVG flame graphs from folded stack data.
package flamegraph

import (
	"fmt"
	"io"
)

// Palette selects the color scheme for flame graph rendering.
type Palette int

const (
	// PaletteHot uses warm red/yellow/orange colors for CPU profiles.
	PaletteHot Palette = iota
	// PaletteMem uses green/blue colors for memory profiles.
	PaletteMem
)

// FoldedStack represents a single folded stack line with frames and a count.
type FoldedStack struct {
	Frames []string // Stack frames from root (outermost) to leaf (innermost).
	Value  int64    // Sample count, byte count, or other metric.
}

// Options configures flame graph rendering.
type Options struct {
	Title     string  // SVG title (default: "Flame Graph").
	CountName string  // Unit label: "samples", "bytes", etc. (default: "samples").
	Width     int     // Image width in pixels (default: 1200).
	MinWidth  float64 // Minimum frame width in pixels to render (default: 0.1).
	Colors    Palette // Color palette.
}

func (o *Options) defaults() {
	if o.Title == "" {
		o.Title = "Flame Graph"
	}
	if o.CountName == "" {
		o.CountName = "samples"
	}
	if o.Width <= 0 {
		o.Width = 1200
	}
	if o.MinWidth <= 0 {
		o.MinWidth = 0.1
	}
}

// Render generates an interactive SVG flame graph and writes it to w.
func Render(w io.Writer, stacks []FoldedStack, opts Options) error {
	opts.defaults()

	root := buildTree(stacks)
	if root == nil {
		return fmt.Errorf("no stack data to render")
	}

	maxDepth := root.maxDepth()
	return renderSVG(w, root, maxDepth, opts)
}
