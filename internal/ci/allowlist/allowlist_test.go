package allowlist

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// recorder is a Reporter that keeps what Check printed, so a test can hold the
// exact lines of an update run without failing itself.
type recorder struct{ lines []string }

func (r *recorder) Helper() {}
func (r *recorder) Errorf(format string, args ...any) {
	r.lines = append(r.lines, fmt.Sprintf(format, args...))
}

func (r *recorder) count(sub string) int {
	n := 0
	for _, l := range r.lines {
		if strings.Contains(l, sub) {
			n++
		}
	}
	return n
}

func writeList(t *testing.T, text string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "x_allowlist.txt")
	require.NoError(t, os.WriteFile(path, []byte(text), 0o644))
	return path
}

func load(t *testing.T, path string, opt Options) *List {
	t.Helper()
	l, err := Load(path, opt)
	require.NoError(t, err)
	return l
}

func readBack(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	return string(raw)
}

func set(keys ...string) map[string]bool {
	m := map[string]bool{}
	for _, k := range keys {
		m[k] = true
	}
	return m
}

const three = `# header one
# header two

a.go:f  # reason a
b.go:g  # reason b
# a note between rows
c.go:h  # reason c
`

// Outside an update Check only reports: the stale rows and the unlisted keys come
// back, nothing is printed for them, and the file is untouched.
func TestCheckReportsWithoutWriting(t *testing.T) {
	t.Parallel()

	path := writeList(t, three)
	l := load(t, path, Options{Ceiling: true})
	var r recorder
	res := CheckMode(&r, l, set("a.go:f", "c.go:h", "d.go:new"), false)
	require.Len(t, res.Stale, 1, "stale = %+v, want b.go:g at line 5", res.Stale)
	require.Equal(t, "b.go:g", res.Stale[0].Key, "stale = %+v, want b.go:g at line 5", res.Stale)
	require.Equal(t, 5, res.Stale[0].Line, "stale = %+v, want b.go:g at line 5", res.Stale)
	require.Equal(t, "d.go:new", strings.Join(res.Unlisted, ","), "unlisted = %v, want [d.go:new]", res.Unlisted)
	require.False(t, res.Updated, "a check outside an update wrote or printed: updated=%v lines=%q", res.Updated, r.lines)
	require.Empty(t, r.lines, "a check outside an update wrote or printed: updated=%v lines=%q", res.Updated, r.lines)
	require.Equal(t, three, readBack(t, path), "a check outside an update wrote or printed: updated=%v lines=%q", res.Updated, r.lines)
}

// Under an update the stale rows go, every comment and kept row stays byte for
// byte, and the run fails exactly once with "updated, rerun". The rerun is clean.
func TestUpdateDropsStaleRowsAndFailsOnce(t *testing.T) {
	t.Parallel()

	path := writeList(t, three)
	var r recorder
	res := CheckMode(&r, load(t, path, Options{Ceiling: true}), set("a.go:f", "c.go:h"), true)
	want := strings.Replace(three, "b.go:g  # reason b\n", "", 1)
	require.Equal(t, want, readBack(t, path), "rewritten list:\n%s\nwant:\n%s", readBack(t, path), want)
	require.True(t, res.Updated, "an update must fail once with %q and return no stale rows: updated=%v stale=%v lines=%q", UpdatedRerun, res.Updated, res.Stale, r.lines)
	require.Empty(t, res.Stale, "an update must fail once with %q and return no stale rows: updated=%v stale=%v lines=%q", UpdatedRerun, res.Updated, res.Stale, r.lines)
	require.Len(t, r.lines, 1, "an update must fail once with %q and return no stale rows: updated=%v stale=%v lines=%q", UpdatedRerun, res.Updated, res.Stale, r.lines)
	require.Equal(t, 1, r.count(UpdatedRerun), "an update must fail once with %q and return no stale rows: updated=%v stale=%v lines=%q", UpdatedRerun, res.Updated, res.Stale, r.lines)

	var again recorder
	res = CheckMode(&again, load(t, path, Options{Ceiling: true}), set("a.go:f", "c.go:h"), true)
	require.False(t, res.Updated, "the rerun must be clean: updated=%v lines=%q res=%+v", res.Updated, again.lines, res)
	require.Empty(t, again.lines, "the rerun must be clean: updated=%v lines=%q res=%+v", res.Updated, again.lines, res)
	require.Empty(t, res.Stale, "the rerun must be clean: updated=%v lines=%q res=%+v", res.Updated, again.lines, res)
	require.Empty(t, res.Unlisted, "the rerun must be clean: updated=%v lines=%q res=%+v", res.Updated, again.lines, res)
}

