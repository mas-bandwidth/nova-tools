package allowlist

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func load(t *testing.T, path string, opt Options) *List {
	t.Helper()
	l, err := Load(path, opt)
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func readBack(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
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
	if len(res.Stale) != 1 || res.Stale[0].Key != "b.go:g" || res.Stale[0].Line != 5 {
		t.Fatalf("stale = %+v, want b.go:g at line 5", res.Stale)
	}
	if strings.Join(res.Unlisted, ",") != "d.go:new" {
		t.Fatalf("unlisted = %v, want [d.go:new]", res.Unlisted)
	}
	if res.Updated || len(r.lines) != 0 || readBack(t, path) != three {
		t.Fatalf("a check outside an update wrote or printed: updated=%v lines=%q", res.Updated, r.lines)
	}
}

// Under an update the stale rows go, every comment and kept row stays byte for
// byte, and the run fails exactly once with "updated, rerun". The rerun is clean.
func TestUpdateDropsStaleRowsAndFailsOnce(t *testing.T) {
	t.Parallel()

	path := writeList(t, three)
	var r recorder
	res := CheckMode(&r, load(t, path, Options{Ceiling: true}), set("a.go:f", "c.go:h"), true)
	want := strings.Replace(three, "b.go:g  # reason b\n", "", 1)
	if got := readBack(t, path); got != want {
		t.Fatalf("rewritten list:\n%s\nwant:\n%s", got, want)
	}
	if !res.Updated || len(res.Stale) != 0 || len(r.lines) != 1 || r.count(UpdatedRerun) != 1 {
		t.Fatalf("an update must fail once with %q and return no stale rows: updated=%v stale=%v lines=%q", UpdatedRerun, res.Updated, res.Stale, r.lines)
	}

	var again recorder
	res = CheckMode(&again, load(t, path, Options{Ceiling: true}), set("a.go:f", "c.go:h"), true)
	if res.Updated || len(again.lines) != 0 || len(res.Stale)+len(res.Unlisted) != 0 {
		t.Fatalf("the rerun must be clean: updated=%v lines=%q res=%+v", res.Updated, again.lines, res)
	}
}

// A ceiling list refuses to grow under an update and says so for each key, one
// line per key; the unlisted keys stay in the result for the caller's remedy.
func TestCeilingListRefusesToGrowAndSaysSo(t *testing.T) {
	t.Parallel()

	path := writeList(t, three)
	var r recorder
	res := CheckMode(&r, load(t, path, Options{Ceiling: true}), set("a.go:f", "b.go:g", "c.go:h", "d.go:new", "e.go:new"), true)
	if got := readBack(t, path); got != three {
		t.Fatalf("a ceiling list grew under an update:\n%s", got)
	}
	if r.count("refuses to grow") != 2 || r.count("d.go:new") != 1 || r.count("e.go:new") != 1 {
		t.Fatalf("each refused key needs its own line: %q", r.lines)
	}
	if res.Updated || strings.Join(res.Unlisted, ",") != "d.go:new,e.go:new" {
		t.Fatalf("updated=%v unlisted=%v; want no write and both keys kept", res.Updated, res.Unlisted)
	}
}

// A list that may grow takes the unlisted keys as new rows under an update.
func TestGrowableListTakesTheMeasuredSet(t *testing.T) {
	t.Parallel()

	path := writeList(t, "# h\nold # gone\nkept # stays")
	opt := Options{NewRow: func(k string) string { return k + " # measured" }}
	var r recorder
	res := CheckMode(&r, load(t, path, opt), set("kept", "new"), true)
	if got, want := readBack(t, path), "# h\nkept # stays\nnew # measured\n"; got != want {
		t.Fatalf("rewritten list %q, want %q", got, want)
	}
	if !res.Updated || len(res.Unlisted) != 0 || r.count(UpdatedRerun) != 1 {
		t.Fatalf("updated=%v unlisted=%v lines=%q", res.Updated, res.Unlisted, r.lines)
	}
}

// A `# ceiling: N` line caps the rows outside an update, and an update lowers it
// to the kept count; it never raises it.
func TestCeilingLineIsLoweredNeverRaised(t *testing.T) {
	t.Parallel()

	path := writeList(t, "# h\n# ceiling: 3\na\nb\nc\n")
	var r recorder
	CheckMode(&r, load(t, path, Options{Ceiling: true}), set("a", "c"), true)
	if got, want := readBack(t, path), "# h\n# ceiling: 2\na\nc\n"; got != want {
		t.Fatalf("rewritten list %q, want %q", got, want)
	}
	if n, ok := load(t, path, Options{}).Ceiling(); !ok || n != 2 {
		t.Fatalf("ceiling = %d, %v; want 2", n, ok)
	}

	over := writeList(t, "# ceiling: 1\na\nb\n")
	var o recorder
	CheckMode(&o, load(t, over, Options{Ceiling: true}), set("a", "b"), false)
	if o.count("over its ceiling of 1") != 1 {
		t.Fatalf("a list over its ceiling must be refused: %q", o.lines)
	}
	var u recorder
	CheckMode(&u, load(t, over, Options{Ceiling: true}), set("a", "b"), true)
	if u.count("never raises a ceiling") != 1 || readBack(t, over) != "# ceiling: 1\na\nb\n" {
		t.Fatalf("an update must not raise a ceiling: %q %q", u.lines, readBack(t, over))
	}

	if _, err := Parse("p", "# ceiling: many\n", Options{}); err == nil {
		t.Fatal("a ceiling that is not a number must be refused")
	}
	if _, err := Parse("p", "# ceiling: 1\n# ceiling: 2\n", Options{}); err == nil {
		t.Fatal("a second ceiling line must be refused")
	}
}

// Keys: the first field by default, the first n fields, or the whole row; a
// missing file is an error unless the list says it may be missing.
func TestKeysAndMissingFiles(t *testing.T) {
	t.Parallel()

	l, err := Parse("p", "a.go:12 sleep 2026-09-17 why\ncmd x\tshape\t#1\n", Options{Key: Fields(2)})
	if err != nil {
		t.Fatal(err)
	}
	if !l.Has("a.go:12 sleep") || !l.Has("cmd x") || l.Len() != 2 {
		t.Fatalf("Fields(2) keys = %+v", l.Rows())
	}
	if FirstField("  x y ") != "x" || WholeRow("$ a b") != "$ a b" {
		t.Fatal("FirstField or WholeRow")
	}

	missing := filepath.Join(t.TempDir(), "none.txt")
	if _, err := Load(missing, Options{}); err == nil {
		t.Fatal("a missing list must be an error by default")
	}
	e, err := Load(missing, Options{MissingIsEmpty: true})
	if err != nil || e.Len() != 0 {
		t.Fatalf("MissingIsEmpty: %v %d", err, e.Len())
	}
	var r recorder
	if res := CheckMode(&r, e, set(), true); res.Updated || len(r.lines) != 0 {
		t.Fatalf("an empty measured set over a missing list writes nothing: %+v %q", res, r.lines)
	}
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Fatalf("the update created %s", missing)
	}
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
		if got != want {
			t.Errorf("%s=%q updates = %v, want %v", UpdateEnv, v, got, want)
		}
	}
}
