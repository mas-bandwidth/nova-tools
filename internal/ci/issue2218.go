package ci

import (
	"os"
	"path/filepath"
	"strings"

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

func goosValues(line string) []string {
	var vals []string
	for _, g := range knownGOOS {
		if strings.Contains(line, g) {
			vals = append(vals, g)
		}
	}
	return vals
}

// PastedDocExamples reads the named docs and returns every $  line inside a
// fenced code block (```-delimited). The docs are README.md, docs/USAGE.md,
// docs/CLI.md and docs/nova-swarm-quickstart.md, per SPEC-TOOLWORK §7 rule 7.
func PastedDocExamples(root string) ([]string, error) {
	files := []string{
		"README.md",
		filepath.Join("docs", "USAGE.md"),
		filepath.Join("docs", "CLI.md"),
		filepath.Join("docs", "nova-swarm-quickstart.md"),
	}
	var examples []string
	for _, f := range files {
		path := filepath.Join(root, f)
		raw, err := os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}
		examples = append(examples, shellLinesInFencedBlocks(string(raw))...)
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

// HelpExampleLines wraps onboarding.ExampleLines so the unexecuted-examples
// scanner has one entry point for `example:` lines a tool's help prints.
func HelpExampleLines(usage, tool string) ([]string, error) {
	return onboarding.ExampleLines(usage, tool)
}