// A ceiling list refuses to grow under an update and says so for each key, one
// line per key; the unlisted keys stay in the result for the caller's remedy.
func TestCeilingListRefusesToGrowAndSaysSo(t *testing.T) {
	t.Parallel()

	path := writeList(t, three)
	var r recorder
	res := CheckMode(&r, load(t, path, Options{Ceiling: true}), set("a.go:f", "b.go:g", "c.go:h", "d.go:new", "e.go:new"), true)
	require.Equal(t, three, readBack(t, path), "a ceiling list grew under an update:\n%s", readBack(t, path))
	require.Equal(t, 2, r.count("refuses to grow"), "each refused key needs its own line: %q", r.lines)
	require.Equal(t, 1, r.count("d.go:new"), "each refused key needs its own line: %q", r.lines)
	require.Equal(t, 1, r.count("e.go:new"), "each refused key needs its own line: %q", r.lines)
	require.False(t, res.Updated, "updated=%v unlisted=%v; want no write and both keys kept", res.Updated, res.Unlisted)
	require.Equal(t, "d.go:new,e.go:new", strings.Join(res.Unlisted, ","), "updated=%v unlisted=%v; want no write and both keys kept", res.Updated, res.Unlisted)
}

// A list that may grow takes the unlisted keys as new rows under an update.
func TestGrowableListTakesTheMeasuredSet(t *testing.T) {
	t.Parallel()

	path := writeList(t, "# h\nold # gone\nkept # stays")
	opt := Options{NewRow: func(k string) string { return k + " # measured" }}
	var r recorder
	res := CheckMode(&r, load(t, path, opt), set("kept", "new"), true)
	require.Equal(t, "# h\nkept # stays\nnew # measured\n", readBack(t, path), "rewritten list %q, want %q", readBack(t, path), "# h\nkept # stays\nnew # measured\n")
	require.True(t, res.Updated, "updated=%v unlisted=%v lines=%q", res.Updated, res.Unlisted, r.lines)
	require.Empty(t, res.Unlisted, "updated=%v unlisted=%v lines=%q", res.Updated, res.Unlisted, r.lines)
	require.Equal(t, 1, r.count(UpdatedRerun), "updated=%v unlisted=%v lines=%q", res.Updated, res.Unlisted, r.lines)
}

// A `# ceiling: N` line caps the rows outside an update, and an update lowers it
// to the kept count; it never raises it.
func TestCeilingLineIsLoweredNeverRaised(t *testing.T) {
	t.Parallel()

	path := writeList(t, "# h\n# ceiling: 3\na\nb\nc\n")
	var r recorder
	CheckMode(&r, load(t, path, Options{Ceiling: true}), set("a", "c"), true)
	require.Equal(t, "# h\n# ceiling: 2\na\nc\n", readBack(t, path), "rewritten list %q, want %q", readBack(t, path), "# h\n# ceiling: 2\na\nc\n")
	n, ok := load(t, path, Options{}).Ceiling()
	require.True(t, ok, "ceiling = %d, %v; want 2", n, ok)
	require.Equal(t, 2, n, "ceiling = %d, %v; want 2", n, ok)

	over := writeList(t, "# ceiling: 1\na\nb\n")
	var o recorder
	CheckMode(&o, load(t, over, Options{Ceiling: true}), set("a", "b"), false)
	require.Equal(t, 1, o.count("over its ceiling of 1"), "a list over its ceiling must be refused: %q", o.lines)
	var u recorder
	CheckMode(&u, load(t, over, Options{Ceiling: true}), set("a", "b"), true)
	require.Equal(t, 1, u.count("never raises a ceiling"), "an update must not raise a ceiling: %q %q", u.lines, readBack(t, over))
	require.Equal(t, "# ceiling: 1\na\nb\n", readBack(t, over), "an update must not raise a ceiling: %q %q", u.lines, readBack(t, over))

	_, err := Parse("p", "# ceiling: many\n", Options{})
	require.Error(t, err, "a ceiling that is not a number must be refused")
	_, err = Parse("p", "# ceiling: 1\n# ceiling: 2\n", Options{})
	require.Error(t, err, "a second ceiling line must be refused")
}

