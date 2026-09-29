package definition

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/card"
	"github.com/mas-bandwidth/nova-tools/internal/cardhdr"
)

// mk builds a valid definition with the given ID and dependencies.
func mk(id string, deps ...string) Definition {
	return Definition{
		Schema: SchemaV2, ID: id, Title: "title of " + id, Kind: "fix-red", Tier: "flash",
		Paths: []string{"internal/queue/" + id + ".go"}, DependsOn: deps,
		Test:     cardhdr.TestLine{Package: "internal/queue", Name: "TestQueue"},
		DoneWhen: "the test passes", Doors: "none", Probes: "none",
		Brief: "# " + id + "\n\nThe brief.\n",
	}
}

// files renders definitions to sources named <id>.md and parses them, so the
// array carries the line numbers a file has.
func files(t *testing.T, defs ...Definition) []Definition {
	t.Helper()
	var srcs []Source
	for _, d := range defs {
		srcs = append(srcs, Source{Name: d.ID + "-" + string(rune('a'+len(srcs))) + ".md", Data: render(d)})
	}
	return mustParse(t, srcs)
}

func TestValidateAcceptsAnArrayAndReportsExternals(t *testing.T) {
	t.Parallel()
	defs := files(t, mk("a"), mk("b", "a"), mk("c", "b", "already-admitted"), mk("d", "already-admitted", "other"))
	rep, refs := validateL(defs)
	if len(refs) > 0 {
		t.Fatalf("%v", Lines(refs))
	}
	if !reflect.DeepEqual(rep.IDs, []string{"a", "b", "c", "d"}) {
		t.Fatalf("ids %v", rep.IDs)
	}
	want := []External{
		{ID: "c", File: "c-c.md", Dependency: "already-admitted"},
		{ID: "d", File: "d-d.md", Dependency: "already-admitted"},
		{ID: "d", File: "d-d.md", Dependency: "other"},
	}
	if !reflect.DeepEqual(rep.External, want) {
		t.Fatalf("external %+v", rep.External)
	}
	// An array of one.
	if _, refs := validateL(defs[:1]); len(refs) > 0 {
		t.Fatalf("%v", Lines(refs))
	}
}

func TestValidateRefusals(t *testing.T) {
	t.Parallel()
	t.Run("a repeated ID names both files", func(t *testing.T) {
		t.Parallel()
		defs := files(t, mk("a"), mk("b"), mk("a"))
		_, refs := validateL(defs)
		r, ok := hasRefusal(refs, "a-c.md", 3, "ID", CauseRepeatedID)
		if !ok {
			t.Fatalf("%v", Lines(refs))
		}
		if r.File != "a-c.md" || !strings.Contains(r.Limit, "a-a.md") {
			t.Fatalf("does not name both files: %s", r)
		}
		wellFormed(t, r)
	})
	t.Run("a card that depends on itself", func(t *testing.T) {
		t.Parallel()
		defs := files(t, mk("a", "a"))
		_, refs := validateL(defs)
		if _, ok := hasRefusal(refs, "a-a.md", 7, "DEPENDS-ON", CauseSelfDependent); !ok {
			t.Fatalf("%v", Lines(refs))
		}
	})
	t.Run("a cycle names its members", func(t *testing.T) {
		t.Parallel()
		defs := files(t, mk("a", "c"), mk("b", "a"), mk("c", "b"), mk("free"))
		_, refs := validateL(defs)
		r, ok := hasRefusal(refs, "a-a.md", 7, "DEPENDS-ON", CauseCycle)
		if !ok {
			t.Fatalf("%v", Lines(refs))
		}
		if r.Found != `"a -> c -> b -> a"` || !strings.Contains(r.Limit, "3 cards") || !strings.Contains(r.Limit, "a-a.md") || !strings.Contains(r.Limit, "c-c.md") {
			t.Fatalf("cycle %q limit %q", r.Found, r.Limit)
		}
		if len(refs) != 1 {
			t.Fatalf("one cycle, one refusal: %v", Lines(refs))
		}
	})
	t.Run("two cycles are two refusals", func(t *testing.T) {
		t.Parallel()
		defs := files(t, mk("a", "b"), mk("b", "a"), mk("x", "y"), mk("y", "x"))
		_, refs := validateL(defs)
		if len(refs) != 2 || refs[0].Cause != CauseCycle || refs[1].Cause != CauseCycle {
			t.Fatalf("%v", Lines(refs))
		}
	})
	t.Run("a diamond is not a cycle", func(t *testing.T) {
		t.Parallel()
		defs := files(t, mk("top", "l", "r"), mk("l", "base"), mk("r", "base"), mk("base"))
		if _, refs := validateL(defs); len(refs) > 0 {
			t.Fatalf("%v", Lines(refs))
		}
	})
	t.Run("empty and oversized arrays", func(t *testing.T) {
		t.Parallel()
		if _, refs := validateL(nil); len(refs) != 1 || refs[0].Cause != CauseEmptyArray {
			t.Fatalf("%v", Lines(refs))
		}
		big := make([]Definition, MaxFiles+1)
		if _, refs := validateL(big); len(refs) != 1 || refs[0].Cause != CauseTooMany || !strings.Contains(refs[0].Limit, "127") {
			t.Fatalf("%v", Lines(refs))
		}
	})
	t.Run("too many outside dependencies", func(t *testing.T) {
		t.Parallel()
		// 129 cards of 8 distinct outside prerequisites each: 1,032 guard entries
		// against the table's 1,024. (A request of 127 cards names at most 1,016, so
		// the bound holds by construction; this reaches the check directly.)
		var defs []Definition
		for i := 0; i <= card.MaxOutsideDependencies/card.MaxDependsOn; i++ {
			var deps []string
			for j := 0; j < card.MaxDependsOn; j++ {
				deps = append(deps, fmt.Sprintf("out-%d-%d", i, j))
			}
			defs = append(defs, mk(fmt.Sprintf("c%d", i), deps...))
		}
		var c card.Collector
		outsideDependencies(&c, defs, map[string]int{})
		refs := list(c.Err())
		if len(refs) != 1 || refs[0].Cause != CauseTooManyExternal || refs[0].Limit != "1024 outside dependencies" || !strings.Contains(refs[0].Found, "1032") {
			t.Fatalf("%v", Lines(refs))
		}
		// exactly at the bound is fine
		var ok card.Collector
		outsideDependencies(&ok, defs[:card.MaxOutsideDependencies/card.MaxDependsOn], map[string]int{})
		if ok.Err() != nil {
			t.Fatalf("%v", ok.Err().Lines())
		}
	})
	t.Run("127 cards of 8 dependencies name 1016 outside cards, within 1024", func(t *testing.T) {
		t.Parallel()
		if card.MaxChangedEntries*card.MaxDependsOn != 1016 || card.MaxChangedEntries*card.MaxDependsOn > card.MaxOutsideDependencies {
			t.Fatal("the array bound does not fit the guard-only entries")
		}
	})
}
