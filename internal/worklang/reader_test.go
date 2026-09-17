package worklang_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/worklang"
)

// The reader contract of docs/SPEC-WORKLANG.md, seen red first: three tests,
// no network and no model call, every refusal named by the field it refuses.
func TestWorklangReader(t *testing.T) {
	// worklang-reader-refuses-a-dispatch-macro: a `#.` anywhere in code
	// position is refused at exit 2 naming the byte offset, the refusal the
	// work file already owes.
	t.Run("worklang-reader-refuses-a-dispatch-macro", func(t *testing.T) {
		data := []byte("(:plan :version 1 :goal #.(error \"x\"))\n")
		offset := strings.Index(string(data), "#")
		if offset < 0 {
			t.Fatal("fixture has no dispatch macro")
		}
		_, err := worklang.Read("work.work", data, worklang.DefaultLimits())
		if err == nil {
			t.Fatal("a #. dispatch macro was read instead of refused")
		}
		ref := assertRefusal(t, err)
		msg := ref.Error()
		if !strings.Contains(msg, "dispatch macro") {
			t.Errorf("refusal does not name the dispatch macro: %s", msg)
		}
		if want := fmt.Sprintf("byte=%d", offset); !strings.Contains(msg, want) {
			t.Errorf("refusal does not name the byte offset %q: %s", want, msg)
		}
		if !strings.Contains(msg, "work.work") {
			t.Errorf("refusal does not name the file: %s", msg)
		}
	})

	// worklang-reader-enforces-the-three-bounds: a plan past --max-bytes,
	// --max-depth or --max-nodes is refused before parsing finishes, naming the
	// bound and the file, never truncated.
	t.Run("worklang-reader-enforces-the-three-bounds", func(t *testing.T) {
		wide := worklang.Limits{MaxBytes: 1 << 20, MaxDepth: 64, MaxNodes: 4096}

		t.Run("max-bytes", func(t *testing.T) {
			data := []byte("(:plan :version 1 (:node :id \"n1\" :kind docs))")
			limits := wide
			limits.MaxBytes = 8
			_, err := worklang.Read("work.work", data, limits)
			assertBound(t, err, "max-bytes")
		})
		t.Run("max-depth", func(t *testing.T) {
			data := []byte("(:plan (:a (:b (:c (:d)))))")
			limits := wide
			limits.MaxDepth = 2
			_, err := worklang.Read("work.work", data, limits)
			assertBound(t, err, "max-depth")
		})
		t.Run("max-nodes", func(t *testing.T) {
			data := []byte("(:plan :version 1 :clip :per-node)")
			limits := wide
			limits.MaxNodes = 2
			_, err := worklang.Read("work.work", data, limits)
			assertBound(t, err, "max-nodes")
		})
	})

	// worklang-unknown-kind-is-a-refusal: `:kind bogus` is refused naming the
	// field; an unknown key beside it is preserved and ignored, unchanged.
	t.Run("worklang-unknown-kind-is-a-refusal", func(t *testing.T) {
		good := []byte("(:plan :version 1 (:node :id \"n1\" :kind docs :bespoke \"kept\"))")
		plan, err := worklang.ParsePlan("work.work", good, worklang.DefaultLimits())
		if err != nil {
			t.Fatalf("a valid plan carrying an unknown key was refused: %v", err)
		}
		if len(plan.Nodes) != 1 {
			t.Fatalf("nodes = %d, want 1", len(plan.Nodes))
		}
		if _, ok := plan.Nodes[0].Unknown["bespoke"]; !ok {
			t.Errorf("an unknown key beside :kind was dropped, not preserved: %#v", plan.Nodes[0].Unknown)
		}

		bad := []byte("(:plan :version 1 (:node :id \"n1\" :kind bogus :bespoke \"kept\"))")
		_, err = worklang.ParsePlan("work.work", bad, worklang.DefaultLimits())
		if err == nil {
			t.Fatal("an unknown :kind was accepted instead of refused")
		}
		ref := assertRefusal(t, err)
		msg := ref.Error()
		if !strings.Contains(msg, ":kind") {
			t.Errorf("refusal does not name the field: %s", msg)
		}
		if !strings.Contains(msg, "bogus") {
			t.Errorf("refusal does not name the value: %s", msg)
		}
	})
}

// assertRefusal checks the shape every refusal shares: it is a *worklang.Refusal
// and it exits 2.
func assertRefusal(t *testing.T, err error) *worklang.Refusal {
	t.Helper()
	if err == nil {
		t.Fatal("want a refusal, got nil")
	}
	var ref *worklang.Refusal
	if !errors.As(err, &ref) {
		t.Fatalf("want *worklang.Refusal, got %T: %v", err, err)
	}
	if ref.ExitCode() != 2 {
		t.Errorf("exit = %d, want 2", ref.ExitCode())
	}
	return ref
}

// assertBound checks one bound refusal: exit 2, the bound named, the file
// named, and the whole input refused rather than truncated.
func assertBound(t *testing.T, err error, bound string) {
	t.Helper()
	if err == nil {
		t.Fatalf("input past --%s was read instead of refused", bound)
	}
	ref := assertRefusal(t, err)
	msg := ref.Error()
	if !strings.Contains(msg, bound) {
		t.Errorf("refusal does not name --%s: %s", bound, msg)
	}
	if !strings.Contains(msg, "work.work") {
		t.Errorf("refusal does not name the file: %s", msg)
	}
}