// Keys: the first field by default, the first n fields, or the whole row; a
// missing file is an error unless the list says it may be missing.
func TestKeysAndMissingFiles(t *testing.T) {
	t.Parallel()

	l, err := Parse("p", "a.go:12 sleep 2026-09-17 why\ncmd x\tshape\t#1\n", Options{Key: Fields(2)})
	require.NoError(t, err)
	require.True(t, l.Has("a.go:12 sleep"), "Fields(2) keys = %+v", l.Rows())
	require.True(t, l.Has("cmd x"), "Fields(2) keys = %+v", l.Rows())
	require.Equal(t, 2, l.Len(), "Fields(2) keys = %+v", l.Rows())
	require.Equal(t, "x", FirstField("  x y "), "FirstField or WholeRow")
	require.Equal(t, "$ a b", WholeRow("$ a b"), "FirstField or WholeRow")

	missing := filepath.Join(t.TempDir(), "none.txt")
	_, err = Load(missing, Options{})
	require.Error(t, err, "a missing list must be an error by default")
	e, err := Load(missing, Options{MissingIsEmpty: true})
	require.NoError(t, err, "MissingIsEmpty: %v %d", err, e.Len())
	require.Equal(t, 0, e.Len(), "MissingIsEmpty: %v %d", err, e.Len())
	var r recorder
	res := CheckMode(&r, e, set(), true)
	require.False(t, res.Updated, "an empty measured set over a missing list writes nothing: %+v %q", res, r.lines)
	require.Empty(t, r.lines, "an empty measured set over a missing list writes nothing: %+v %q", res, r.lines)
	_, err = os.Stat(missing)
	require.True(t, os.IsNotExist(err), "the update created %s", missing)
}

// Only NOVA_CI_UPDATE=1 turns Check into a rewrite.
func TestUpdateNeedsTheValueOne(t *testing.T) {
	t.Parallel()

	for v, want := range map[string]bool{"1": true, "": false, "true": false, "0": false, "yes": false} {
		got := updatingFrom(func(k string) string {
			if k == UpdateEnv {
				return v
			}
			return ""
		})
		assert.Equal(t, want, got, "%s=%q updates = %v, want %v", UpdateEnv, v, got, want)
	}
}

// TestCountedListHoldsSitesNotJustKeys: a counted row says how many sites it covers.
// A new site under a listed key is Over (it used to read as already listed), a site
// fixed is Lowered, a key with no site left is Stale, and a count that is not a
// positive integer is a list that cannot be read.
func TestCountedListHoldsSitesNotJustKeys(t *testing.T) {
	t.Parallel()
	const text = "# ceiling: 3\na:f 2 two sites, a reason\nb:g 1 one site\nc:h 3 three\n"
	path := writeList(t, text)
	l := load(t, path, Options{Ceiling: true, Counted: true})
	require.Equal(t, 2, l.Count("a:f"), "counts = %d, %d", l.Count("a:f"), l.Count("zz"))
	require.Equal(t, 0, l.Count("zz"), "counts = %d, %d", l.Count("a:f"), l.Count("zz"))
	rec := &recorder{}
	res := CheckCountedMode(rec, l, map[string]int{"a:f": 3, "b:g": 1, "c:h": 1, "d:i": 1}, false)
	if assert.Len(t, res.Over, 1, "Over = %+v, want a:f 2 -> 3", res.Over) {
		assert.Equal(t, CountRow{"a:f", 2, 3}, res.Over[0], "Over = %+v, want a:f 2 -> 3", res.Over)
	}
	if assert.Len(t, res.Lowered, 1, "Lowered = %+v, want c:h 3 -> 1", res.Lowered) {
		assert.Equal(t, CountRow{"c:h", 3, 1}, res.Lowered[0], "Lowered = %+v, want c:h 3 -> 1", res.Lowered)
	}
	if assert.Len(t, res.Unlisted, 1, "Unlisted = %v", res.Unlisted) {
		assert.Equal(t, "d:i", res.Unlisted[0], "Unlisted = %v", res.Unlisted)
	}
	res = CheckCountedMode(rec, l, map[string]int{"a:f": 2, "b:g": 1}, false)
	if assert.Len(t, res.Stale, 1, "a key with no site left is stale: %+v", res) {
		assert.Equal(t, "c:h", res.Stale[0].Key, "a key with no site left is stale: %+v", res)
	}
	assert.Empty(t, res.Over, "a key with no site left is stale: %+v", res)
	assert.Empty(t, res.Lowered, "a key with no site left is stale: %+v", res)
	assert.Empty(t, res.Unlisted, "a key with no site left is stale: %+v", res)
	for _, bad := range []string{"a:f reason without a count\n", "a:f 0 zero\n", "a:f\n"} {
		_, err := Parse("x", bad, Options{Counted: true})
		assert.Error(t, err, "Parse(%q) accepted a row with no positive count", bad)
	}
}

