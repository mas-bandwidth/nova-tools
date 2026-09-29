package definition

import (
	"os"
	"strings"
	"testing"
)

// docExamples returns the fenced blocks of the doc that carry the info string.
func docExamples(t *testing.T, path, info string) []string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	parts := strings.Split(string(b), "```"+info+"\n")
	for _, p := range parts[1:] {
		end := strings.Index(p, "\n```")
		if end < 0 {
			t.Fatalf("an unclosed %s block in %s", info, path)
		}
		out = append(out, p[:end+1])
	}
	return out
}

// B10: every example card in docs/SPEC-CARD.md parses, and admits.
func TestEveryExampleCardInTheDocParses(t *testing.T) {
	t.Parallel()
	examples := docExamples(t, "../../../docs/SPEC-CARD.md", "card")
	if len(examples) == 0 {
		t.Fatal("the doc has no example card")
	}
	for i, ex := range examples {
		defs, refs := parseL(one("example.md", ex))
		if len(refs) > 0 || len(defs) != 1 {
			t.Errorf("example %d: %v\n%s", i, Lines(refs), ex)
			continue
		}
		// the doc's example is complete: every profile key is present
		for _, k := range requiredKeys {
			if defs[0].Lines[k] == 0 {
				t.Errorf("example %d lacks %s", i, k)
			}
		}
		// and the brief begins with prose that is not a header line
		if !strings.HasPrefix(defs[0].Brief, "Goal:") {
			t.Errorf("example %d: the brief is %q", i, defs[0].Brief[:20])
		}
		// it round-trips through the array rules with its outside dependencies reported
		rep, vrefs := validateL(defs)
		if len(vrefs) > 0 || len(rep.External) != 2 {
			t.Errorf("example %d: %v %+v", i, Lines(vrefs), rep)
		}
	}
}

// The doc's refusal example is what a refusal renders as.
func TestTheDocsRefusalLineIsTheShapeOfARefusal(t *testing.T) {
	t.Parallel()
	_, refs := parseL(one("cards/x.md", replaceLine(5, "KIND: nonsense")))
	r, ok := hasRefusal(refs, "cards/x.md", 5, "KIND", CauseInvalidKind)
	if !ok {
		t.Fatal(Lines(refs))
	}
	if !strings.HasPrefix(r.String(), "refused parse file=cards/x.md line=5 field=KIND: invalid-kind; found \"nonsense\"; limit KIND is one of internal/hygiene/kinds.txt: ") {
		t.Fatalf("%s", r)
	}
}
