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
	t.Parallel()

	// worklang-reader-refuses-a-dispatch-macro: a `#.` anywhere in code
	// position is refused at exit 2 naming the byte offset, the refusal the
	// work file already owes.
	t.Run("worklang-reader-refuses-a-dispatch-macro", func(t *testing.T) {
		data := []byte("(:plan :version 1 :goal #.(error \"x\"))\n")
		offset := strings.Index(string(data), "#")
		if offset < 0 {
			t.Fatal("fixture has no dispatch macro")
		}
		_, err := worklang.Read("work.work", data, testLimits())
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

	// worklang-reader-refuses-every-escape-token: each of the reader macros
	// that would evaluate or escape is refused at its own byte, and the same
	// byte inside a string or a comment is text.
	t.Run("worklang-reader-refuses-every-escape-token", func(t *testing.T) {
		for _, c := range []struct{ tok, name string }{
			{"#", "dispatch macro"},
			{"|", "multiple escape"},
			{"'", "quote"},
			{"`", "backquote"},
			{",", "unquote"},
			{"\\", "single escape"},
		} {
			data := []byte("(tree " + c.tok + "x)")
			_, err := worklang.Read("tree.lisp", data, testLimits())
			if err == nil {
				t.Fatalf("%q in code position was read instead of refused", c.tok)
			}
			msg := assertRefusal(t, err).Error()
			if !strings.Contains(msg, c.name) || !strings.Contains(msg, "byte=6") {
				t.Errorf("refusal for %q does not name %q at byte=6: %s", c.tok, c.name, msg)
			}
			opaque := []byte("(tree \"a" + c.tok + "b\" ; " + c.tok + "\n 1)")
			if _, err := worklang.Read("tree.lisp", opaque, testLimits()); err != nil {
				t.Errorf("%q inside a string or a comment was refused: %v", c.tok, err)
			}
		}
	})

	// worklang-reader-reads-the-five-kinds: one list holding each kind comes
	// back decoded, with the byte span of every form.
	t.Run("worklang-reader-reads-the-five-kinds", func(t *testing.T) {
		data := []byte("(a :k \"s\\\"q\" -12 ())")
		f, err := worklang.Read("tree.lisp", data, testLimits())
		if err != nil {
			t.Fatal(err)
		}
		kinds := []worklang.Kind{worklang.Symbol, worklang.Keyword, worklang.String, worklang.Integer, worklang.List}
		if f.Kind != worklang.List || len(f.List) != len(kinds) {
			t.Fatalf("form = %#v", f)
		}
		for i, k := range kinds {
			if f.List[i].Kind != k {
				t.Errorf("element %d is kind %d, want %d", i, f.List[i].Kind, k)
			}
		}
		if f.List[1].Value != "k" || f.List[2].Value != "s\"q" || f.List[3].Int != -12 {
			t.Errorf("decoded values wrong: %#v", f.List)
		}
		if f.Offset != 0 || f.End != len(data) || f.List[1].Offset != 3 {
			t.Errorf("spans wrong: list %d..%d, keyword at %d", f.Offset, f.End, f.List[1].Offset)
		}
	})

	// worklang-reader-refuses-a-malformed-file: unbalanced, trailing and
	// unterminated input is refused whole.
	t.Run("worklang-reader-refuses-a-malformed-file", func(t *testing.T) {
		for _, src := range []string{"(a b", "(a) (b)", "(a \"b)", "", ":", "12x"} {
			_, err := worklang.Read("tree.lisp", []byte(src), testLimits())
			if err == nil {
				t.Errorf("%q was read instead of refused", src)
				continue
			}
			assertRefusal(t, err)
		}
		if _, err := worklang.Read("tree.lisp", []byte("(a)"), worklang.Limits{}); err == nil {
			t.Error("a zero Limits was accepted")
		}
	})
}

// testLimits is 64 KiB, depth 64 and 4096 nodes.
func testLimits() worklang.Limits {
	return worklang.Limits{MaxBytes: 65536, MaxDepth: 64, MaxNodes: 4096}
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
