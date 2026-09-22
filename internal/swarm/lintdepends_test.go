package swarm

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// #2636, THE LINT THIRD. Three refusals and two passes. The card under test is
// the typedCard fixture, whose contract line names its own id as CARD-0000.

func dependsHeader(value string) []byte {
	h := append(fullHeader(), "DEPENDS-ON: "+value)
	return typedCard(h...)
}

func TestLintDependsRefusesAMissingKey(t *testing.T) {
	fs := LintCardDepends(typedCard(fullHeader()...), nil)
	if len(fs) != 1 || fs[0].Check != "depends-on" {
		t.Fatalf("a typed card with no DEPENDS-ON key draws depends-on, got %v", fs)
	}
	if !strings.Contains(fs[0].Excerpt, "DEPENDS-ON") {
		t.Fatalf("the refusal names the key: %q", fs[0].Excerpt)
	}
}

func TestLintDependsRefusesASelfDependency(t *testing.T) {
	fs := LintCardDepends(dependsHeader("other-card, CARD-0000"), Lineup{"other-card": true})
	if len(fs) != 1 || !strings.Contains(fs[0].Excerpt, "CARD-0000") || !strings.Contains(fs[0].Excerpt, "own id") {
		t.Fatalf("a card that names its own id is a self-dependency, got %v", fs)
	}
	if strings.Contains(fs[0].Excerpt, "not in the lineup") {
		t.Fatalf("a self-dependency is not reported as an unknown id: %q", fs[0].Excerpt)
	}
}

func TestLintDependsRefusesAnUnknownID(t *testing.T) {
	fs := LintCardDepends(dependsHeader("missing-card"), Lineup{"other-card": true})
	if len(fs) != 1 || !strings.Contains(fs[0].Excerpt, "missing-card") || !strings.Contains(fs[0].Excerpt, "not in the lineup") {
		t.Fatalf("an id the lineup does not hold is refused, got %v", fs)
	}
	// No lineup is not an empty lineup: the lint does not guess.
	if fs := LintCardDepends(dependsHeader("missing-card"), nil); len(fs) != 0 {
		t.Fatalf("with no lineup an id is not called unknown, got %v", fs)
	}
}

func TestLintDependsDashPasses(t *testing.T) {
	if fs := LintCardDepends(dependsHeader("-"), Lineup{"other-card": true}); len(fs) != 0 {
		t.Fatalf("DEPENDS-ON: - passes, and `-` is not looked up as an id, got %v", fs)
	}
}

func TestLintDependsKnownIDPasses(t *testing.T) {
	lineup := Lineup{"other-card": true, "third-card": true}
	if fs := LintCardDepends(dependsHeader("other-card, third-card"), lineup); len(fs) != 0 {
		t.Fatalf("ids the lineup holds pass, got %v", fs)
	}
}

func TestReadLineupSkipsTheDependsOnHeader(t *testing.T) {
	p := filepath.Join(t.TempDir(), "ORDER.tsv")
	body := "id\tdepends-on\nother-card\t-\nthird-card\tother-card\n# a comment\n\n"
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := ReadLineup(p)
	if err != nil {
		t.Fatal(err)
	}
	if !got["other-card"] || !got["third-card"] || got["id"] || got["-"] || got["depends-on"] {
		t.Fatalf("the lineup is the id column, not the header and not `-`: %v", got)
	}
}
