package main

import (
	"compress/gzip"
	"flag"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

var update = flag.Bool("update", false, "rewrite testdata/fixture/ROADMAP.golden.md from what the code renders")

func loadFixture(t *testing.T) *Roadmap {
	t.Helper()
	rm, files, entries, err := Load("testdata/fixture/docs/roadmap.sexp", "")
	require.NoError(t, err)
	require.Equal(t, [2]int{1, 1}, [2]int{files, entries}, "a dot file is not applied")
	return rm
}

// Schema's data renders, byte for byte, the page schema's own renderer makes.
// The data and the overlay under testdata/schema/docs are copied from
// github.com/mas-bandwidth/schema main at 47c2cec0 (2026-11-07).
// testdata/schema/ROADMAP.md is schema's committed page with its tables
// re-spliced by that commit's tools/roadmap/render.lisp (sbcl --script
// tools/roadmap/render.lisp docs/roadmap.sexp ROADMAP.md): the committed page
// itself was behind its data at that commit (render.lisp --check reported
// DRIFT), so render.lisp's output is the reference. Every table is emptied
// first, so each one is re-rendered here, and the hand-written text around
// them is kept.
// The data is kept gzipped (2.2 MB plain, past the repository's 1 MiB file
// bound) and is read through a plain copy in the test's own directory.
func TestSchemaGolden(t *testing.T) {
	t.Parallel()
	data := filepath.Join(t.TempDir(), "roadmap.sexp")
	gunzipTo(t, "testdata/schema/docs/roadmap.sexp.gz", data)
	rm, files, entries, err := Load(data, "testdata/schema/docs/roadmap-evidence")
	require.NoError(t, err)
	require.Positive(t, files)
	require.Positive(t, entries)
	raw, err := os.ReadFile("testdata/schema/ROADMAP.md")
	require.NoError(t, err)
	want := string(raw)
	emptied := HandWritten(want)
	require.NotContains(t, emptied, "| complete", "the tables were emptied")
	got, err := rm.Splice(emptied, "docs/roadmap.sexp")
	require.NoError(t, err)
	require.Equal(t, want, got)
	again, err := rm.Splice(want, "docs/roadmap.sexp")
	require.NoError(t, err)
	require.Equal(t, want, again, "a page that is up to date is left as it is")
}

// A first page: a heading per area and the tables, byte for byte. Run
// go test ./cmd/nova-roadmap -run TestStarterGolden -update to rewrite the golden.
func TestStarterGolden(t *testing.T) {
	t.Parallel()
	page, err := loadFixture(t).Starter("docs/roadmap.sexp")
	require.NoError(t, err)
	if *update {
		require.NoError(t, os.WriteFile("testdata/fixture/ROADMAP.golden.md", []byte(page), 0o644))
	}
	want, err := os.ReadFile("testdata/fixture/ROADMAP.golden.md")
	require.NoError(t, err)
	require.Equal(t, string(want), page)
	again, err := loadFixture(t).Splice(page, "docs/roadmap.sexp")
	require.NoError(t, err)
	require.Equal(t, page, again)
}

// The cells follow the one rule: ✅ only when every task under the cell is done,
// the overlay moves a task, a later row says so, and the complete row is the
// floor of the ✅ share per column.
func TestCellsAndComplete(t *testing.T) {
	t.Parallel()
	rm := loadFixture(t)
	page, err := rm.Starter("docs/roadmap.sexp")
	require.NoError(t, err)
	for _, want := range []string{
		"## Packet wire\n",
		"| feature | go | cs |\n|---|:---:|:---:|\n",
		"| integers | ✅ | ✅ |\n",
		"| floats | ✅ | ❌ |\n", // go/f64 moved to done by the overlay; a work-set cell
		"| tagged unions (later) | ❌ | ❌ |\n",
		"| complete | 66% | 33% |\n",
		"| feature | done |\n|---|:---:|\n| render a page | ✅ |\n| check for drift | ❌ |\n| complete | 50% |\n",
		"[Source data](docs/roadmap.sexp)",
	} {
		require.Contains(t, page, want)
	}
	require.NotContains(t, page, "cs-floats", "extra keys are kept, not rendered")
	require.Equal(t, 5, rm.Rows())
}

// Only the bytes between the markers change; the page's own text is kept, and a
// page that does not hold exactly one block per area, in order, is refused.
func TestSpliceKeepsTheHandWrittenPage(t *testing.T) {
	t.Parallel()
	rm := loadFixture(t)
	page := "# My roadmap\n\nIntro, by hand.\n\n## Wire\n\nWhat the wire is.\n\n" +
		StartMarker("wire") + "\nstale table\n" + EndMarker("wire") + "\n\nBetween.\n\n" +
		StartMarker("tools") + "\n" + EndMarker("tools") + "\n\n## Fund this work\n\nThanks.\n"
	got, err := rm.Splice(page, "docs/roadmap.sexp")
	require.NoError(t, err)
	require.Equal(t, HandWritten(page), HandWritten(got))
	require.NotContains(t, got, "stale table")
	require.True(t, strings.HasPrefix(got, "# My roadmap\n\nIntro, by hand.\n"))
	require.True(t, strings.HasSuffix(got, "## Fund this work\n\nThanks.\n"))
	require.Contains(t, got, StartMarker("wire")+"\n\n| feature | go | cs |\n")
	require.Contains(t, got, "[Source data](docs/roadmap.sexp)\n\n"+EndMarker("tools")+"\n")

	w, tl := StartMarker("wire")+"\n"+EndMarker("wire")+"\n", StartMarker("tools")+"\n"+EndMarker("tools")+"\n"
	for name, c := range map[string]struct{ page, want string }{
		"no markers":      {"# Roadmap\n", "the page carries marker pairs for []"},
		"one missing":     {w, `the page carries marker pairs for ["wire"]`},
		"wrong order":     {tl + w, `the page carries marker pairs for ["tools" "wire"]`},
		"end missing":     {StartMarker("wire") + "\n" + tl, `"<!-- nova-work:wire:end -->" is missing`},
		"start twice":     {w + StartMarker("wire") + "\n" + tl, `the page carries marker pairs for ["wire" "wire" "tools"]`},
		"end twice":       {w + EndMarker("wire") + "\n" + tl, "appears more than once"},
		"shares its line": {"x " + EndMarker("wire") + "\n" + StartMarker("wire") + "\n" + EndMarker("wire") + "\n" + tl, "shares its line"},
		"opens late":      {EndMarker("wire") + "\n" + StartMarker("wire") + "\n" + tl, "opens after it closes"},
		"nested marker":   {StartMarker("wire") + "\n<!-- nova-work:x:end -->\n" + EndMarker("wire") + "\n" + tl, "holds another marker"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, err := rm.Splice(c.page, "docs/roadmap.sexp")
			require.ErrorContains(t, err, c.want)
		})
	}
}

