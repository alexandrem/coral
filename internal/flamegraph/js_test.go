package flamegraph

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// TestJavaScriptInteractions executes the script extracted from the rendered SVG
// against a minimal DOM model. Deno is already a build dependency downloaded by
// make generate; no browser or npm packages are needed, including in short mode.
func TestJavaScriptInteractions(t *testing.T) {
	var doc testSVGElement
	if err := xml.Unmarshal(renderInteractionSVG(t), &doc); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	var script string
	for _, child := range doc.Children {
		if child.XMLName.Local == "script" {
			script = child.Text
			break
		}
	}
	if script == "" {
		t.Fatal("rendered SVG has no interaction script")
	}
	deno, err := filepath.Abs(filepath.Join("..", "cli", "run", "binaries", "deno-"+runtime.GOOS+"-"+runtime.GOARCH))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(deno); err != nil {
		t.Fatalf("Deno build dependency unavailable; run make generate: %v", err)
	}
	source := "const svgData = " + string(data) + ";\n" + testDOM + "\n" + script + "\n" + interactionAssertions + `
init({});
runTests();
const result = document.documentElement.getAttribute("data-test-result");
if (result !== "passed") throw new Error(result || "interaction checks did not complete");
`
	path := filepath.Join(t.TempDir(), "flamegraph-interactions.js")
	if err := os.WriteFile(path, []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, deno, "run", "--no-config", "--no-check", path).CombinedOutput()
	if err != nil {
		t.Fatalf("SVG JavaScript interaction regressions failed: %v\n%s", err, output)
	}
}

type testSVGElement struct {
	XMLName    xml.Name
	Attributes []xml.Attr       `xml:",any,attr"`
	Text       string           `xml:",chardata"`
	Children   []testSVGElement `xml:",any"`
}

func renderInteractionSVG(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	stacks := []FoldedStack{
		{Frames: []string{"main", "left", "duplicate"}, Value: 20},
		{Frames: []string{"main", "right", "duplicate"}, Value: 20},
		{Frames: []string{"main", "right", "a-long-function-name-that-needs-truncation"}, Value: 59},
		{Frames: []string{"main", "right", "tiny"}, Value: 1},
	}
	if err := Render(&buf, stacks, Options{Width: 400}); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// testDOM models only the DOM operations used by the SVG script. Attributes,
// names, text nodes, and geometry all come from the actual rendered document.
const testDOM = `
class Element {
 constructor(data) {
  this.tagName = data.XMLName.Local;
  this.attrs = Object.fromEntries((data.Attributes || []).map(a => [a.Name.Local, a.Value]));
  this.childNodes = (data.Children || []).map(child => new Element(child));
  this.text = data.Text;
  this.style = { fill: "", display: "", opacity: "" };
 }
 getAttribute(name) { return this.attrs[name] ?? null; }
 setAttribute(name, value) { this.attrs[name] = String(value); }
 get textContent() { return this.text + this.childNodes.map(child => child.textContent).join(""); }
 set textContent(value) { this.text = String(value); this.childNodes = []; }
 descendants() { return this.childNodes.flatMap(child => [child, ...child.descendants()]); }
 getElementsByTagName(name) { return this.descendants().filter(child => child.tagName === name); }
}
const root = new Element(svgData);
root.viewBox = { baseVal: { width: Number(root.getAttribute("viewBox").split(" ")[2]) } };
const allElements = [root, ...root.descendants()];
const listeners = {};
const document = {
 documentElement: root,
 getElementById: id => allElements.find(el => el.getAttribute("id") === id),
 getElementsByClassName: name => allElements.filter(el => (el.getAttribute("class") || "").split(" ").includes(name)),
 addEventListener: (type, handler) => { listeners[type] = handler; },
};
globalThis.window = globalThis;
`

const interactionAssertions = `
function runTests() {
 try {
  function check(ok, message) { if (!ok) throw new Error(message); }
  var groups = Array.from(document.getElementsByClassName("func_g"));
  function name(g) { return g.getElementsByTagName("title")[0].textContent.split(" (")[0]; }
  function frame(n) { return groups.find(function(g) { return name(g) === n; }); }
  function rect(g) { return g.getElementsByTagName("rect")[0]; }
  function num(g, attr) { return parseFloat(rect(g).getAttribute(attr)); }
  function visible(g) { return g.style.display !== "none"; }
  var original = groups.map(function(g) { return [num(g,"x"), num(g,"width")]; });
  function restored() {
   groups.forEach(function(g,i) {
    check(visible(g), "reset must reveal " + name(g));
    check(num(g,"x") === original[i][0] && num(g,"width") === original[i][1], "reset must restore each frame independently: " + name(g));
   });
  }
  var main = frame("main"), left = frame("left"), right = frame("right"), tiny = frame("tiny");
  var longFrame = frame("a-long-function-name-that-needs-truncation");
  var tinyText = tiny.getElementsByTagName("text")[0];
  check(!tinyText || tinyText.textContent === "" || tinyText.style.display === "none", "tiny labels must fit");
  s(left);
  check(document.getElementById("details").textContent.includes("left (20 samples"), "hover must show counts");
  c();
  check(document.getElementById("details").textContent.trim() === "", "mouseout must clear details");
  zoom(left);
  check(Math.abs(num(left,"x") - 10) < 0.1, "selected frame must start at left padding");
  check(Math.abs(num(left,"width") - 380) < 0.1, "selected frame must fill viewport");
  check(!visible(right) && !visible(tiny) && !visible(longFrame), "zoom must hide sibling branches and their descendants");
  check(visible(main) && num(main,"x") === 10 && num(main,"width") === 380, "ancestors must fit viewport");
  unzoom(); restored();
  zoom(tiny);
  check(Math.abs(num(tiny,"x") - 10) < 0.1, "nonzero-offset frame must align on zoom");
  check(tiny.getElementsByTagName("text")[0].textContent === "tiny", "zoom must reveal previously omitted labels");
  check(!visible(longFrame), "touching sibling must be hidden");
  check(num(right,"x") === 10 && num(right,"width") === 380, "partial-width ancestor must fill viewport");
  zoom(right);
  check(visible(longFrame), "zooming an ancestor must reveal its descendants");
  unzoom(); restored();
  zoom(longFrame);
  check(longFrame.getElementsByTagName("text")[0].textContent === name(longFrame), "zoom must restore full function name");
  unzoom(); restored();
  search("duplicate");
  check(groups.filter(function(g) { return rect(g).style.fill !== ""; }).length === 2, "search must highlight both duplicate frames");
  check(document.getElementById("matched").textContent.includes("2 frames"), "search must report matches");
  search("no-such-function");
  check(document.getElementById("matched").textContent.includes("0 frames"), "empty search must clear stale match count");
  check(groups.every(function(g) { return rect(g).style.fill === ""; }), "new search must clear highlights");
  search("duplicate"); reset_search();
  check(document.getElementById("matched").textContent.trim() === "", "reset search must clear count");
  check(groups.every(function(g) { return rect(g).style.fill === ""; }), "reset search must restore palette");
  var alerted = false;
  window.alert = function() { alerted = true; };
  search("["); check(alerted, "invalid regex must be handled");
  document.documentElement.setAttribute("data-test-result", "passed");
 } catch (err) {
  document.documentElement.setAttribute("data-test-result", "failed: " + err.message);
 }
}
`
