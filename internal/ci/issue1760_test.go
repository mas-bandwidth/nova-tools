package ci

import (
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// TestIssue1760 is the class test nova-tools #1760 asked for: docs/CLI.md's launch
// synopsis and the binary's usage line must name the same flags, in both directions.
//
// A 2026-09-19 dogfood ran `nova-pulse launch` exactly as docs/CLI.md spells it and
// could not place a card at all: the document's synopsis promised --benches and --bench,
// the shipped binary defined neither, and the binary's own usage line carried four flags
// (--routes, --floor, --key-env, --base-url) the document did not mention. There was also
// no --runner flag on the binary, so the only remedy for "runner not on PATH" was to
// mutate PATH itself -- which could put a stale nova-swarm in front of the current one.
//
// Two sources of truth for one flag set is one source of truth too many. This test reads
// both as text and refuses a drift, because a doc test that has to build a binary first
// is a doc test nobody runs.
func TestIssue1760(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	doc := readFile(t, filepath.Join(root, "docs", "CLI.md"))
	src := readFile(t, filepath.Join(root, "cmd", "nova-pulse", "main.go"))

	binary := launchFlagsFromUsage(src)
	documented := launchFlagsFromDoc(doc)

	if len(documented) == 0 {
		t.Fatal("docs/CLI.md gives no launch synopsis this test can read; the document changed shape")
	}
	if len(binary) == 0 {
		t.Fatal("cmd/nova-pulse/main.go holds no usage line beginning \"nova-pulse launch\"")
	}

	// Documented but not in binary: the doc promises flags the binary lacks.
	for _, f := range sortedStrs(documented) {
		if !binary[f] {
			t.Errorf("docs/CLI.md's launch promises %s, which the binary does not define (nova-tools #1760)", f)
		}
	}

	// In binary but not documented: the binary has flags the doc omits.
	for _, f := range sortedStrs(binary) {
		if !documented[f] {
			t.Errorf("nova-pulse launch defines %s, which docs/CLI.md's synopsis does not mention (nova-tools #1760)", f)
		}
	}
}

var flagRE = regexp.MustCompile(`--[a-z0-9][a-z0-9-]*`)

// launchFlagsFromUsage pulls the launch synopsis line from the Go source's usage string
// and returns the set of --flags it names.
func launchFlagsFromUsage(src string) map[string]bool {
	for _, line := range strings.Split(src, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, `nova-pulse launch`) && (strings.Contains(line, "<") || strings.Contains(line, "[")) {
			return flagSet(line)
		}
	}
	return nil
}

// launchFlagsFromDoc pulls the launch synopsis line from docs/CLI.md's fenced block
// inside the "### launch" section and returns the set of --flags it names.
func launchFlagsFromDoc(doc string) map[string]bool {
	var inSection, inFence bool
	for _, line := range strings.Split(doc, "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "### "):
			inSection = strings.TrimSpace(strings.TrimPrefix(line, "### ")) == "launch"
			inFence = false
			continue
		case !inSection:
			continue
		case trimmed == "```":
			inFence = !inFence
			continue
		}
		if inFence && strings.HasPrefix(line, "nova-pulse launch") && (strings.Contains(line, "<") || strings.Contains(line, "[")) {
			return flagSet(line)
		}
	}
	return nil
}

func flagSet(line string) map[string]bool {
	out := map[string]bool{}
	for _, f := range flagRE.FindAllString(line, -1) {
		out[f] = true
	}
	return out
}

func sortedStrs(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
