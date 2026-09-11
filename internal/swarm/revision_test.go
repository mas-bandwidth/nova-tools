package swarm

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// DEMANDED TEST 16 (SPEC-SWARM.md:1350). A REPORT IS A REVISION.
//
// The page holds a revision ENTIRE or not at all. A writer that ignored the protocol and
// appended in place while triage was reading is TRIAGE SKIPPED -- nothing recorded as
// consumed, the whole file taken by the next run -- and nothing about any of this is
// decided by a modification time, because two revisions renamed into place inside one
// second are two revisions.
func TestAReportIsARevision(t *testing.T) {
	dir := t.TempDir()
	p, id := revisionPool(t, dir)
	copyPath := filepath.Join(p.ReportsDir(id), CopiedResult)

	// A worker that APPENDS IN PLACE while triage is between its first hash and its parse.
	// The pause is the injected one demanded test 16 names; nothing outside this package's
	// tests sets it.
	var out, errb bytes.Buffer
	code := Triage(TriageInput{
		Pool: p, Stdout: &out, Stderr: &errb, Now: func() time.Time { return time.Now().UTC() },
		pauseAfterFirstHash: func() {
			f, err := os.OpenFile(copyPath, os.O_APPEND|os.O_WRONLY, 0o644)
			if err != nil {
				t.Error(err)
				return
			}
			defer f.Close()
			if _, err := f.WriteString("\n## Left owed\n- the line the worker was still writing under the reader's hands\n"); err != nil {
				t.Error(err)
			}
		},
	})
	if code != 0 {
		t.Fatalf("triage exited %d: %s", code, errb.String())
	}
	if want := "TRIAGE SKIPPED id=" + id + ": changed while read"; !strings.Contains(out.String(), want) {
		t.Errorf("a report that changed while it was read is %q:\n%s", want, out.String())
	}
	if strings.Contains(out.String(), "TRIAGE REPORT id="+id) {
		t.Errorf("a skipped report is not folded:\n%s", out.String())
	}
	// NOTHING IS RECORDED AS CONSUMED for it, which is what makes the next run whole.
	var state TriageState
	if err := ReadJSON(p.Path(TriageStateFile), &state); err != nil {
		t.Fatalf("triage wrote no state: %v", err)
	}
	if _, recorded := state.Consumed[id]; recorded {
		t.Errorf("a skipped report was recorded as consumed: %v", state.Consumed)
	}

	// The next run takes it WHOLE -- the appended line included.
	out.Reset()
	if code := Triage(TriageInput{Pool: p, Stdout: &out, Stderr: &errb, Now: time.Now}); code != 0 {
		t.Fatalf("the second triage exited %d: %s", code, errb.String())
	}
	firstRev := revOf(t, out.String(), id)
	page := readPage(t, out.String())
	if !strings.Contains(page, "still writing under the reader's hands") {
		t.Errorf("the run after a skip takes the whole file:\n%s", page)
	}

	// TWO REVISIONS RENAMED INSIDE ONE SECOND, with the filesystem's mtime resolution
	// FORCED EQUAL, are two revisions: two `rev=` hashes, both in pages. A tool that
	// decided freshness by mtime would see one.
	info, err := os.Stat(copyPath)
	if err != nil {
		t.Fatal(err)
	}
	second := strings.Replace(report(), "a first revision", "a second revision", 1)
	if err := writeAtomic(copyPath, []byte(second), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(copyPath, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	if err := p.writeRev(p.ReportsDir(id), 1, HashBytes([]byte(second))); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if code := Triage(TriageInput{Pool: p, Stdout: &out, Stderr: &errb, Now: time.Now}); code != 0 {
		t.Fatalf("the third triage exited %d: %s", code, errb.String())
	}
	secondRev := revOf(t, out.String(), id)
	if secondRev == firstRev {
		t.Errorf("two revisions with equal mtimes are two revisions, got one rev=%s twice", secondRev)
	}
	if page := readPage(t, out.String()); !strings.Contains(page, "a second revision") {
		t.Errorf("the second revision belongs in a page:\n%s", page)
	}
}

// Demanded test 16's last line: THE SOURCE TRIPWIRE. No modification time decides anything
// in this package -- not what is fresh, not what was consumed, not which revision a page
// holds. A revision is its bytes.
func TestNoModTimeDecidesAnythingInThisPackage(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		raw, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(raw, []byte("ModTime")) {
			t.Errorf("%s reads a modification time; a revision is its bytes and never its mtime", name)
		}
	}
}

// revisionPool is one finished job with one published revision retained beside the pool.
func revisionPool(t *testing.T, dir string) (*Pool, string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, "pool"), 0o755); err != nil {
		t.Fatal(err)
	}
	p, err := OpenPool(filepath.Join(dir, "pool"))
	if err != nil {
		t.Fatal(err)
	}
	sc := Sidecar{ID: NewID(time.Now().UTC(), "rev"), Files: 1, Tokens: 1000, RC: 0, Class: ClassOK, End: EndDone}
	if err := p.Add([]byte("a task"), sc); err != nil {
		t.Fatal(err)
	}
	if err := p.Claim(sc.ID, Pending, Done); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(p.ReportsDir(sc.ID), 0o755); err != nil {
		t.Fatal(err)
	}
	body := report()
	if err := writeAtomic(filepath.Join(p.ReportsDir(sc.ID), CopiedResult), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := p.writeRev(p.ReportsDir(sc.ID), 1, HashBytes([]byte(body))); err != nil {
		t.Fatal(err)
	}
	return p, sc.ID
}

