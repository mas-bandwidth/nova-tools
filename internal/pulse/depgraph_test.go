package pulse

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestParseCardDependencies(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    []string
	}{
		{
			name: "single depends-on",
			content: `# card-a
RESULT card-a sha=1234567890ab
depends-on: card-b
STEP 1. echo hello
`,
			want: []string{"card-b"},
		},
		{
			name: "comma separated",
			content: `depends-on: card-b, card-c, card-d
`,
			want: []string{"card-b", "card-c", "card-d"},
		},
		{
			name: "case insensitive and underscore",
			content: `DEPENDS_ON: card-x
`,
			want: []string{"card-x"},
		},
		{
			name: "lisp style list",
			content: `:depends-on ("E01-F01" "E01-F02")
`,
			want: []string{"E01-F01", "E01-F02"},
		},
		{
			name: "strips md extension and quotes",
			content: `depends-on: "card-1.md", 'card-2.md'
`,
			want: []string{"card-1", "card-2"},
		},
		{
			name: "multiple depends-on lines",
			content: `depends-on: card-1
LANE: core
depends-on: card-2, card-3
`,
			want: []string{"card-1", "card-2", "card-3"},
		},
		{
			name: "no dependencies",
			content: `# card-plain
LANE: test
STEP 1. echo hi
`,
			want: nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ParseCardDependencies(tc.content)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("ParseCardDependencies got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestDepGraphCycleDetection2Node(t *testing.T) {
	g := NewDependencyGraph()
	g.AddDependency("card-a", "card-b")
	g.AddDependency("card-b", "card-a")

	cycle := g.FindCycle()
	if len(cycle) == 0 {
		t.Fatalf("expected cycle, got none")
	}
	want := []string{"card-a", "card-b", "card-a"}
	if !reflect.DeepEqual(cycle, want) {
		t.Fatalf("expected cycle %v, got %v", want, cycle)
	}

	err := g.CheckCycles()
	if err == nil {
		t.Fatalf("expected error from CheckCycles, got nil")
	}
	if !errors.Is(err, ErrDependencyCycle) {
		t.Fatalf("expected ErrDependencyCycle, got %v", err)
	}
	wantMsg := "CYCLE REFUSED: card-a -> card-b -> card-a"
	if err.Error() != wantMsg {
		t.Fatalf("expected error message %q, got %q", wantMsg, err.Error())
	}
}

func TestDepGraphCycleDetection3Node(t *testing.T) {
	g := NewDependencyGraph()
	g.AddDependency("card-a", "card-b")
	g.AddDependency("card-b", "card-c")
	g.AddDependency("card-c", "card-a")

	cycle := g.FindCycle()
	want := []string{"card-a", "card-b", "card-c", "card-a"}
	if !reflect.DeepEqual(cycle, want) {
		t.Fatalf("expected cycle %v, got %v", want, cycle)
	}
}

func TestDepGraphSelfCycle(t *testing.T) {
	g := NewDependencyGraph()
	g.AddDependency("card-a", "card-a")

	cycle := g.FindCycle()
	want := []string{"card-a", "card-a"}
	if !reflect.DeepEqual(cycle, want) {
		t.Fatalf("expected cycle %v, got %v", want, cycle)
	}
}

func TestDepGraphAcyclicDAG(t *testing.T) {
	g := NewDependencyGraph()
	g.AddDependency("card-c", "card-b")
	g.AddDependency("card-b", "card-a")
	g.AddCard(&CardNode{ID: "card-a"})

	cycle := g.FindCycle()
	if len(cycle) != 0 {
		t.Fatalf("expected no cycle, got %v", cycle)
	}

	if err := g.CheckCycles(); err != nil {
		t.Fatalf("expected nil error for acyclic graph, got %v", err)
	}
}

func TestDepGraphCycleNormalization(t *testing.T) {
	rawCycle := []string{"card-z", "card-a", "card-m", "card-z"}
	normalized := NormalizeCycle(rawCycle)
	want := []string{"card-a", "card-m", "card-z", "card-a"}
	if !reflect.DeepEqual(normalized, want) {
		t.Fatalf("NormalizeCycle got %v, want %v", normalized, want)
	}
}

func TestCheckDirCycles(t *testing.T) {
	dir := t.TempDir()
	cardA := `RESULT card-a sha=1234567890ab
depends-on: card-b
`
	cardB := `RESULT card-b sha=1234567890cd
depends-on: card-a
`
	if err := os.WriteFile(filepath.Join(dir, "card-a.md"), []byte(cardA), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "card-b.md"), []byte(cardB), 0o644); err != nil {
		t.Fatal(err)
	}

	err := CheckDirCycles(dir)
	if err == nil {
		t.Fatalf("expected cycle error, got nil")
	}
	if !errors.Is(err, ErrDependencyCycle) {
		t.Fatalf("expected ErrDependencyCycle, got %v", err)
	}
	wantMsg := "CYCLE REFUSED: card-a -> card-b -> card-a"
	if err.Error() != wantMsg {
		t.Fatalf("expected %q, got %q", wantMsg, err.Error())
	}
}

func TestMutationTeethDepGraph(t *testing.T) {
	// A mutation test with teeth verifying cycle refusal invariants
	g := NewDependencyGraph()
	g.AddDependency("card-foo", "card-bar")
	g.AddDependency("card-bar", "card-foo")

	err := g.CheckCycles()
	if err == nil {
		t.Fatalf("TEETH: cycle must produce error")
	}

	msg := err.Error()
	if !strings.HasPrefix(msg, "CYCLE REFUSED: ") {
		t.Fatalf("TEETH: error must start with 'CYCLE REFUSED: ', got %q", msg)
	}

	parts := strings.Split(strings.TrimPrefix(msg, "CYCLE REFUSED: "), " -> ")
	if len(parts) < 2 {
		t.Fatalf("TEETH: cycle must contain at least 2 nodes in path, got %v", parts)
	}
	if parts[0] != parts[len(parts)-1] {
		t.Fatalf("TEETH: cycle must start and end with same node: %v", parts)
	}
}
