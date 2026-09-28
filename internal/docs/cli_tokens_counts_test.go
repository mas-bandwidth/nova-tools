package docs

import (
	"os"
	"strings"
	"testing"
)

// numberWords spells the counts this test expects a document sentence to carry.
var numberWords = map[int]string{
	1: "one", 2: "two", 3: "three", 4: "four", 5: "five", 6: "six",
	7: "seven", 8: "eight", 9: "nine", 10: "ten", 11: "eleven", 12: "twelve",
}

// docs/CLI.md is the command reference a stranger copies from, so a sentence
// in it about what a tool prints is a claim a reader trusts without running the
// tool. A COUNTED claim about a tool's own help output is worse: it rots
// silently as verbs are added, and nobody notices until someone counts.
//
// The banner in cmd/nova-tokens/main.go is the truth here, and the document is
// judged against it. `report` carries two usage lines and one name, which is
// why a count of usage lines and a count of verbs differ -- this test counts
// names, not lines.
//
// The number in each expected sentence is derived from the banner rather than
// hard-coded, so the test keeps its meaning when a verb is added.
func TestTheCLIReferenceCountsNovaTokensHelpCorrectly(t *testing.T) {
	t.Parallel()

	const source = "../../cmd/nova-tokens/main.go"
	raw, err := os.ReadFile(source)
	if err != nil {
		t.Fatalf("%s: %v; the banner printed by `nova-tokens help` lives in this file", source, err)
	}
	lines := strings.Split(string(raw), "\n")

	start, end := -1, -1
	for i, line := range lines {
		if start == -1 && strings.HasPrefix(line, "const usage = `") {
			start = i
			continue
		}
		if start != -1 && line == "`" {
			end = i
			break
		}
	}
	if start == -1 {
		t.Fatalf("%s: no line beginning \"const usage = `\"; the banner this test cuts is not there", source)
	}
	if end == -1 {
		t.Fatalf("%s: the banner opened at line %d never closes with a lone backtick at column one", source, start+1)
	}
	banner := lines[start+1 : end]

	usageAt := -1
	for i, line := range banner {
		if line == "usage:" {
			usageAt = i
			break
		}
	}
	if usageAt == -1 {
		t.Fatalf("%s: the banner carries no line exactly \"usage:\"", source)
	}
	verbs := map[string]bool{}
	for _, line := range banner[usageAt+1:] {
		if line == "" {
			break
		}
		if strings.HasPrefix(line, "  nova-tokens ") {
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				verbs[fields[1]] = true
			}
		}
	}
	if len(verbs) < 5 {
		t.Fatalf("%s: the usage block names %d distinct verbs; a scan that finds almost none would pass by asking nothing", source, len(verbs))
	}

	var blockCounts []int
	for i, line := range banner {
		if line != "example:" {
			continue
		}
		n := 0
		for _, after := range banner[i+1:] {
			if after == "" {
				break
			}
			if strings.HasPrefix(after, "  nova-tokens ") {
				n++
			}
		}
		blockCounts = append(blockCounts, n)
	}
	total := 0
	for _, n := range blockCounts {
		total += n
	}

	ref, err := os.ReadFile("../../docs/CLI.md")
	if err != nil {
		t.Fatalf("../../docs/CLI.md: %v", err)
	}
	doc := string(ref)

	verbWord, ok := numberWords[len(verbs)]
	if !ok {
		t.Fatalf("%s: the usage block names %d distinct verbs, outside the 1..12 this test can spell; widen numberWords", source, len(verbs))
	}
	verbClaim := "`nova-tokens help` lists all " + verbWord + " verbs."
	if !strings.Contains(doc, verbClaim) {
		t.Errorf("docs/CLI.md makes a counted claim about the tool's own help that is wrong: it should say %q, where the usage block in %s names %d distinct verbs (report has two usage lines and one name, which is why a line count and a verb count differ) -- correct the sentence",
			verbClaim, source, len(verbs))
	}

	exampleWord, ok := numberWords[total]
	if !ok {
		t.Fatalf("%s: the banner carries %d pasteable example lines, outside the 1..12 this test can spell; widen numberWords", source, total)
	}
	exampleClaim := "`nova-tokens help` carries " + exampleWord + " example lines"
	if !strings.Contains(doc, exampleClaim) {
		t.Errorf("docs/CLI.md makes a counted claim about the tool's own help that is wrong: it should say %q, where the banner in %s carries %d pasteable `nova-tokens ...` lines across %d example blocks -- correct the sentence",
			exampleClaim, source, total, len(blockCounts))
	}
}