const minimal = `(:schema 1 :root "goal"
 :page (:roadmaps ("a"))
 :nodes
 ((:id "goal" :type :work-set :children ("a"))
  (:id "a" :type :roadmap :title "A"
   :rows (("r1" "Row 1") ("r2" "Row 2")) :columns (("done" "done"))
   :cells (("r1" "done" "t1") ("r2" "done" "t2")))
  (:id "t1" :type :task :title "T1" :state :done :evidence ("a_test.go: TestOne"))
  (:id "t2" :type :task :title "T2" :state :todo)))`

// Every shape problem is refused, named: the refusals schema's render.lisp
// holds, and the ones this tool adds (a | in a title, an unknown row key).
func TestRefuseEveryShapeProblem(t *testing.T) {
	t.Parallel()
	_, err := Decode("ok.sexp", []byte(minimal))
	require.NoError(t, err, "the base case decodes")
	cases := map[string]struct{ from, to, want string }{
		"wrong version":      {`(:schema 1`, `(:schema 2`, "want :schema 1"},
		"no root":            {`:root "goal"`, `:rootx "goal"`, "want a non-empty string :root"},
		"root names none":    {`:root "goal"`, `:root "nope"`, `:root "nope" names no node`},
		"duplicate key":      {`:title "T1"`, `:title "T1" :title "T1b"`, "key :title stands twice"},
		"duplicate id":       {`(:id "t2"`, `(:id "t1"`, "the id stands twice"},
		"unknown type":       {`(:id "t2" :type :task`, `(:id "t2" :type :wish`, ":type wants :task, :work-set or :roadmap"},
		"done no evidence":   {`:state :done :evidence ("a_test.go: TestOne")`, `:state :done`, `done task "t1" has no :evidence`},
		"blank evidence":     {`("a_test.go: TestOne")`, `("   ")`, `done task "t1" has no :evidence`},
		"bad state":          {`:state :todo`, `:state :finished`, "has state :finished"},
		"dangling child":     {`:children ("a")`, `:children ("a" "ghost")`, `child "ghost" names no node`},
		"empty work-set":     {`:children ("a")`, `:children ()`, "a work-set wants a non-empty :children"},
		"missing cell":       {`("r2" "done" "t2")`, ``, `no cell for row "r2" and column "done"`},
		"duplicate cell":     {`("r2" "done" "t2")`, `("r2" "done" "t2") ("r2" "done" "t1")`, `cell ("r2", "done") stands twice`},
		"unknown column":     {`("r2" "done" "t2")`, `("r2" "nope" "t2")`, "names an unknown row or column"},
		"cell to nothing":    {`("r2" "done" "t2")`, `("r2" "done" "ghost")`, `names no node "ghost"`},
		"pipe in title":      {`"Row 2"`, `"Row | 2"`, "would break the table"},
		"unknown row key":    {`("r2" "Row 2")`, `("r2" "Row 2" :soon t)`, "unknown key :soon"},
		"cycle":              {`(:id "t2" :type :task :title "T2" :state :todo)`, `(:id "t2" :type :work-set :children ("w2")) (:id "w2" :type :work-set :children ("t2"))`, "cycle:"},
		"unreachable area":   {`:children ("a")`, `:children ("t1")`, `no area reachable from :root`},
		"area twice":         {`:roadmaps ("a")`, `:roadmaps ("a" "a")`, `names "a" twice`},
		"no page":            {`:page (:roadmaps ("a"))`, ``, "want :page"},
		"prose on the page":  {`:roadmaps ("a")`, `:roadmaps ("a") :title "P"`, ":page: carries :title; it holds :roadmaps only"},
		"prose in an area":   {`:title "A"`, `:title "A" :text "What A is."`, `area "a" carries :text`},
		"bad implementation": {`:title "T2"`, `:title "T2" :implementation :implemented`, ":implementation wants"},
		"evaluation form":    {`:title "T2"`, `:title #.(x)`, "dispatch macro"},
		"quote":              {`:title "T2"`, `:title 'x`, "quote at byte"},
		"trailing form":      {`(:id "t2" :type :task :title "T2" :state :todo)))`, `(:id "t2" :type :task :title "T2" :state :todo))) (:extra 1)`, "trailing bytes"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			src := strings.Replace(minimal, c.from, c.to, 1)
			require.NotEqual(t, minimal, src, "the case must change the file")
			rm, err := Decode("bad.sexp", []byte(src))
			require.Nil(t, rm)
			require.ErrorContains(t, err, c.want)
			require.ErrorContains(t, err, "bad.sexp")
		})
	}
}

