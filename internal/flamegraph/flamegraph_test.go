package flamegraph

import (
	"bytes"
	"strings"
	"testing"
)

func TestBuildTree_Basic(t *testing.T) {
	stacks := []FoldedStack{
		{Frames: []string{"main", "foo", "bar"}, Value: 10},
		{Frames: []string{"main", "foo", "baz"}, Value: 5},
		{Frames: []string{"main", "qux"}, Value: 3},
	}

	root := buildTree(stacks)
	if root == nil {
		t.Fatal("expected non-nil root")
	}
	if root.value != 18 {
		t.Errorf("root.value = %d, want 18", root.value)
	}
	if len(root.children) != 1 {
		t.Fatalf("root.children = %d, want 1 (main)", len(root.children))
	}

	main := root.children[0]
	if main.name != "main" || main.value != 18 {
		t.Errorf("main = {%s, %d}, want {main, 18}", main.name, main.value)
	}
	if len(main.children) != 2 {
		t.Fatalf("main.children = %d, want 2 (foo, qux)", len(main.children))
	}

	// Children should be sorted alphabetically.
	if main.children[0].name != "foo" {
		t.Errorf("first child = %s, want foo", main.children[0].name)
	}
	if main.children[1].name != "qux" {
		t.Errorf("second child = %s, want qux", main.children[1].name)
	}

	foo := main.children[0]
	if foo.value != 15 {
		t.Errorf("foo.value = %d, want 15", foo.value)
	}
	if len(foo.children) != 2 {
		t.Fatalf("foo.children = %d, want 2 (bar, baz)", len(foo.children))
	}
	if foo.children[0].name != "bar" || foo.children[0].value != 10 {
		t.Errorf("bar = {%s, %d}, want {bar, 10}", foo.children[0].name, foo.children[0].value)
	}
	if foo.children[1].name != "baz" || foo.children[1].value != 5 {
		t.Errorf("baz = {%s, %d}, want {baz, 5}", foo.children[1].name, foo.children[1].value)
	}
}

func TestBuildTree_Empty(t *testing.T) {
	root := buildTree(nil)
	if root != nil {
		t.Error("expected nil root for empty input")
	}

	root = buildTree([]FoldedStack{})
	if root != nil {
		t.Error("expected nil root for empty slice")
	}
}

func TestBuildTree_ZeroValue(t *testing.T) {
	stacks := []FoldedStack{
		{Frames: []string{"main"}, Value: 0},
	}
	root := buildTree(stacks)
	if root != nil {
		t.Error("expected nil root for zero-value stacks")
	}
}

func TestMaxDepth(t *testing.T) {
	stacks := []FoldedStack{
		{Frames: []string{"a", "b", "c", "d"}, Value: 1},
		{Frames: []string{"a", "b"}, Value: 1},
	}
	root := buildTree(stacks)
	if d := root.maxDepth(); d != 4 {
		t.Errorf("maxDepth = %d, want 4", d)
	}
}

func TestRender_BasicSVG(t *testing.T) {
	stacks := []FoldedStack{
		{Frames: []string{"main", "processRequest", "parseJSON"}, Value: 127},
		{Frames: []string{"main", "processRequest", "validateData"}, Value: 89},
		{Frames: []string{"main", "handleError"}, Value: 34},
	}

	var buf bytes.Buffer
	err := Render(&buf, stacks, Options{
		Title:     "Test CPU Flame Graph",
		CountName: "samples",
	})
	if err != nil {
		t.Fatalf("Render failed: %v", err)
	}

	svg := buf.String()

	// Verify SVG structure.
	checks := []string{
		`<?xml version="1.0"`,
		"<svg ",
		"</svg>",
		"<script",
		"</script>",
		"Test CPU Flame Graph",
		"func_g",
		"parseJSON",
		"validateData",
		"handleError",
		"processRequest",
	}
	for _, check := range checks {
		if !strings.Contains(svg, check) {
			t.Errorf("SVG missing expected content: %q", check)
		}
	}

	// Verify tooltip format.
	if !strings.Contains(svg, "samples") {
		t.Error("SVG missing 'samples' count name in tooltips")
	}
}

func TestRender_EmptyInput(t *testing.T) {
	var buf bytes.Buffer
	err := Render(&buf, nil, Options{})
	if err == nil {
		t.Error("expected error for empty input")
	}
}

