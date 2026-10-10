package sexp_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/roadmap/sexp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The reader contract of docs/SPEC-WORKLANG.md, seen red first: three tests,
// no network and no model call, every refusal named by the field it refuses.
func TestWorklangReader(t *testing.T) {
	t.Parallel()

	// worklang-reader-refuses-a-dispatch-macro: a `#.` anywhere in code
	// position is refused at exit 2 naming the byte offset, the refusal the
	// work file already owes.
	t.Run("worklang-reader-refuses-a-dispatch-macro", func(t *testing.T) {
		t.Parallel()
		data := []byte("(:plan :version 1 :goal #.(error \"x\"))\n")
		offset := strings.Index(string(data), "#")
		require.GreaterOrEqual(t, offset, 0, "fixture has no dispatch macro")
		_, err := sexp.Read("work.work", data, testLimits())
		require.Error(t, err, "a #. dispatch macro was read instead of refused")
		ref := assertRefusal(t, err)
		msg := ref.Error()
		assert.Contains(t, msg, "dispatch macro", "refusal does not name the dispatch macro: %s", msg)
		want := fmt.Sprintf("byte=%d", offset)
		assert.Contains(t, msg, want, "refusal does not name the byte offset %q: %s", want, msg)
		assert.Contains(t, msg, "work.work", "refusal does not name the file: %s", msg)
	})

	// worklang-reader-enforces-the-three-bounds: a plan past --max-bytes,
	// --max-depth or --max-nodes is refused before parsing finishes, naming the
	// bound and the file, never truncated.
	t.Run("worklang-reader-enforces-the-three-bounds", func(t *testing.T) {
		t.Parallel()
		wide := sexp.Limits{MaxBytes: 1 << 20, MaxDepth: 64, MaxNodes: 4096}

		t.Run("max-bytes", func(t *testing.T) {
			t.Parallel()
			data := []byte("(:plan :version 1 (:node :id \"n1\" :kind docs))")
			limits := wide
			limits.MaxBytes = 8
			_, err := sexp.Read("work.work", data, limits)
			assertBound(t, err, "max-bytes")
		})
		t.Run("max-depth", func(t *testing.T) {
			t.Parallel()
			data := []byte("(:plan (:a (:b (:c (:d)))))")
			limits := wide
			limits.MaxDepth = 2
			_, err := sexp.Read("work.work", data, limits)
			assertBound(t, err, "max-depth")
		})
		t.Run("max-nodes", func(t *testing.T) {
			t.Parallel()
			data := []byte("(:plan :version 1 :clip :per-node)")
			limits := wide
			limits.MaxNodes = 2
			_, err := sexp.Read("work.work", data, limits)
			assertBound(t, err, "max-nodes")
		})
	})

	// worklang-reader-refuses-every-escape-token: each of the reader macros
	// that would evaluate or escape is refused at its own byte, and the same
	// byte inside a string or a comment is text.
	t.Run("worklang-reader-refuses-every-escape-token", func(t *testing.T) {
		t.Parallel()
		for _, c := range []struct{ tok, name string }{
			{"#", "dispatch macro"},
			{"|", "multiple escape"},
			{"'", "quote"},
			{"`", "backquote"},
			{",", "unquote"},
			{"\\", "single escape"},
		} {
			t.Run(c.name, func(t *testing.T) {
				t.Parallel()
				data := []byte("(tree " + c.tok + "x)")
				_, err := sexp.Read("tree.lisp", data, testLimits())
				require.Error(t, err, "%q in code position was read instead of refused", c.tok)
				msg := assertRefusal(t, err).Error()
				assert.Contains(t, msg, c.name, "refusal for %q does not name %q at byte=6: %s", c.tok, c.name, msg)
				assert.Contains(t, msg, "byte=6", "refusal for %q does not name %q at byte=6: %s", c.tok, c.name, msg)
				opaque := []byte("(tree \"a" + c.tok + "b\" ; " + c.tok + "\n 1)")
				_, err = sexp.Read("tree.lisp", opaque, testLimits())
				assert.NoError(t, err, "%q inside a string or a comment was refused: %v", c.tok, err)
			})
		}
	})

	// worklang-reader-reads-the-five-kinds: one list holding each kind comes
	// back decoded, with the byte span of every form.
	t.Run("worklang-reader-reads-the-five-kinds", func(t *testing.T) {
		t.Parallel()
		data := []byte("(a :k \"s\\\"q\" -12 ())")
		f, err := sexp.Read("tree.lisp", data, testLimits())
		require.NoError(t, err)
		kinds := []sexp.Kind{sexp.Symbol, sexp.Keyword, sexp.String, sexp.Integer, sexp.List}
		require.Equal(t, sexp.List, f.Kind, "form = %#v", f)
		require.Len(t, f.List, len(kinds), "form = %#v", f)
		for i, k := range kinds {
			assert.Equal(t, k, f.List[i].Kind, "element %d is kind %d, want %d", i, f.List[i].Kind, k)
		}
		assert.Equal(t, "k", f.List[1].Value, "decoded values wrong: %#v", f.List)
		assert.Equal(t, "s\"q", f.List[2].Value, "decoded values wrong: %#v", f.List)
		assert.Equal(t, int64(-12), f.List[3].Int, "decoded values wrong: %#v", f.List)
		assert.Equal(t, 0, f.Offset, "spans wrong: list %d..%d, keyword at %d", f.Offset, f.End, f.List[1].Offset)
		assert.Equal(t, len(data), f.End, "spans wrong: list %d..%d, keyword at %d", f.Offset, f.End, f.List[1].Offset)
		assert.Equal(t, 3, f.List[1].Offset, "spans wrong: list %d..%d, keyword at %d", f.Offset, f.End, f.List[1].Offset)
	})

	// worklang-reader-refuses-a-malformed-file: unbalanced, trailing and
	// unterminated input is refused whole.
	t.Run("worklang-reader-refuses-a-malformed-file", func(t *testing.T) {
		t.Parallel()
		for _, src := range []string{"(a b", "(a) (b)", "(a \"b)", "", ":", "12x"} {
			t.Run(src, func(t *testing.T) {
				t.Parallel()
				_, err := sexp.Read("tree.lisp", []byte(src), testLimits())
				require.Error(t, err, "%q was read instead of refused", src)
				_ = assertRefusal(t, err) // ignored: the helper asserts the refusal's shape; this caller needs nothing from it
			})
		}
		t.Run("zero-limits", func(t *testing.T) {
			t.Parallel()
			_, err := sexp.Read("tree.lisp", []byte("(a)"), sexp.Limits{})
			assert.Error(t, err, "a zero Limits was accepted")
		})
	})
}