// The overlay moves state and evidence, keeps a task's evidence when an entry
// names none, and refuses a whole file for any bad entry, changing nothing.
func TestEvidenceOverlay(t *testing.T) {
	t.Parallel()
	rm, err := Decode("ok.sexp", []byte(minimal))
	require.NoError(t, err)
	n, err := rm.ApplyEvidence("ev.sexp", []byte("; proof\n(:id \"t2\" :state :done :evidence (\"b_test.go: TestTwo\"))\n(:id \"t1\" :state :unknown :note \"re-check\") ; trailing comment"))
	require.NoError(t, err)
	require.Equal(t, 2, n)
	require.Equal(t, []string{"a_test.go: TestOne"}, rm.Nodes["t1"].Evidence, "no :evidence keeps the task's")
	require.Contains(t, rm.Table("a", "s"), "| Row 1 | ❌ |\n| Row 2 | ✅ |\n")
	for _, bad := range []string{
		`(:id "nope" :state :done :evidence ("x"))`,
		`(:id "a" :state :done :evidence ("x"))`,
		`(:id "t1" :state :done :evidence (" "))`,
		`(:id "t1" :state :finished)`,
		`(:id "t1")`,
		`(:id "t1" :state :done :evidence #.(list 1))`,
		`(:id "t2" :state :done :evidence ("x")) (:id "nope" :state :done :evidence ("x"))`,
	} {
		rm, err := Decode("ok.sexp", []byte(minimal))
		require.NoError(t, err)
		_, err = rm.ApplyEvidence("ev.sexp", []byte(bad))
		require.Error(t, err, bad)
		require.Equal(t, "done", rm.Nodes["t1"].State, "a refused file changes nothing: %s", bad)
		require.Equal(t, "todo", rm.Nodes["t2"].State, "a refused file changes nothing, not even its good entries: %s", bad)
	}
}

// A named overlay directory that is not there is refused; the default one may be absent.
func TestLoadEvidenceDirectory(t *testing.T) {
	t.Parallel()
	_, _, _, err := Load("testdata/fixture/docs/roadmap.sexp", "testdata/no-such-dir")
	require.ErrorContains(t, err, "--evidence testdata/no-such-dir is not a directory")
	_, _, _, err = Load("testdata/no-such.sexp", "")
	require.ErrorContains(t, err, "reading testdata/no-such.sexp")
}

// gunzipTo writes the gzip file src, uncompressed, to dst.
func gunzipTo(t *testing.T, src, dst string) {
	t.Helper()
	in, err := os.Open(src)
	require.NoError(t, err)
	defer in.Close()
	zr, err := gzip.NewReader(in)
	require.NoError(t, err)
	out, err := os.Create(dst)
	require.NoError(t, err)
	_, err = io.Copy(out, zr)
	require.NoError(t, err)
	require.NoError(t, out.Close())
	require.NoError(t, zr.Close())
}