// TestCountedUpdateLowersCountsAndNeverRaisesThem: under the update a count falls to
// the measured number with the row's reason kept byte for byte, a stale row goes, and a
// count that would have to rise is refused with a line, the file left as it was.
func TestCountedUpdateLowersCountsAndNeverRaisesThem(t *testing.T) {
	t.Parallel()
	path := writeList(t, "# ceiling: 3\na:f 4 why a\nb:g 2 why b\nc:h 1 why c\n")
	l := load(t, path, Options{Ceiling: true, Counted: true})
	rec := &recorder{}
	res := CheckCountedMode(rec, l, map[string]int{"a:f": 2, "b:g": 2}, true)
	require.True(t, res.Updated, "update did not report: %+v %v", res, rec.lines)
	require.Equal(t, 1, rec.count(UpdatedRerun), "update did not report: %+v %v", res, rec.lines)
	assert.Equal(t, "# ceiling: 2\na:f 2 why a\nb:g 2 why b\n", readBack(t, path), "after the update:\n%s\nwant:\n%s", readBack(t, path), "# ceiling: 2\na:f 2 why a\nb:g 2 why b\n")

	path = writeList(t, "# ceiling: 1\na:f 1 why a\n")
	l = load(t, path, Options{Ceiling: true, Counted: true})
	rec = &recorder{}
	CheckCountedMode(rec, l, map[string]int{"a:f": 2, "z:z": 1}, true)
	assert.Equal(t, 1, rec.count("refuses to raise a count"), "the update did not refuse both raises: %v", rec.lines)
	assert.Equal(t, 1, rec.count("refuses to grow"), "the update did not refuse both raises: %v", rec.lines)
	assert.Equal(t, "# ceiling: 1\na:f 1 why a\n", readBack(t, path), "a refused update changed the file: %q", readBack(t, path))
}

// TestParseRejectsDuplicateKeys verifies that Parse rejects repeated keys by default,
// and accepts them when RepeatedKeys is enabled.
func TestParseRejectsDuplicateKeys(t *testing.T) {
	t.Parallel()
	const duplicateText = `# list with duplicate
a.go:f # first
b.go:g # second
a.go:f # duplicate
`
	_, err := Parse("test.txt", duplicateText, Options{})
	require.Error(t, err)
	require.ErrorContains(t, err, `duplicate key "a.go:f"`)

	// RepeatedKeys permits repeated keys.
	l, err := Parse("test.txt", duplicateText, Options{RepeatedKeys: true})
	require.NoError(t, err)
	require.Equal(t, 3, l.Len())

	const duplicateCounted = `# ceiling: 2
a:f 1 reason
a:f 2 reason duplicate
`
	_, err = Parse("test.txt", duplicateCounted, Options{Counted: true})
	require.Error(t, err)
	require.ErrorContains(t, err, `duplicate key "a:f"`)
}