// testLimits is 64 KiB, depth 64 and 4096 nodes.
func testLimits() sexp.Limits {
	return sexp.Limits{MaxBytes: 65536, MaxDepth: 64, MaxNodes: 4096}
}

// assertRefusal checks the shape every refusal shares: it is a *sexp.Refusal
// and it exits 2.
func assertRefusal(t testing.TB, err error) *sexp.Refusal {
	t.Helper()
	require.Error(t, err, "want a refusal, got nil")
	var ref *sexp.Refusal
	require.ErrorAs(t, err, &ref, "want *sexp.Refusal, got %T: %v", err, err)
	assert.Equal(t, 2, ref.ExitCode(), "exit = %d, want 2", ref.ExitCode())
	return ref
}

// assertBound checks one bound refusal: exit 2, the bound named, the file
// named, and the whole input refused rather than truncated.
func assertBound(t testing.TB, err error, bound string) {
	t.Helper()
	require.Error(t, err, "input past --%s was read instead of refused", bound)
	ref := assertRefusal(t, err)
	msg := ref.Error()
	assert.Contains(t, msg, bound, "refusal does not name --%s: %s", bound, msg)
	assert.Contains(t, msg, "work.work", "refusal does not name the file: %s", msg)
}

func TestTheReaderRefusesAnIntegerThatOverflowsInt64(t *testing.T) {
	t.Parallel()

	// 9223372036854775807 (math.MaxInt64) succeeds.
	f, err := sexp.Read("tree.lisp", []byte("9223372036854775807"), testLimits())
	require.NoError(t, err)
	assert.Equal(t, sexp.Integer, f.Kind)
	assert.Equal(t, int64(9223372036854775807), f.Int)

	// -9223372036854775808 (math.MinInt64) succeeds.
	fMin, err := sexp.Read("tree.lisp", []byte("-9223372036854775808"), testLimits())
	require.NoError(t, err)
	assert.Equal(t, sexp.Integer, fMin.Kind)
	assert.Equal(t, int64(-9223372036854775808), fMin.Int)

	// Small numbers unchanged.
	for _, small := range []struct {
		raw string
		val int64
	}{
		{"0", 0},
		{"42", 42},
		{"-42", -42},
		{"+42", 42},
		{"123456789", 123456789},
		{"-123456789", -123456789},
	} {
		fSmall, err := sexp.Read("tree.lisp", []byte(small.raw), testLimits())
		require.NoError(t, err, "reading %s failed: %v", small.raw, err)
		assert.Equal(t, sexp.Integer, fSmall.Kind)
		assert.Equal(t, small.val, fSmall.Int)
	}

	// 9223372036854775808, 18446744073709551623, and a 40-digit number are refused naming the byte.
	const fortyDigits = "1234567890123456789012345678901234567890"
	overflowCases := []string{
		"9223372036854775808",
		"18446744073709551623",
		fortyDigits,
		"-9223372036854775809",
		"-18446744073709551623",
		"-" + fortyDigits,
	}
	for _, raw := range overflowCases {
		t.Run("standalone/"+raw, func(t *testing.T) {
			t.Parallel()
			_, err := sexp.Read("tree.lisp", []byte(raw), testLimits())
			require.Error(t, err, "%s was read instead of refused", raw)
			ref := assertRefusal(t, err)
			assert.Contains(t, ref.Error(), "forbidden token at byte=0", "refusal for %s: %s", raw, ref.Error())
		})

		t.Run("in-list/"+raw, func(t *testing.T) {
			t.Parallel()
			src := "(num " + raw + ")"
			_, err := sexp.Read("tree.lisp", []byte(src), testLimits())
			require.Error(t, err, "%s in list was read instead of refused", raw)
			ref := assertRefusal(t, err)
			assert.Contains(t, ref.Error(), "forbidden token at byte=5", "refusal for %s: %s", src, ref.Error())
		})
	}
}