func report() string {
	return "# a first revision\n\n## Head\nfindings: 1\nrepo: o/n\nrev: abc\na first revision of a report.\n\n" +
		"## Findings\n- something `THE RULE, VERBATIM` x.go:1\n\n" +
		"## Per item\n| item | state | evidence |\n| --- | --- | --- |\n| an item | green | x.go:1 |\n"
}

func revOf(t *testing.T, out, id string) string {
	t.Helper()
	for _, line := range strings.Split(out, "\n") {
		if !strings.Contains(line, "TRIAGE REPORT id="+id) {
			continue
		}
		for _, token := range strings.Fields(line) {
			if strings.HasPrefix(token, "rev=") {
				return strings.TrimPrefix(token, "rev=")
			}
		}
	}
	t.Fatalf("no TRIAGE REPORT for %s in:\n%s", id, out)
	return ""
}

func readPage(t *testing.T, out string) string {
	t.Helper()
	for _, line := range strings.Split(out, "\n") {
		for _, token := range strings.Fields(line) {
			if strings.HasPrefix(token, "page=") {
				raw, err := os.ReadFile(strings.TrimPrefix(token, "page="))
				if err != nil {
					t.Fatal(err)
				}
				return string(raw)
			}
		}
	}
	t.Fatalf("no page= in:\n%s", out)
	return ""
}

// RULE 15 ON THE PAGE (SPEC-SWARM.md:297): "the merged finding carries every contributing
// job id, `jobs=<id,id,…>`, ON THE PAGE and on its triage line."
//
// DeepSeek's confirming read, finding 3: the terminal line carried `jobs=` and the page --
// the artifact that is kept, the one a coordinator reads tomorrow -- carried each report's
// raw finding lines, undeduplicated and with no job ids at all. Two workers finding one
// thing were two findings on the page and one in the count.
func TestThePageCarriesTheMergedFindingAndItsJobs(t *testing.T) {
	dir := t.TempDir()
	p := emptyPool(t, dir)
	first := donePublished(t, p, "a")
	second := donePublished(t, p, "b")

	var out, errb bytes.Buffer
	if code := Triage(TriageInput{Pool: p, Stdout: &out, Stderr: &errb, Now: time.Now}); code != 0 {
		t.Fatalf("triage exited %d: %s", code, errb.String())
	}
	page := readPage(t, out.String())
	jobs := first + "," + second
	if !strings.Contains(page, "jobs="+jobs) && !strings.Contains(page, "jobs="+second+","+first) {
		t.Errorf("the page wants the merged finding's contributing job ids (jobs=%s):\n%s", jobs, page)
	}
	if n := strings.Count(page, "THE RULE, VERBATIM"); n != 1 {
		t.Errorf("one finding reported by two jobs is ONE line on the page, got %d:\n%s", n, page)
	}
	// And the terminal line says the same thing, which is what makes the page an index.
	if !strings.Contains(out.String(), "TRIAGE FINDING jobs="+jobs) && !strings.Contains(out.String(), "TRIAGE FINDING jobs="+second+","+first) {
		t.Errorf("the triage line wants both job ids:\n%s", out.String())
	}
}

func emptyPool(t *testing.T, dir string) *Pool {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, "pool"), 0o755); err != nil {
		t.Fatal(err)
	}
	p, err := OpenPool(filepath.Join(dir, "pool"))
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// donePublished is one finished job with one published report, retained beside the pool.
func donePublished(t *testing.T, p *Pool, label string) string {
	t.Helper()
	sc := Sidecar{ID: NewID(time.Now().UTC(), label), Files: 1, Tokens: 1000, RC: 0, Class: ClassOK, End: EndDone}
	if err := p.Add([]byte("a task"), sc); err != nil {
		t.Fatal(err)
	}
	if err := p.Claim(sc.ID, Pending, Done); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(p.ReportsDir(sc.ID), 0o755); err != nil {
		t.Fatal(err)
	}
	body := report()
	if err := writeAtomic(filepath.Join(p.ReportsDir(sc.ID), CopiedResult), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := p.writeRev(p.ReportsDir(sc.ID), 1, HashBytes([]byte(body))); err != nil {
		t.Fatal(err)
	}
	// A job id is time-ordered to the second; two made in one second would collide.
	time.Sleep(1100 * time.Millisecond)
	return sc.ID
}
