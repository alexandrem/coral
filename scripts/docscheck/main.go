// Command docscheck validates the user documentation: relative links and
// anchors, reading-map coverage, YAML examples, and shell example syntax.
package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	userDir   = "docs/user"
	cliManual = "docs/CLI_REFERENCE.md"
)

var (
	linkRe    = regexp.MustCompile(`\]\(([^)\s]+)\)`)
	headingRe = regexp.MustCompile(`^#{1,6}\s+(.*?)\s*#*\s*$`)
	fenceRe   = regexp.MustCompile("^\\s*(```+|~~~+)\\s*([A-Za-z0-9_+-]*)")
	slugDrop  = regexp.MustCompile(`[^\p{L}\p{N}\s_-]`)
)

// block is a fenced code block with its starting line number.
type block struct {
	lang string
	line int
	body string
}

// doc is a parsed Markdown file.
type doc struct {
	path    string
	links   []link
	anchors map[string]bool
	blocks  []block
}

type link struct {
	target string
	line   int
}

func main() {
	files, err := filepath.Glob(filepath.Join(userDir, "*.md"))
	if err != nil || len(files) == 0 {
		fmt.Fprintln(os.Stderr, "docscheck: no files under", userDir, "(run from the repository root)")
		os.Exit(2)
	}
	files = append(files, cliManual)
	sort.Strings(files)

	docs := map[string]*doc{}
	for _, f := range files {
		d, err := parse(f)
		if err != nil {
			fmt.Fprintln(os.Stderr, "docscheck:", err)
			os.Exit(2)
		}
		docs[filepath.Clean(f)] = d
	}

	var problems []string
	for _, f := range files {
		d := docs[filepath.Clean(f)]
		problems = append(problems, checkLinks(d, docs)...)
		problems = append(problems, checkBlocks(d)...)
	}
	problems = append(problems, checkCoverage(docs)...)

	for _, p := range problems {
		fmt.Println(p)
	}
	if len(problems) > 0 {
		fmt.Printf("docscheck: %d problem(s)\n", len(problems))
		os.Exit(1)
	}
	fmt.Printf("docscheck: %d files OK\n", len(files))
}

// parse reads a Markdown file, collecting links, heading anchors, and fenced
// code blocks. Links and headings inside code blocks are ignored.
func parse(path string) (*doc, error) {
	raw, err := os.ReadFile(path) // #nosec G304 -- Paths come from the documentation tree or its links. //nolint:gosec
	if err != nil {
		return nil, err
	}
	d := &doc{path: path, anchors: map[string]bool{}}
	seen := map[string]int{}
	var cur *block
	var fence string
	for i, line := range strings.Split(string(raw), "\n") {
		n := i + 1
		if m := fenceRe.FindStringSubmatch(line); m != nil {
			switch {
			case cur == nil:
				cur = &block{lang: strings.ToLower(m[2]), line: n}
				fence = m[1]
			case strings.HasPrefix(m[1], fence) && strings.TrimSpace(line) == m[1]:
				d.blocks = append(d.blocks, *cur)
				cur = nil
			default:
				cur.body += line + "\n"
			}
			continue
		}
		if cur != nil {
			cur.body += line + "\n"
			continue
		}
		if m := headingRe.FindStringSubmatch(line); m != nil {
			s := slug(m[1])
			if c := seen[s]; c > 0 {
				d.anchors[fmt.Sprintf("%s-%d", s, c)] = true
			} else {
				d.anchors[s] = true
			}
			seen[s]++
		}
		for _, m := range linkRe.FindAllStringSubmatch(line, -1) {
			d.links = append(d.links, link{target: m[1], line: n})
		}
	}
	return d, nil
}

// slug converts a heading to its GitHub-style anchor.
func slug(h string) string {
	h = strings.ToLower(strings.NewReplacer("`", "", "*", "").Replace(h))
	h = slugDrop.ReplaceAllString(h, "")
	return strings.ReplaceAll(strings.TrimSpace(h), " ", "-")
}

// checkLinks verifies that relative link targets and fragments exist.
func checkLinks(d *doc, docs map[string]*doc) []string {
	var out []string
	for _, l := range d.links {
		if strings.Contains(l.target, "://") || strings.HasPrefix(l.target, "mailto:") {
			continue
		}
		file, frag, _ := strings.Cut(l.target, "#")
		target := d.path
		if file != "" {
			target = filepath.Clean(filepath.Join(filepath.Dir(d.path), file))
			if _, err := os.Stat(target); err != nil {
				out = append(out, fmt.Sprintf("%s:%d: missing link target %s", d.path, l.line, l.target))
				continue
			}
		}
		if frag == "" {
			continue
		}
		td, ok := docs[target]
		if !ok {
			if strings.HasSuffix(target, ".md") {
				if td, err := parse(target); err == nil {
					ok = true
					docs[target] = td
				}
			}
			if !ok {
				continue
			}
			td = docs[target]
		}
		if !td.anchors[frag] {
			out = append(out, fmt.Sprintf("%s:%d: missing anchor %s", d.path, l.line, l.target))
		}
	}
	return out
}

// checkBlocks parses YAML examples and syntax-checks shell examples.
func checkBlocks(d *doc) []string {
	var out []string
	for _, b := range d.blocks {
		switch b.lang {
		case "yaml", "yml":
			dec := yaml.NewDecoder(strings.NewReader(b.body))
			for {
				var v any
				err := dec.Decode(&v)
				if err != nil {
					if err.Error() != "EOF" {
						out = append(out, fmt.Sprintf("%s:%d: invalid YAML: %v", d.path, b.line, err))
					}
					break
				}
			}
		case "bash", "sh", "shell":
			cmd := exec.Command("bash", "-n")
			cmd.Stdin = strings.NewReader(b.body)
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			if err := cmd.Run(); err != nil {
				out = append(out, fmt.Sprintf("%s:%d: shell syntax: %s", d.path, b.line, strings.TrimSpace(stderr.String())))
			}
		}
	}
	return out
}

// checkCoverage verifies that the reading map links every chapter.
func checkCoverage(docs map[string]*doc) []string {
	readme := docs[filepath.Join(userDir, "README.md")]
	if readme == nil {
		return []string{userDir + "/README.md: missing reading map"}
	}
	linked := map[string]bool{}
	for _, l := range readme.links {
		file, _, _ := strings.Cut(l.target, "#")
		linked[filepath.Clean(filepath.Join(userDir, file))] = true
	}
	var out []string
	for path := range docs {
		if filepath.Dir(path) == userDir && filepath.Base(path) != "README.md" && !linked[path] {
			out = append(out, fmt.Sprintf("%s: not linked from the reading map", path))
		}
	}
	sort.Strings(out)
	return out
}
