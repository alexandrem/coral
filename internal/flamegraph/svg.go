package flamegraph

import (
	"fmt"
	"html"
	"io"
	"math"
)

const (
	frameHeight = 16   // Height of each frame in pixels.
	fontSize    = 12   // Font size in pixels.
	fontWidth   = 0.59 // Approximate width of a character at fontSize.
	xPad        = 10   // Horizontal padding.
	yPadTop     = 60   // Top padding (title + subtitle area).
	yPadBottom  = 40   // Bottom padding (details bar).
)

// svgWriter wraps an io.Writer and captures the first write error.
type svgWriter struct {
	w   io.Writer
	err error
}

func (sw *svgWriter) printf(format string, args ...any) {
	if sw.err != nil {
		return
	}
	_, sw.err = fmt.Fprintf(sw.w, format, args...)
}

func (sw *svgWriter) print(s string) {
	if sw.err != nil {
		return
	}
	_, sw.err = fmt.Fprint(sw.w, s)
}

// renderSVG writes a complete interactive SVG flame graph.
func renderSVG(w io.Writer, root *node, maxDepth int, opts Options) error {
	imageHeight := yPadTop + (maxDepth+1)*frameHeight + yPadBottom
	imageWidth := opts.Width
	totalValue := root.value

	if totalValue == 0 {
		return fmt.Errorf("no samples to render")
	}

	// Scale factor: value units to pixels.
	widthPerUnit := float64(imageWidth-2*xPad) / float64(totalValue)

	// Layout the tree.
	lt := layoutTree(root)

	sw := &svgWriter{w: w}

	// Write SVG header.
	sw.printf(`<?xml version="1.0" standalone="no"?>
<!DOCTYPE svg PUBLIC "-//W3C//DTD SVG 1.1//EN" "http://www.w3.org/Graphics/SVG/1.1/DTD/svg11.dtd">
<svg version="1.1" width="%d" height="%d" onload="init(evt)" viewBox="0 0 %d %d"
 xmlns="http://www.w3.org/2000/svg" xmlns:xlink="http://www.w3.org/1999/xlink">
`, imageWidth, imageHeight, imageWidth, imageHeight)

	// Background.
	sw.printf(`<rect x="0" y="0" width="%d" height="%d" fill="url(#bg)" />
`, imageWidth, imageHeight)

	// Defs: background gradient.
	sw.print(`<defs>
  <linearGradient id="bg" y1="0" y2="1" x1="0" x2="0">
    <stop stop-color="#eeeeee" offset="5%" />
    <stop stop-color="#eeeeb0" offset="95%" />
  </linearGradient>
</defs>
`)

	// Styles.
	sw.print(`<style type="text/css">
  text { font-family: Verdana, sans-serif; font-size: 12px; fill: rgb(0,0,0); }
  .func_g:hover { stroke: black; stroke-width: 0.5; cursor: pointer; }
</style>
`)

	// Embedded JavaScript.
	if err := writeJS(w, opts, imageWidth, imageHeight); err != nil {
		return err
	}

	// Title.
	sw.printf(`<text x="%d" y="24" text-anchor="middle" style="font-size:17px">%s</text>
`,
		imageWidth/2, html.EscapeString(opts.Title))

	// Details bar (updated by JS on mouseover).
	sw.printf(`<text id="details" x="%d" y="%d"> </text>
`,
		xPad, imageHeight-yPadBottom+frameHeight+4)

	// Search/unzoom buttons (hidden by default, shown by JS).
	sw.printf(`<text id="unzoom" x="%d" y="24" onclick="unzoom()" style="opacity:0.0;cursor:pointer">Reset Zoom</text>
`,
		xPad)
	sw.printf(`<text id="search" x="%d" y="24" onclick="search_prompt()" style="opacity:0.1;cursor:pointer">Search</text>
`,
		imageWidth-xPad-60)
	sw.printf(`<text id="matched" x="%d" y="24"> </text>
`,
		imageWidth-xPad-60)

	// Render frames.
	var render func(ln *layoutNode)
	render = func(ln *layoutNode) {
		if sw.err != nil {
			return
		}

		// Skip root node (it's the container, not a real frame).
		if ln.depth > 0 {
			x1 := float64(xPad) + ln.x*widthPerUnit
			x2 := x1 + float64(ln.value)*widthPerUnit
			pixelWidth := x2 - x1

			if pixelWidth >= opts.MinWidth {
				// Y coordinate: bottom-up layout (leaf at top, root at bottom).
				y1 := float64(imageHeight-yPadBottom) - float64(ln.depth)*float64(frameHeight)
				y2 := y1 + float64(frameHeight) - 1 // 1px gap between frames.

				pct := 100.0 * float64(ln.value) / float64(totalValue)
				color := frameColor(ln.name, opts.Colors)
				escapedName := html.EscapeString(ln.name)

				// Truncate text to fit.
				chars := int(math.Floor(pixelWidth / (fontSize * fontWidth)))
				displayText := ""
				if chars >= 3 {
					if chars >= len(ln.name) {
						displayText = escapedName
					} else {
						displayText = html.EscapeString(ln.name[:chars-2]) + ".."
					}
				}

				sw.printf(`<g class="func_g" onmouseover="s(this)" onmouseout="c()" onclick="zoom(this)">
`)
				sw.printf(`<title>%s (%d %s, %.2f%%)</title>
`,
					escapedName, ln.value, opts.CountName, pct)
				sw.printf(`<rect x="%.1f" y="%.1f" width="%.1f" height="%.1f" fill="%s" rx="2" ry="2" />
`,
					x1, y1, x2-x1, y2-y1, color)
				if displayText != "" {
					sw.printf(`<text x="%.1f" y="%.1f">%s</text>
`,
						x1+3, y1+float64(frameHeight)-4, displayText)
				}
				sw.print("</g>\n")
			}
		}

		for _, c := range ln.children {
			render(c)
		}
	}
	render(lt)

	// Footer.
	sw.print("</svg>\n")
	return sw.err
}

// frameColor returns a deterministic color for a frame name.
func frameColor(name string, palette Palette) string {
	v1, v2 := nameHash(name)
	switch palette {
	case PaletteMem:
		// Green/aqua tones.
		r := 0 + int(110*v1)
		g := 190 + int(65*v2)
		b := 0 + int(110*v1)
		return fmt.Sprintf("rgb(%d,%d,%d)", r, g, b)
	default: // PaletteHot
		// Warm red/yellow/orange tones.
		r := 205 + int(50*v1)
		g := 0 + int(230*v2)
		b := 0 + int(55*v1)
		return fmt.Sprintf("rgb(%d,%d,%d)", r, g, b)
	}
}

// nameHash returns two deterministic float64 values in [0,1) for a name.
func nameHash(name string) (float64, float64) {
	var h1, h2 uint32
	for i := 0; i < len(name); i++ {
		h1 = h1*31 + uint32(name[i])
		h2 = h2*37 + uint32(name[i])
	}
	return float64(h1%100) / 100.0, float64(h2%100) / 100.0
}
