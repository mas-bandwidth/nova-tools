package definition

import (
	"reflect"
	"strings"
	"testing"

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
		srcs = append(srcs, Source{Name: d.ID + "-" + string(rune('a'+len(srcs))) + ".md", Data: Render(d)})
	}
	return mustParse(t, srcs)
}

func TestValidateAcceptsAnArrayAndReportsExternals(t *testing.T) {
	t.Parallel()
	defs := files(t, mk("a"), mk("b", "a"), mk("c", "b", "already-admitted"), mk("d", "already-admitted", "other"))
	rep, refs := Validate(defs)
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
	if _, refs := Validate(defs[:1]); len(refs) > 0 {
		t.Fatalf("%v", Lines(refs))
	}
}

func TestValidateRefusals(t *testing.T) {
	t.Parallel()
	t.Run("a repeated ID names both files", func(t *testing.T) {
		t.Parallel()
		defs := files(t, mk("a"), mk("b"), mk("a"))
		_, refs := Validate(defs)
		r, ok := hasRefusal(refs, "a-c.md", 3, "ID", CauseDuplicateID)
		if !ok {
			t.Fatalf("%v", Lines(refs))
		}
		if !strings.Contains(r.Found, "a-a.md") || !strings.Contains(r.Found, "a-c.md") || len(r.Also) != 1 || r.Also[0] != "a-a.md" {
			t.Fatalf("does not name both files: %s", r)
		}
		wellFormed(t, r)
	})
	t.Run("a card that depends on itself", func(t *testing.T) {
		t.Parallel()
		defs := files(t, mk("a", "a"))
		_, refs := Validate(defs)
		if _, ok := hasRefusal(refs, "a-a.md", 7, "DEPENDS-ON", CauseSelfDependent); !ok {
			t.Fatalf("%v", Lines(refs))
		}
	})
	t.Run("a cycle names its members", func(t *testing.T) {
		t.Parallel()
		defs := files(t, mk("a", "c"), mk("b", "a"), mk("c", "b"), mk("free"))
		_, refs := Validate(defs)
		r, ok := hasRefusal(refs, "a-a.md", 7, "DEPENDS-ON", CauseCycle)
		if !ok {
			t.Fatalf("%v", Lines(refs))
		}
		if r.Found != "a -> c -> b -> a" || len(r.Also) != 2 {
			t.Fatalf("cycle %q also %v", r.Found, r.Also)
		}
		if len(refs) != 1 {
			t.Fatalf("one cycle, one refusal: %v", Lines(refs))
		}
	})
	t.Run("two cycles are two refusals", func(t *testing.T) {
		t.Parallel()
		defs := files(t, mk("a", "b"), mk("b", "a"), mk("x", "y"), mk("y", "x"))
		_, refs := Validate(defs)
		if len(refs) != 2 || refs[0].Cause != CauseCycle || refs[1].Cause != CauseCycle {
			t.Fatalf("%v", Lines(refs))
		}
	})
	t.Run("a diamond is not a cycle", func(t *testing.T) {
		t.Parallel()
		defs := files(t, mk("top", "l", "r"), mk("l", "base"), mk("r", "base"), mk("base"))
		if _, refs := Validate(defs); len(refs) > 0 {
			t.Fatalf("%v", Lines(refs))
		}
	})
	t.Run("empty and oversized arrays", func(t *testing.T) {
		t.Parallel()
		if _, refs := Validate(nil); len(refs) != 1 || refs[0].Cause != CauseEmptyArray {
			t.Fatalf("%v", Lines(refs))
		}
		big := make([]Definition, MaxFiles+1)
		if _, refs := Validate(big); len(refs) != 1 || refs[0].Cause != CauseTooManyFiles || !strings.Contains(refs[0].Found, "128") {
			t.Fatalf("%v", Lines(refs))
		}
	})
	t.Run("a hand-built definition meets the field rules", func(t *testing.T) {
		t.Parallel()
		d := mk("a")
		d.Kind, d.Paths, d.Tier = "nonsense", []string{"../x"}, "huge"
		d.Test = cardhdr.TestLine{None: true, Why: "why"}
		_, refs := Validate([]Definition{d})
		for _, c := range []struct {
			key   string
			cause Cause
		}{{"KIND", CauseInvalidKind}, {"PATHS", CauseInvalidPaths}, {"TIER", CauseInvalidTier}} {
			if _, ok := hasRefusal(refs, "#1", 0, c.key, c.cause); !ok {
				t.Errorf("no %s refusal in %v", c.key, Lines(refs))
			}
		}
		d = mk("a")
		d.Test = cardhdr.TestLine{None: true, Why: "why"} // fix-red lands a pull request
		if _, refs := Validate([]Definition{d}); len(refs) != 1 || refs[0].Cause != CauseInvalidTest {
			t.Errorf("%v", Lines(refs))
		}
		d = mk("a")
		d.Brief = " \n"
		if _, refs := Validate([]Definition{d}); len(refs) != 1 || refs[0].Cause != CauseNoBrief {
			t.Errorf("%v", Lines(refs))
		}
	})
}
