package flamegraph

import (
	"fmt"
	"io"
)

// writeJS writes the embedded JavaScript for interactivity.
func writeJS(w io.Writer, opts Options, imageWidth, imageHeight int) error {
	_, err := fmt.Fprintf(w, `<script type="text/ecmascript"><![CDATA[
"use strict";
var details, searchbtn, unzoombtn, matchedtxt, svg, searching, currentSearchTerm;
var xpad = %d, frameheight = %d;

function init(evt) {
    details = document.getElementById("details").firstChild;
    searchbtn = document.getElementById("search");
    unzoombtn = document.getElementById("unzoom");
    matchedtxt = document.getElementById("matched");
    svg = document.getElementsByTagName("svg")[0];
}

// Functions used by mouseover/mouseout.
function s(node) {
    details.nodeValue = node.getElementsByTagName("title")[0].textContent;
}
function c() {
    details.nodeValue = " ";
}

// Zoom into a frame.
function zoom(node) {
    var attr = find_child(node, "rect").attributes;
    var ox = parseFloat(attr.x.value);
    var ow = parseFloat(attr.width.value);
    if (ow === 0) return;

    var svgWidth = svg.width.baseVal.value;
    var scaleFactor = (svgWidth - 2 * xpad) / ow;
    var nx = xpad - ox * scaleFactor + xpad * (scaleFactor - 1);

    var el = document.getElementsByClassName("func_g");
    for (var i = 0; i < el.length; i++) {
        var r = find_child(el[i], "rect");
        var rx = parseFloat(r.attributes.x.value);
        var rw = parseFloat(r.attributes.width.value);
        var ry = parseFloat(r.attributes.y.value);
        var ty = parseFloat(find_child(el[i], "text").attributes.y.value);

        // Determine the target frame's y range.
        var targetY = parseFloat(attr.y.value);

        // Hide frames that are not ancestors or descendants.
        if (rx + rw < ox || rx > ox + ow) {
            // Not overlapping horizontally: hide unless it's an ancestor.
            if (ry >= targetY) {
                el[i].style.display = "none";
                continue;
            }
        }

        // Show ancestor frames (below target in y, i.e., larger y).
        if (ry > targetY && (rx + rw < ox || rx > ox + ow)) {
            el[i].style.display = "none";
            continue;
        }

        el[i].style.display = "block";

        // Scale horizontally.
        var newX = nx + rx * scaleFactor;
        var newW = rw * scaleFactor;
        r.attributes.x.value = newX;
        r.attributes.width.value = newW;

        // Update text.
        var t = find_child(el[i], "text");
        t.attributes.x.value = newX + 3;
        update_text(t, newW);
    }

    unzoombtn.style.opacity = "1.0";
}

// Reset zoom.
function unzoom() {
    var el = document.getElementsByClassName("func_g");
    for (var i = 0; i < el.length; i++) {
        el[i].style.display = "block";
        var r = find_child(el[i], "rect");
        var orig = original_attrs(find_child(el[i], "title").textContent, r);
        r.attributes.x.value = orig[0];
        r.attributes.width.value = orig[1];
        var t = find_child(el[i], "text");
        t.attributes.x.value = parseFloat(orig[0]) + 3;
        update_text(t, parseFloat(orig[1]));
    }
    unzoombtn.style.opacity = "0.0";
}

// Store original attributes for unzoom.
var origData = {};
function original_attrs(title, rect) {
    var key = title + "," + rect.attributes.y.value;
    if (!(key in origData)) {
        origData[key] = [rect.attributes.x.value, rect.attributes.width.value];
    }
    return origData[key];
}

// Save initial positions on first zoom.
(function() {
    var saved = false;
    var origZoom = zoom;
    zoom = function(node) {
        if (!saved) {
            var el = document.getElementsByClassName("func_g");
            for (var i = 0; i < el.length; i++) {
                var r = find_child(el[i], "rect");
                var title = find_child(el[i], "title").textContent;
                original_attrs(title, r);
            }
            saved = true;
        }
        origZoom(node);
    };
})();

// Search functionality.
function search_prompt() {
    var term = prompt("Search (regex):", currentSearchTerm || "");
    if (term === null || term === "") {
        reset_search();
        return;
    }
    currentSearchTerm = term;
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

    var el = document.getElementsByClassName("func_g");
    var matched = 0;
    var totalVal = 0;
    var matchedVal = 0;

    for (var i = 0; i < el.length; i++) {
        var title = find_child(el[i], "title").textContent;
        var rect = find_child(el[i], "rect");

        // Extract value from title "name (N unit, X%%)".
        var m = title.match(/\((\d+)/);
        var val = m ? parseInt(m[1]) : 0;
        totalVal += val;

        if (re.test(title)) {
            rect.style.fill = "rgb(230,0,230)";
            matched++;
            matchedVal += val;
        } else {
            rect.style.fill = "";
        }
    }

    if (matched > 0 && totalVal > 0) {
        matchedtxt.firstChild.nodeValue = "Matched: " + matched + " frames";
        matchedtxt.style.opacity = "1.0";
        searchbtn.style.opacity = "1.0";
    }
    searching = true;
}

function reset_search() {
    var el = document.getElementsByClassName("func_g");
    for (var i = 0; i < el.length; i++) {
        find_child(el[i], "rect").style.fill = "";
    }
    matchedtxt.firstChild.nodeValue = " ";
    matchedtxt.style.opacity = "0.0";
    searchbtn.style.opacity = "0.1";
    currentSearchTerm = null;
    searching = false;
}

function find_child(parent, name) {
    var children = parent.childNodes;
    for (var i = 0; i < children.length; i++) {
        if (children[i].tagName === name) return children[i];
    }
    return null;
}

function update_text(t, width) {
    var name = t.textContent;
    var charWidth = %f;
    var maxChars = Math.floor(width / charWidth);
    if (maxChars < 3) {
        t.style.display = "none";
    } else {
        t.style.display = "block";
    }
}

// Keyboard shortcuts.
document.addEventListener("keydown", function(e) {
    if (e.ctrlKey && e.key === "f") {
        e.preventDefault();
        search_prompt();
    }
});
]]></script>
`, xPad, frameHeight, fontSize*fontWidth)
	return err
}
