package ci

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/mas-bandwidth/nova-tools/internal/onboarding"
)

// CILegsFromYAML reads the ci.yml workflow text and returns the GOOS values
// for which it runs legs. The legs are derived from literal runs-on labels
// (self-hosted linux -> "linux", self-hosted macOS -> "darwin") and from the
// GitHub-hosted matrix OS values (ubuntu-latest -> "linux", macos-latest ->
// "darwin"). There is no native Windows leg since 2026-09-18.
func CILegsFromYAML(yaml string) map[string]bool {
	legs := make(map[string]bool)
	for _, line := range strings.Split(yaml, "\n") {
		if !strings.HasPrefix(line, "    runs-on:") && !strings.HasPrefix(line, "        runs-on:") {
			continue
		}
		switch {
		case hasPlatformLabel(line, "linux"):
			legs["linux"] = true
		case hasPlatformLabel(line, "macOS"):
			legs["darwin"] = true
		case strings.Contains(line, "ubuntu-latest"):
			legs["linux"] = true
		case strings.Contains(line, "macos-latest"):
			legs["darwin"] = true
		}
	}
	return legs
}

func hasPlatformLabel(line, label string) bool {
	tokens := strings.Fields(strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(line, "    runs-on:"), "        runs-on:")))
	for _, t := range tokens {
		if strings.Trim(strings.TrimSuffix(strings.TrimSuffix(t, "]"), "["), `"' `) == label {
			return true
		}
	}
	return false
}

// PlatformLinesFromTESTSmd reads docs/TESTS.md and returns the GOOS values
// named by `Platform:` lines that appear within `## nova-*` sections.
// A platform line may be written as `Platform: darwin` or
// `Platform: recorded on macOS (darwin) — prose...`; in either case the
// GOOS keyword (linux, darwin) is extracted.
func PlatformLinesFromTESTSmd(md string) []string {
	var platforms []string
	for _, section := range toolSectionBodies(md) {
		for _, line := range strings.Split(section, "\n") {
			trimmed := strings.TrimSpace(line)
			if !strings.HasPrefix(trimmed, "Platform:") {
				continue
			}
			platforms = append(platforms, goosValues(trimmed)...)
		}
	}
	return platforms
}

func toolSectionBodies(md string) []string {
	var bodies []string
	head := "\n" + md
	for {
		top, rest, hasTop := strings.Cut(head, "\n## ")
		if !hasTop {
			break
		}
		head = rest
		_ = top
		name := strings.Fields(head)[0]
		if !strings.HasPrefix(name, "nova-") {
			continue
		}
		body, after, cut := strings.Cut(head, "\n## ")
		if !cut {
			bodies = append(bodies, head)
			break
		}
		bodies = append(bodies, body)
		head = after
	}
	return bodies
}

var knownGOOS = []string{"linux", "darwin", "windows", "freebsd", "netbsd", "openbsd", "plan9", "solaris", "aix", "android", "illumos", "ios", "js", "wasip1"}

// goosValues returns the known GOOS values the line names as whole words:
// "(darwin)" names darwin, "json" does not name js and "ratios" does not name
// ios.
func goosValues(line string) []string {
	words := make(map[string]bool)
	for _, w := range strings.FieldsFunc(line, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }) {
		words[w] = true
	}
	var vals []string
	for _, g := range knownGOOS {
		if words[g] {
			vals = append(vals, g)
		}
	}
	return vals
}

// PastedDocs are the documents a stranger pastes from, per SPEC-TOOLWORK §7
// rule 7, relative to the repo root.
var PastedDocs = []string{
	"README.md",
	filepath.Join("docs", "USAGE.md"),
	filepath.Join("docs", "CLI.md"),
	filepath.Join("docs", "nova-swarm-quickstart.md"),
}

// DocExample is one pasted example and the doc it is pasted in.
type DocExample struct {
	Doc  string // relative to the repo root, as in PastedDocs
	Line string // "$ ..." or "example: ..."
}

// PastedDocExamples returns every pasted example of the PastedDocs: each $
// line inside a fenced code block, and each line of a help banner's
// `example:` block pasted inside one (as "example: <line>"). A missing doc is
// an error naming it, so the scan never silently covers fewer documents.
func PastedDocExamples(root string) ([]string, error) {
	docExamples, err := PastedDocExamplesByDoc(root)
	if err != nil {
		return nil, err
	}
	examples := make([]string, 0, len(docExamples))
	for _, e := range docExamples {
		examples = append(examples, e.Line)
	}
	return examples, nil
}

// PastedDocExamplesByDoc is PastedDocExamples with the doc each example is
// pasted in.
func PastedDocExamplesByDoc(root string) ([]DocExample, error) {
	var examples []DocExample
	for _, f := range PastedDocs {
		raw, err := os.ReadFile(filepath.Join(root, f))
		if err != nil {
			return nil, fmt.Errorf("pasted-example doc %s: %w", filepath.ToSlash(f), err)
		}
		lines, err := pastedLinesInFencedBlocks(string(raw))
		if err != nil {
			return nil, fmt.Errorf("pasted-example doc %s: %w", filepath.ToSlash(f), err)
		}
		for _, l := range lines {
			examples = append(examples, DocExample{Doc: filepath.ToSlash(f), Line: l})
		}
	}
	return examples, nil
}

func shellLinesInFencedBlocks(md string) []string {
	var examples []string
	inFence := false
	for _, line := range strings.Split(md, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			inFence = !inFence
			continue
		}
		if inFence && strings.HasPrefix(trimmed, "$ ") {
			examples = append(examples, trimmed)
		}
	}
	return examples
}

// pastedLinesInFencedBlocks returns the $ lines of every fenced block and, for
// a block that is a pasted help banner, its `example:` lines through
// HelpExampleLines.
func pastedLinesInFencedBlocks(md string) ([]string, error) {
	examples := shellLinesInFencedBlocks(md)
	var block []string
	inFence := false
	for _, line := range strings.Split(md, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			if inFence {
				lines, err := blockHelpExamples(strings.Join(block, "\n"))
				if err != nil {
					return nil, err
				}
				examples = append(examples, lines...)
				block = block[:0]
			}
			inFence = !inFence
			continue
		}
		if inFence {
			block = append(block, line)
		}
	}
	return examples, nil
}

// blockHelpExamples returns a fenced block's `example:` lines, prefixed
// "example: ", or none when the block carries no `example:` heading. The tool
// is the first word of the first line under the heading.
func blockHelpExamples(block string) ([]string, error) {
	banner := "\n" + block + "\n"
	_, tail, found := strings.Cut(banner, "\nexample:\n")
	if !found {
		return nil, nil
	}
	first, _, _ := strings.Cut(strings.TrimSpace(tail), "\n")
	fields := strings.Fields(first)
	if len(fields) == 0 {
		return nil, fmt.Errorf("an `example:` block with no command under it")
	}
	lines, err := HelpExampleLines(banner, fields[0])
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(lines))
	for _, l := range lines {
		out = append(out, "example: "+l)
	}
	return out, nil
}

// HelpExampleLines wraps onboarding.ExampleLines: the `example:` lines of a
// help banner pasted in one of the PastedDocs are pasted examples too.
func HelpExampleLines(usage, tool string) ([]string, error) {
	return onboarding.ExampleLines(usage, tool)
}