func TestRender_SingleStack(t *testing.T) {
	stacks := []FoldedStack{
		{Frames: []string{"main"}, Value: 100},
	}

	var buf bytes.Buffer
	err := Render(&buf, stacks, Options{})
	if err != nil {
		t.Fatalf("Render failed: %v", err)
	}

	svg := buf.String()
	if !strings.Contains(svg, "main") {
		t.Error("SVG missing 'main' frame")
	}
	if !strings.Contains(svg, "<rect") {
		t.Error("SVG missing rect elements")
	}
}

func TestRender_MemoryOptions(t *testing.T) {
	stacks := []FoldedStack{
		{Frames: []string{"main", "malloc"}, Value: 4096},
	}

	var buf bytes.Buffer
	err := Render(&buf, stacks, Options{
		Title:     "Memory Flame Graph",
		CountName: "bytes",
		Colors:    PaletteMem,
	})
	if err != nil {
		t.Fatalf("Render failed: %v", err)
	}

	svg := buf.String()
	if !strings.Contains(svg, "Memory Flame Graph") {
		t.Error("SVG missing custom title")
	}
	if !strings.Contains(svg, "bytes") {
		t.Error("SVG missing 'bytes' count name")
	}
}

func TestColorDeterministic(t *testing.T) {
	c1 := frameColor("main.processRequest", PaletteHot)
	c2 := frameColor("main.processRequest", PaletteHot)
	if c1 != c2 {
		t.Errorf("non-deterministic colors: %q != %q", c1, c2)
	}

	// Different names should (usually) produce different colors.
	c3 := frameColor("completely.different.function", PaletteHot)
	if c1 == c3 {
		t.Log("warning: different names produced same color (possible but unlikely)")
	}
}

func TestColorPalettes(t *testing.T) {
	hot := frameColor("test", PaletteHot)
	mem := frameColor("test", PaletteMem)
	if hot == mem {
		t.Error("hot and mem palettes produced same color")
	}

	// Verify format.
	if !strings.HasPrefix(hot, "rgb(") || !strings.HasSuffix(hot, ")") {
		t.Errorf("invalid color format: %q", hot)
	}
}

func TestNameHash(t *testing.T) {
	v1, v2 := nameHash("test")
	if v1 < 0 || v1 >= 1 || v2 < 0 || v2 >= 1 {
		t.Errorf("nameHash out of range: %f, %f", v1, v2)
	}

	// Deterministic.
	v1b, v2b := nameHash("test")
	if v1 != v1b || v2 != v2b {
		t.Error("nameHash not deterministic")
	}
}

func TestRender_MinWidth(t *testing.T) {
	// One large frame and one tiny frame.
	stacks := []FoldedStack{
		{Frames: []string{"big"}, Value: 10000},
		{Frames: []string{"tiny"}, Value: 1},
	}

	var buf bytes.Buffer
	err := Render(&buf, stacks, Options{
		Width:    200, // Small width to make tiny frame sub-pixel.
		MinWidth: 1.0, // 1px minimum.
	})
	if err != nil {
		t.Fatalf("Render failed: %v", err)
	}

	svg := buf.String()
	if !strings.Contains(svg, "big") {
		t.Error("SVG missing 'big' frame")
	}
	// "tiny" should be pruned because its pixel width < 1.0.
	if strings.Contains(svg, ">tiny<") {
		t.Error("SVG should have pruned 'tiny' frame below MinWidth")
	}
}

func TestLayoutTree(t *testing.T) {
	stacks := []FoldedStack{
		{Frames: []string{"a", "b"}, Value: 10},
		{Frames: []string{"a", "c"}, Value: 5},
	}

	root := buildTree(stacks)
	lt := layoutTree(root)

	if lt.x != 0 {
		t.Errorf("root x = %f, want 0", lt.x)
	}
	if lt.depth != 0 {
		t.Errorf("root depth = %d, want 0", lt.depth)
	}
	if len(lt.children) != 1 {
		t.Fatalf("root children = %d, want 1", len(lt.children))
	}

	a := lt.children[0]
	if a.depth != 1 {
		t.Errorf("a.depth = %d, want 1", a.depth)
	}
	if len(a.children) != 2 {
		t.Fatalf("a.children = %d, want 2", len(a.children))
	}

	// b comes before c alphabetically.
	b := a.children[0]
	c := a.children[1]
	if b.name != "b" || c.name != "c" {
		t.Errorf("children = [%s, %s], want [b, c]", b.name, c.name)
	}
	if b.x != 0 {
		t.Errorf("b.x = %f, want 0", b.x)
	}
	if c.x != 10 {
		t.Errorf("c.x = %f, want 10", c.x)
	}
}
