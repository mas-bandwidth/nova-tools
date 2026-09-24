package pulse

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestCardHeaderDependsOn(t *testing.T) {
	tests := []struct {
		name     string
		content  string
		wantDeps []string
		wantPrio int
		wantLane string
	}{
		{
			name:     "single dependency",
			content:  "RESULT card-1\ndepends-on: card-0\n",
			wantDeps: []string{"card-0"},
			wantPrio: 0,
		},
		{
			name:     "multiple comma separated dependencies",
			content:  "RESULT card-2\nDEPENDS-ON: card-0, card-1, card-extra\nPRIORITY: 10\nLANE: pulse\n",
			wantDeps: []string{"card-0", "card-1", "card-extra"},
			wantPrio: 10,
			wantLane: "pulse",
		},
		{
			name:     "quoted and spaced dependencies",
			content:  "RESULT card-3\nDepends-On: \"dep-a\", 'dep-b' , dep-c\npriority: 5\n",
			wantDeps: []string{"dep-a", "dep-b", "dep-c"},
			wantPrio: 5,
		},
		{
			name:     "path dependencies",
			content:  "RESULT card-4\ndepends-on: mas-bandwidth/nova-tools:internal/pulse/fill.go, lib/wire.go\n",
			wantDeps: []string{"mas-bandwidth/nova-tools:internal/pulse/fill.go", "lib/wire.go"},
			wantPrio: 0,
		},
		{
			name:     "empty depends-on header",
			content:  "RESULT card-5\ndepends-on: \n",
			wantDeps: nil,
			wantPrio: 0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			cardPath := filepath.Join(dir, "card.md")
			if err := os.WriteFile(cardPath, []byte(tc.content), 0o644); err != nil {
				t.Fatal(err)
			}

			info, err := ParseCard(cardPath)
			if err != nil {
				t.Fatalf("ParseCard failed: %v", err)
			}
			if !reflect.DeepEqual(info.Dependencies, tc.wantDeps) {
				t.Errorf("got dependencies %v, want %v", info.Dependencies, tc.wantDeps)
			}
			if info.Priority != tc.wantPrio {
				t.Errorf("got priority %d, want %d", info.Priority, tc.wantPrio)
			}
			if tc.wantLane != "" && info.Lane != tc.wantLane {
				t.Errorf("got lane %q, want %q", info.Lane, tc.wantLane)
			}
		})
	}
}

func TestCardSexpDependsOn(t *testing.T) {
	tests := []struct {
		name     string
		content  string
		wantDeps []string
		wantPrio int
	}{
		{
			name: "single sexp dependency",
			content: `(
  :id "E01-F02"
  :title "Uniform read bounds"
  :depends-on ("E01-F01")
  :priority 20
)`,
			wantDeps: []string{"E01-F01"},
			wantPrio: 20,
		},
		{
			name: "multi-line sexp dependencies",
			content: `(
  :id "E03-F04"
  :depends-on (
    "E03-F01"
    "E03-F02"
    "E03-F03"
  )
)`,
			wantDeps: []string{"E03-F01", "E03-F02", "E03-F03"},
			wantPrio: 0,
		},
		{
			name: "empty sexp dependencies",
			content: `(
  :id "E01-F01"
  :depends-on ()
)`,
			wantDeps: nil,
			wantPrio: 0,
		},
		{
			name: "combined header and sexp block",
			content: `RESULT card-combo
depends-on: dep-header-1
(
  :depends-on ("dep-sexp-2")
)
`,
			wantDeps: []string{"dep-header-1", "dep-sexp-2"},
			wantPrio: 0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			deps := ParseCardDependencies(tc.content)
			if !reflect.DeepEqual(deps, tc.wantDeps) {
				t.Errorf("got dependencies %v, want %v", deps, tc.wantDeps)
			}
		})
	}
}

func TestParseSexpDocumentDependencies(t *testing.T) {
	doc := `
(
  :current-features 3
  :nodes (
    (
      :id "E01-F01"
      :title "Reader"
      :depends-on ()
    )
    (
      :id "E01-F02"
      :title "Bounds"
      :depends-on ( "E01-F01" )
    )
    (
      :id "E01-F03"
      :title "Identity"
      :depends-on (
        "E01-F01"
        "E01-F02"
      )
    )
  )
)
`
	got := ParseSexpDependencies(doc)
	if len(got) != 3 {
		t.Fatalf("expected 3 nodes, got %d: %v", len(got), got)
	}
	if len(got["E01-F01"]) != 0 {
		t.Errorf("E01-F01 should have 0 deps, got %v", got["E01-F01"])
	}
	if !reflect.DeepEqual(got["E01-F02"], []string{"E01-F01"}) {
		t.Errorf("E01-F02 deps = %v, want [E01-F01]", got["E01-F02"])
	}
	if !reflect.DeepEqual(got["E01-F03"], []string{"E01-F01", "E01-F02"}) {
		t.Errorf("E01-F03 deps = %v, want [E01-F01, E01-F02]", got["E01-F03"])
	}
}

// TestCardDependsOnDashIsNone: the cut template writes "DEPENDS-ON: -" for a card with no
// dependency (FormatDependsOn). The fill gate must read that as none, not as a dependency named
// "-": on 2026-09-23 every holdfix card on hulk/vision/hetzner/space was HELD with
// "dependency - not merged into dev" while the table showed them as ready (rowan-tools phantom-ready).
func TestCardDependsOnDashIsNone(t *testing.T) {
	for _, content := range []string{
		"RESULT card-1\nDEPENDS-ON: -\n",
		"RESULT card-1\ndepends-on: -  \n",
		"(card :id c1 :depends-on (-))\n",
		"RESULT card-1\nDEPENDS-ON: -, card-0\n",
	} {
		got := ParseCardDependencies(content)
		for _, d := range got {
			if d == "-" {
				t.Errorf("ParseCardDependencies(%q) = %q; \"-\" means no dependency", content, got)
			}
		}
	}
	if ok, reason := NewGitAndResultsChecker(t.TempDir(), "dev", t.TempDir()).IsDependencyMerged("-"); !ok {
		t.Errorf("IsDependencyMerged(\"-\") = false (%s); \"-\" means no dependency", reason)
	}
}
