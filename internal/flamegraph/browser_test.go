package flamegraph

import (
	"bytes"
	"context"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestBrowser exercises the generated standalone SVG in a real Chromium DOM.
// This optional check runs only when CORAL_FLAMEGRAPH_BROWSER is set explicitly.
// TestJavaScriptInteractions covers the interaction logic without a browser.
func TestBrowser(t *testing.T) {
	browser := os.Getenv("CORAL_FLAMEGRAPH_BROWSER")
	if browser == "" {
		t.Skip("optional browser check; set CORAL_FLAMEGRAPH_BROWSER to a Chromium executable")
	}
	svg := strings.Replace(string(renderInteractionSVG(t)), `onload="init(evt)"`, `onload="init(evt); runTests()"`, 1)
	svg = strings.Replace(svg, "</svg>", `<script type="text/ecmascript"><![CDATA[`+interactionAssertions+`]]></script></svg>`, 1)
	dir := t.TempDir()
	path := filepath.Join(dir, "profile.svg")
	if err := os.WriteFile(path, []byte(svg), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, browser, "--headless", "--no-sandbox", "--disable-gpu", "--disable-background-networking", "--timeout=5000", "--virtual-time-budget=1000", "--no-first-run", "--password-store=basic", "--use-mock-keychain", "--no-default-browser-check", "--user-data-dir="+filepath.Join(dir, "chrome"), "--dump-dom", (&url.URL{Scheme: "file", Path: path}).String())
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("browser failed: %v\n%s", err, &stderr)
	}
	if !bytes.Contains(output, []byte(`data-test-result="passed"`)) {
		t.Fatalf("browser regressions failed:\n%s\n%s", output, &stderr)
	}
}
