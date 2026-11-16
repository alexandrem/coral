package flamegraph

import (
	"fmt"
	"io"
)

// writeJS writes the embedded JavaScript for interactivity.
func writeJS(w io.Writer) error {
	_, err := fmt.Fprintf(w, `<script type="text/ecmascript"><![CDATA[
"use strict";
var details, searchbtn, unzoombtn, matchedtxt, svg, frames, currentSearchTerm;
var xpad = %d;

function init(evt) {
    details = document.getElementById("details");
    searchbtn = document.getElementById("search");
    unzoombtn = document.getElementById("unzoom");
    matchedtxt = document.getElementById("matched");
    svg = document.documentElement;
    frames = Array.from(document.getElementsByClassName("func_g"));
    frames.forEach(function(frame) {
        var rect = find_child(frame, "rect");
        // Keep positions on the frame itself: names and counts can repeat.
        frame.original = {
            x: parseFloat(rect.getAttribute("x")),
            width: parseFloat(rect.getAttribute("width")),
            y: parseFloat(rect.getAttribute("y"))
        };
        update_text(frame, frame.original.width);
    });
}

function s(node) {
    details.textContent = find_child(node, "title").textContent;
}
function c() {
    details.textContent = " ";
}

// Always zoom from original coordinates, including when zooming an ancestor.
function zoom(node) {
    var target = node.original;
    if (target.width <= 0) return;
    var width = svg.viewBox.baseVal.width - 2 * xpad;
    var scale = width / target.width;
    frames.forEach(function(frame) {
        var orig = frame.original;
        var ancestor = orig.y > target.y;
        // Allow only floating-point noise at shared frame boundaries.
        var contains = orig.x <= target.x + 1e-7 && orig.x + orig.width >= target.x + target.width - 1e-7;
        var inside = orig.x >= target.x - 1e-7 && orig.x + orig.width <= target.x + target.width + 1e-7;
        if (frame !== node && (ancestor ? !contains : !inside)) {
            frame.style.display = "none";
            return;
        }
        frame.style.display = "block";
        frame.style.opacity = ancestor ? "0.5" : "1";
        var rect = find_child(frame, "rect");
        var newX = ancestor ? xpad : xpad + (orig.x - target.x) * scale;
        var newWidth = ancestor ? width : orig.width * scale;
        rect.setAttribute("x", newX);
        rect.setAttribute("width", newWidth);
        update_text(frame, newWidth);
    });
    unzoombtn.style.opacity = "1";
}

function unzoom() {
    frames.forEach(function(frame) {
        frame.style.display = "block";
        frame.style.opacity = "1";
        var rect = find_child(frame, "rect");
        rect.setAttribute("x", frame.original.x);
        rect.setAttribute("width", frame.original.width);
        update_text(frame, frame.original.width);
    });
    unzoombtn.style.opacity = "0";
}

function search_prompt() {
    var term = prompt("Search (regex):", currentSearchTerm || "");
    if (term === null) return;
    if (term === "") {
        reset_search();
        return;
    }
    search(term);
}

function search(term) {
    var re;
    try {
        re = new RegExp(term, "i");
    } catch(e) {
        alert("Invalid regex: " + e.message);
        return;
    }
    currentSearchTerm = term;
    var matched = 0;
    frames.forEach(function(frame) {
        var match = re.test(frame.getAttribute("data-name"));
        find_child(frame, "rect").style.fill = match ? "rgb(230,0,230)" : "";
        if (match) matched++;
    });
    matchedtxt.textContent = "Matched: " + matched + " frames";
    matchedtxt.style.opacity = "1";
    searchbtn.style.opacity = "1";
}

function reset_search() {
    frames.forEach(function(frame) { find_child(frame, "rect").style.fill = ""; });
    matchedtxt.textContent = " ";
    matchedtxt.style.opacity = "0";
    searchbtn.style.opacity = "0.1";
    currentSearchTerm = null;
}

function find_child(parent, name) {
    return parent.getElementsByTagName(name)[0];
}

function update_text(frame, width) {
    var text = find_child(frame, "text");
    var rect = find_child(frame, "rect");
    text.setAttribute("x", parseFloat(rect.getAttribute("x")) + 3);
    var chars = Array.from(frame.getAttribute("data-name"));
    var maxChars = Math.floor(Math.max(0, width - 6) / %f);
    text.textContent = maxChars < 3 ? "" : (chars.length <= maxChars ? chars.join("") : chars.slice(0, maxChars - 2).join("") + "..");
}

document.addEventListener("keydown", function(e) {
    if ((e.ctrlKey || e.metaKey) && e.key === "f") {
        e.preventDefault();
        search_prompt();
    }
});
]]></script>
`, xPad, fontSize*fontWidth)
	return err
}
