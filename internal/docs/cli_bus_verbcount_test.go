package docs

import (
	"os"
	"sort"
	"strings"
	"testing"
)

// cli_bus_verbcount_test.go holds docs/CLI.md — the command reference a
// stranger copies from — against the verb list its own nova-bus section claims.
//
// A COUNTED claim about a tool's own verb list is the kind of sentence a reader
// trusts without checking, and the kind that rots silently as verbs are added:
// `prepare` and `reply` arrived after the number was written. The banner in the
// source is the truth; the document is judged against it. `draft` and `send`
// each carry two usage lines but one name, which is why the verb count and the
// raw line count differ. The number is derived from the banner rather than
// hard-coded, so the test keeps its meaning when a verb is added. It reads text
// and runs nothing.

// busMainPath holds the banner this test treats as the truth.
const busMainPath = "../../cmd/nova-bus/main.go"

// cliBusPath is the command reference judged against the banner.
const cliBusPath = "../../docs/CLI.md"

// verbWords spells the small counts this test can judge. A count outside the
// range is a t.Fatal, not a guessed word.
var verbWords = map[int]string{
	1:  "one",
	2:  "two",
	3:  "three",
	4:  "four",
	5:  "five",
	6:  "six",
	7:  "seven",
	8:  "eight",
	9:  "nine",
	10: "ten",
	11: "eleven",
	12: "twelve",
}

// TestTheCLIReferenceCountsNovaBusVerbsCorrectly reads the nova-bus banner and
// holds docs/CLI.md to the number of distinct verbs the banner names, in both
// places the reference states it.
func TestTheCLIReferenceCountsNovaBusVerbsCorrectly(t *testing.T) {
	t.Parallel()

	verbs := busUsageVerbs(t)

	word, ok := verbWords[len(verbs)]
	if !ok {
		t.Fatalf("the nova-bus banner names %d distinct verbs (%s); this test spells counts 1..12 and cannot judge a count outside that range — extend the lookup rather than trusting a number it cannot write",
			len(verbs), strings.Join(verbs, ", "))
	}

	cli, err := os.ReadFile(cliBusPath)
	if err != nil {
		t.Fatalf("%s: %v; docs/CLI.md is the command reference a stranger copies from", cliBusPath, err)
	}
	doc := string(cli)

	claim := "`nova-bus` is " + word + " verbs over that."
	if !strings.Contains(doc, claim) {
		t.Errorf("the nova-bus banner names %d distinct verbs (%s), but %s does not contain %q; a counted claim about a tool's own verb list is the kind of sentence a reader trusts without checking, and the banner in the source is the truth the document is judged against",
			len(verbs), strings.Join(verbs, ", "), cliBusPath, claim)
	}

	heading := "### The " + word + " verbs"
	if !hasExactLine(doc, heading) {
		t.Errorf("the nova-bus banner names %d distinct verbs (%s), but %s has no line exactly %q; a counted heading about a tool's own verb list is the kind of sentence a reader trusts without checking, and it rots silently as verbs are added",
			len(verbs), strings.Join(verbs, ", "), cliBusPath, heading)
	}
}

// busUsageVerbs cuts the `const usage` banner from cmd/nova-bus/main.go and
// returns the distinct verb names in its `usage:` block, sorted. It t.Fatal's
// when either banner end is missing, when the `usage:` line is absent, or when
// the scan finds fewer than five verbs — a scan that finds almost none would
// pass by asking nothing.
func busUsageVerbs(t *testing.T) []string {
	t.Helper()

	src, err := os.ReadFile(busMainPath)
	if err != nil {
		t.Fatalf("%s: %v; the banner in the source is the truth this test reads", busMainPath, err)
	}

	var banner []string
	started, closed := false, false
	for _, line := range strings.Split(string(src), "\n") {
		if !started {
			if strings.HasPrefix(line, "const usage = `") {
				started = true
			}
			continue
		}
		if line == "`" {
			closed = true
			break
		}
		banner = append(banner, line)
	}
	if !started {
		t.Fatalf("%s: no line begins %q, so the banner cannot be cut; the banner in the source is the truth this test reads",
			busMainPath, "const usage = `")
	}
	if !closed {
		t.Fatalf("%s: the banner, opened by %q, has no closing line that is one backtick at column one; the banner in the source is the truth this test reads",
			busMainPath, "const usage = `")
	}

	seen := map[string]bool{}
	var verbs []string
	inUsage := false
	for _, line := range banner {
		if !inUsage {
			if line == "usage:" {
				inUsage = true
			}
			continue
		}
		if line == "" {
			break
		}
		if !strings.HasPrefix(line, "  nova-bus ") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		if name := fields[1]; !seen[name] {
			seen[name] = true
			verbs = append(verbs, name)
		}
	}
	if !inUsage {
		t.Fatalf("%s: the banner has no line exactly %q, so no verb list can be read; the banner in the source is the truth this test reads",
			busMainPath, "usage:")
	}
	if len(verbs) < 5 {
		t.Fatalf("%s: the usage scan found only %d distinct verbs (%s); a scan that finds almost none would pass by asking nothing",
			busMainPath, len(verbs), strings.Join(verbs, ", "))
	}
	sort.Strings(verbs)
	return verbs
}

// hasExactLine reports whether text holds a line equal to want.
func hasExactLine(text, want string) bool {
	for _, line := range strings.Split(text, "\n") {
		if line == want {
			return true
		}
	}
	return false
}
