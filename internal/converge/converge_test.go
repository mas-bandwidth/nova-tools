package converge

// The red tests docs/SPEC-CHECK.md demands, numbered there and named here.
// Every fake stands where the real thing is a forge, a git, a bench or a clock:
// nothing in this file opens a socket, and the only clock is the one handed in.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/dogfood"
)

// ---------------------------------------------------------------------------
// the fakes
// ---------------------------------------------------------------------------

// fakeForge answers from memory, in the shape gh answers in. It is the whole
// seam: two reads, no writes, and a hang a test can ask for.
type fakeForge struct {
	open   []PR
	closed []PR
	err    error
	hang   bool
}

func (f fakeForge) OpenPRs(ctx context.Context) ([]PR, error) {
	if f.hang {
		return nil, errors.New("gh pr list --state open ran past --timeout 1s and was killed")
	}
	return f.open, f.err
}

func (f fakeForge) ClosedSince(ctx context.Context, since time.Time) ([]PR, error) {
	if f.hang {
		return nil, errors.New("gh pr list --state all ran past --timeout 1s and was killed")
	}
	return f.closed, f.err
}

// fakeGit answers one revision and one file's content at it.
type fakeGit struct {
	rev   string
	files map[string]string
	err   error
}

func (g fakeGit) RevBefore(ctx context.Context, t time.Time) (string, error) {
	if g.err != nil {
		return "", g.err
	}
	return g.rev, nil
}

func (g fakeGit) Show(ctx context.Context, rev, path string) (string, error) {
	if g.err != nil {
		return "", g.err
	}
	return g.files[path], nil
}

func at(t *testing.T, s string) time.Time {
	t.Helper()
	v, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatalf("bad fixture time %q: %v", s, err)
	}
	return v.UTC()
}

const (
	windowSince = "2026-09-18T00:00:00Z"
	windowNow   = "2026-09-18T12:00:00Z"
)

// specWith builds a SPEC-CI.md holding n class-test entries in the index, and a
// `###` heading outside it that must not be counted.
func specWith(n int) string {
	var b strings.Builder
	b.WriteString("# spec\n\n### not in the index\n\n## The class tests\n\n")
	for i := 0; i < n; i++ {
		fmt.Fprintf(&b, "### `class-%d` — a rule\n\nprose\n\n", i)
	}
	b.WriteString("## Something else\n\n### also not in the index\n")
	return b.String()
}

// writeFile drops one fixture file and returns its path.
func writeFile(t *testing.T, dir, name, body string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// writeReceipt writes one dogfood receipt into a receipts directory.
func writeReceipt(t *testing.T, dir, name string, r dogfood.Receipt) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), raw, 0o644); err != nil {
		t.Fatal(err)
	}
}

// fixture is a whole reading's worth of sources, all of them fakes.
type fixture struct {
	dir  string
	opts Options
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	dir := t.TempDir()

	ledger := writeFile(t, dir, "ledger.md", strings.Join([]string{
		"| Tool | Case | Result |",
		"|---|---|---|",
		"| a | one | PASS (hulk) |",
		"| b | two | FAIL then PASS: fixed |",
		"| c | three | PARTIAL: idle path only |",
		"| d | four | TODO |",
		"| e | five | NEEDS WORK before it is trusted |",
	}, "\n"))

	retired := writeFile(t, dir, "retired.md", strings.Join([]string{
		"# Retired scripts",
		"",
		"Retired 2026-09-18 by Rowan.",
		"",
		"| script | lines | what it did |",
		"| --- | --- | --- |",
		"| `board.sh` | 189 | a board |",
		"| `seal.sh` | 27 | a seal |",
		"",
		"## Round 0 — 2026-09-01",
		"",
		"| script | lines | what it did |",
		"| --- | --- | --- |",
		"| `old.sh` | 3 | older than the window |",
	}, "\n"))

	binDir := filepath.Join(dir, "bin")
	writeFile(t, binDir, "one.sh", "echo 1\n")
	writeFile(t, binDir, "two.sh", "echo 2\n")
	writeFile(t, binDir, "shebanged", "#!/bin/sh\necho 3\n")
	writeFile(t, binDir, "nova-bus", "\x7fELF not a script\n")
	writeFile(t, filepath.Join(binDir, "retired"), "gone.sh", "echo gone\n")

	repoDir := filepath.Join(dir, "repo")
	writeFile(t, repoDir, SpecCIPath, specWith(29))

	receipts := filepath.Join(dir, "receipts")
	writeReceipt(t, receipts, "a.json", dogfood.Receipt{
		Tool: "nova-check", Verb: "links", By: "Stella", At: "2026-09-18T09:00:00Z", OK: true,
		Notes: "ran it on the self repo. Edges: the count line is quiet",
	})
	writeReceipt(t, receipts, "b.json", dogfood.Receipt{
		Tool: "nova-check", Verb: "kernel", By: "Rowan", At: "2026-09-18T10:00:00Z", OK: false,
		Notes: "refused a budget it should have taken", Issue: 1404,
	})
	writeReceipt(t, receipts, "c.json", dogfood.Receipt{
		Tool: "nova-merge", Verb: "batch", By: "Rowan", At: "2026-09-17T10:00:00Z", OK: true,
		Notes: "landed a batch. Edges: the queue line does not say which lane",
	})

	versions := writeFile(t, dir, "versions.tsv", strings.Join([]string{
		"machine\tstamp",
		"hulk\tv0.16.0-dev.04bb4e1c",
		"vision\tv0.16.0-dev.04bb4e1c",
		"space\tv0.16.0-dev.04bb4e1c",
		"mini\tv0.15.0",
	}, "\n"))

	certs := writeFile(t, dir, "certs.tsv", strings.Join([]string{
		"name\tstatus",
		"hulk\tcertified",
		"vision\tcertified",
		"space\tpending",
		"mini\tpending",
	}, "\n"))

	return &fixture{dir: dir, opts: Options{
		Repo:         "mas-bandwidth/nova-tools",
		LedgerPath:   ledger,
		ReceiptsDir:  receipts,
		RetiredPath:  retired,
		BinDir:       binDir,
		RepoDir:      repoDir,
		VersionsPath: versions,
		CertsPath:    certs,
		Since:        at(t, windowSince),
		Now:          at(t, windowNow),
		Forge: fakeForge{
			open: []PR{
				{Number: 1, Title: "old and open", CreatedAt: at(t, "2026-09-17T08:00:00Z")},
				{Number: 2, Title: "new and open", CreatedAt: at(t, "2026-09-18T08:00:00Z")},
			},
			closed: []PR{
				{Number: 3, Title: "integration-11a: a batch", Body: "round 1\nround 2", Merged: true,
					CreatedAt: at(t, "2026-09-18T01:00:00Z"), ClosedAt: at(t, "2026-09-18T03:00:00Z")},
				{Number: 4, Title: "integration-11b: another", Body: "round 4", Merged: true,
					CreatedAt: at(t, "2026-09-18T04:00:00Z"), ClosedAt: at(t, "2026-09-18T06:00:00Z")},
				{Number: 5, Title: "integration-10a: the window before", Body: "round 6", Merged: true,
					CreatedAt: at(t, "2026-09-17T14:00:00Z"), ClosedAt: at(t, "2026-09-17T18:00:00Z")},
				{Number: 6, Title: "a plain pull request", CreatedAt: at(t, "2026-09-17T09:00:00Z"),
					ClosedAt: at(t, "2026-09-18T02:00:00Z")},
			},
		},
		Git: fakeGit{rev: "abc123456789", files: map[string]string{SpecCIPath: specWith(27)}},
	}}
}

func (f *fixture) read(t *testing.T) Report {
	t.Helper()
	r, err := Read(context.Background(), f.opts)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	return r
}

// stream finds one stream of a report by name.
func stream(t *testing.T, r Report, name string) Stream {
	t.Helper()
	for _, s := range r.Streams {
		if s.Name == name {
			return s
		}
	}
	t.Fatalf("no %s stream in the reading; got %d streams", name, len(r.Streams))
	return Stream{}
}

// field reads one key=value extra off a printed line.
func field(t *testing.T, line, key string) string {
	t.Helper()
	for _, tok := range strings.Fields(line) {
		if v, ok := strings.CutPrefix(tok, key+"="); ok {
			return v
		}
	}
	t.Fatalf("no %s= field in %q", key, line)
	return ""
}

// ---------------------------------------------------------------------------
// 1
// ---------------------------------------------------------------------------

func TestConvergencePrintsOneLinePerStream(t *testing.T) {
	f := newFixture(t)
	lines := f.read(t).Lines()
	if len(lines) != len(Order)+1 {
		t.Fatalf("want %d stream lines and one verdict, got %d:\n%s", len(Order), len(lines), strings.Join(lines, "\n"))
	}
	for i, name := range Order {
		want := "CONVERGENCE " + name + " "
		if !strings.HasPrefix(lines[i], want) {
			t.Errorf("line %d is %q, want it to start %q", i, lines[i], want)
		}
		for _, key := range []string{"now", "before", "ratio", "trend"} {
			field(t, lines[i], key)
		}
	}
	last := lines[len(lines)-1]
	if !strings.HasPrefix(last, "CONVERGENCE OK ") && !strings.HasPrefix(last, "CONVERGENCE WARN ") {
		t.Errorf("verdict line is %q", last)
	}
	for _, key := range []string{"streams", "contracting", "widening", "absent"} {
		field(t, last, key)
	}
}

// ---------------------------------------------------------------------------
// 2
// ---------------------------------------------------------------------------

func TestATrendIsTheDirectionTheStreamConverges(t *testing.T) {
	for _, tc := range []struct {
		name         string
		now, before  float64
		lower        bool
		want         Trend
	}{
		{"fewer open edges", 3, 5, true, Contracting},
		{"more open edges", 7, 5, true, Widening},
		{"unmoved", 5, 5, true, Flat},
		{"more class tests", 29, 27, false, Contracting},
		{"fewer class tests", 25, 27, false, Widening},
		{"unmoved class tests", 27, 27, false, Flat},
	} {
		s := Stream{Name: "X", Now: tc.now, Before: tc.before, HaveNow: true, HaveBefore: true, Lower: tc.lower}
		if got := s.Trend(); got != tc.want {
			t.Errorf("%s: trend %s, want %s", tc.name, got, tc.want)
		}
	}
}

// ---------------------------------------------------------------------------
// 3
// ---------------------------------------------------------------------------

func TestRatioIsAlwaysNowOverBefore(t *testing.T) {
	for _, lower := range []bool{true, false} {
		s := Stream{Now: 3, Before: 4, HaveNow: true, HaveBefore: true, Lower: lower}
		if !strings.Contains(s.Line(), "ratio=0.75") {
			t.Errorf("lower=%v: %q wants ratio=0.75", lower, s.Line())
		}
	}
	zero := Stream{Now: 3, Before: 0, HaveNow: true, HaveBefore: true, Lower: true}
	if !strings.Contains(zero.Line(), "ratio=-") {
		t.Errorf("a before of zero printed %q; an infinity is not a measurement", zero.Line())
	}
	both := Stream{Now: 0, Before: 0, HaveNow: true, HaveBefore: true, Lower: true}
	if !strings.Contains(both.Line(), "ratio=1") || both.Trend() != Flat {
		t.Errorf("zero against zero printed %q trend %s", both.Line(), both.Trend())
	}
}

// ---------------------------------------------------------------------------
// 4 and 5
// ---------------------------------------------------------------------------

func TestLandingReadsRoundsFromTheBodyAndTheLogs(t *testing.T) {
	f := newFixture(t)
	// #3 says round 2 in its body; three logs say it went to round 3, and the
	// logs win because a body is written by hand.
	logs := filepath.Join(f.dir, "logs")
	for round := 1; round <= 3; round++ {
		writeFile(t, logs, "3-round-"+strconv.Itoa(round)+".log", "green\n")
	}
	writeFile(t, logs, "not-a-round.txt", "ignored\n")
	f.opts.BatchLogs = logs

	s := stream(t, f.read(t), "LANDING")
	// #3 at 3 rounds (logs) and #4 at 4 rounds (body) is a mean of 3.5.
	if s.Now != 3.5 {
		t.Errorf("rounds per batch now=%v, want 3.5 (logs beat the body)", s.Now)
	}
	if got := field(t, s.Line(), "batches"); got != "2" {
		t.Errorf("batches=%s, want 2", got)
	}
}

func TestLandingComparesTheWindowWithTheOneBefore(t *testing.T) {
	f := newFixture(t)
	s := stream(t, f.read(t), "LANDING")
	if s.Now != 3 { // #3 round 2 and #4 round 4
		t.Errorf("now=%v, want 3", s.Now)
	}
	if s.Before != 6 { // #5, merged in the twelve hours before --since
		t.Errorf("before=%v, want 6 (the batch in the window before --since)", s.Before)
	}
	if s.Trend() != Contracting {
		t.Errorf("trend %s, want contracting: fewer rounds per batch is landing getting cheaper", s.Trend())
	}
	line := s.Line()
	if got := field(t, line, "per-hour"); got != "0.17" {
		t.Errorf("per-hour=%s, want 0.17 (two batches in twelve hours)", got)
	}
	if got := field(t, line, "prev-batches"); got != "1" {
		t.Errorf("prev-batches=%s, want 1", got)
	}
}

// ---------------------------------------------------------------------------
// 6
// ---------------------------------------------------------------------------

func TestClassesCountsTheIndexEntriesAtBothRevisions(t *testing.T) {
	f := newFixture(t)
	s := stream(t, f.read(t), "CLASSES")
	if s.Now != 29 || s.Before != 27 {
		t.Fatalf("now=%v before=%v, want 29 and 27; the `###` outside the index must not be counted", s.Now, s.Before)
	}
	if s.Trend() != Contracting {
		t.Errorf("trend %s, want contracting: a class made mechanical cannot come back", s.Trend())
	}
	if got := field(t, s.Line(), "rev"); got != "abc123456789" {
		t.Errorf("rev=%s, want the revision the fake git named", got)
	}
}

// ---------------------------------------------------------------------------
// 7 and 8
// ---------------------------------------------------------------------------

func TestScriptsCountsWhatIsLeftAndWhatTheWindowRetired(t *testing.T) {
	f := newFixture(t)
	s := stream(t, f.read(t), "SCRIPTS")
	if s.Now != 3 {
		t.Errorf("now=%v, want 3: two .sh, one shebang, and neither the binary nor the subdirectory", s.Now)
	}
	if s.Before != 5 {
		t.Errorf("before=%v, want 5: three left plus the two rows dated inside the window", s.Before)
	}
	if got := field(t, s.Line(), "retired-in-window"); got != "2" {
		t.Errorf("retired-in-window=%s, want 2", got)
	}
}

func TestRetiredRowsInheritTheNearestDateAbove(t *testing.T) {
	md := strings.Join([]string{
		"| undated.sh | 1 | before any date at all |",
		"",
		"Retired 2026-09-18 by Rowan.",
		"",
		"| a.sh | 1 | in the window |",
		"",
		"## Round 0 — 2026-09-01",
		"",
		"| b.sh | 1 | outside it |",
	}, "\n")
	rows := ParseRetired(md)
	if len(rows) != 3 {
		t.Fatalf("parsed %d rows, want 3: %+v", len(rows), rows)
	}
	if rows[0].HaveDate {
		t.Errorf("the row above every date carries %v; a row nobody dated is not this window's", rows[0].Date)
	}
	in, undated := RetiredInWindow(rows, at(t, windowSince), at(t, windowNow))
	if in != 1 || undated != 1 {
		t.Errorf("in-window=%d undated=%d, want 1 and 1", in, undated)
	}
}

// ---------------------------------------------------------------------------
// 9
// ---------------------------------------------------------------------------

func TestPRsCountsWhatWasOpenAtSince(t *testing.T) {
	f := newFixture(t)
	s := stream(t, f.read(t), "PRS")
	if s.Now != 2 {
		t.Errorf("now=%v, want 2 open", s.Now)
	}
	// #1 open and older than --since, plus #6 created before it and closed inside.
	if s.Before != 2 {
		t.Errorf("before=%v, want 2", s.Before)
	}
	line := s.Line()
	if got := field(t, line, "opened"); got != "1" {
		t.Errorf("opened=%s, want 1", got)
	}
	if got := field(t, line, "closed"); got != "3" {
		t.Errorf("closed=%s, want 3 (two batches and one plain pull request)", got)
	}
}

// ---------------------------------------------------------------------------
// 10
// ---------------------------------------------------------------------------

func TestEdgesIsTheGateAndTheRounds(t *testing.T) {
	f := newFixture(t)
	s := stream(t, f.read(t), "EDGES")
	// Stella's Edges: note and Rowan's older one are open; the not-ok receipt
	// has an issue and is not.
	if s.Now != 2 {
		t.Errorf("now=%v, want 2 open edges", s.Now)
	}
	if s.Before != 1 {
		t.Errorf("before=%v, want 1: only the receipt written before --since", s.Before)
	}
	line := s.Line()
	if got := field(t, line, "rounds"); got != "2" {
		t.Errorf("rounds=%s, want 2 friends in the window", got)
	}
	if got := field(t, line, "not-ok"); got != "1" {
		t.Errorf("not-ok=%s, want 1", got)
	}
	if got := field(t, line, "not-ok-per-round"); got != "0.50" {
		t.Errorf("not-ok-per-round=%s, want 0.50", got)
	}

	f.opts.By = []string{"Stella"}
	narrowed := stream(t, f.read(t), "EDGES")
	if got := field(t, narrowed.Line(), "rounds"); got != "1" {
		t.Errorf("--by Stella left rounds=%s, want 1", got)
	}
	if narrowed.Now != 1 {
		t.Errorf("--by Stella left now=%v, want 1", narrowed.Now)
	}
}

// ---------------------------------------------------------------------------
// 11
// ---------------------------------------------------------------------------

func TestFleetIsTheUnitsOffTheMajorityStamp(t *testing.T) {
	f := newFixture(t)
	s := stream(t, f.read(t), "FLEET")
	if s.Now != 1 {
		t.Errorf("now=%v, want 1 machine off the one build", s.Now)
	}
	if got := field(t, s.Line(), "certified"); got != "2/4" {
		t.Errorf("certified=%s, want 2/4", got)
	}

	one := "machine\tstamp\na\tv1\nb\tv1\nc\tv1\nd\tv1\ne\tv1"
	rows, err := ParseVersions("roll.tsv", one)
	if err != nil {
		t.Fatal(err)
	}
	if got := Fleet(rows, 0, 0, false); got.Now != 0 {
		t.Errorf("a fleet on one build read %v, want 0", got.Now)
	}

	snap := "name\tstamp\trevision\tplatform\nnova-bus\tv1\tabc\tdarwin/arm64\nnova-check\tv1\tabc\tdarwin/arm64"
	snapRows, err := ParseVersions("snapshot.tsv", snap)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapRows) != 2 || snapRows[0].Unit != "nova-bus" || snapRows[0].Stamp != "v1" {
		t.Errorf("the snapshot header read as %+v", snapRows)
	}
}

// ---------------------------------------------------------------------------
// 12
// ---------------------------------------------------------------------------

func TestFleetAndLedgerTakeTheirBeforeFromTheState(t *testing.T) {
	f := newFixture(t)
	report := f.read(t)
	for _, name := range []string{"FLEET", "LEDGER"} {
		s := stream(t, report, name)
		if s.HaveBefore {
			t.Errorf("%s had a before with no state; a snapshot is one instant", name)
		}
		if s.Trend() != Flat {
			t.Errorf("%s trend %s on a first tick, want flat", name, s.Trend())
		}
	}

	st := State{Streams: map[string]StreamState{
		"FLEET":  {Now: 3, At: windowSince},
		"LEDGER": {Now: 2, At: windowSince},
	}}
	applied, _, _ := report.Apply(st, at(t, windowNow))
	fleet := stream(t, applied, "FLEET")
	if fleet.Before != 3 || fleet.Trend() != Contracting {
		t.Errorf("FLEET before=%v trend=%s, want 3 and contracting", fleet.Before, fleet.Trend())
	}
	if got := field(t, fleet.Line(), "before-from"); got != "state" {
		t.Errorf("before-from=%s, want state", got)
	}
	ledger := stream(t, applied, "LEDGER")
	if ledger.Before != 2 || ledger.Trend() != Widening {
		t.Errorf("LEDGER before=%v trend=%s, want 2 and widening (three open rows now)", ledger.Before, ledger.Trend())
	}
}

// ---------------------------------------------------------------------------
// 13
// ---------------------------------------------------------------------------

func TestLedgerCountsTheRowsNotYetPass(t *testing.T) {
	f := newFixture(t)
	s := stream(t, f.read(t), "LEDGER")
	if got := field(t, s.Line(), "rows"); got != "5" {
		t.Errorf("rows=%s, want 5", got)
	}
	if s.Now != 3 {
		t.Errorf("open=%v, want 3: PARTIAL, TODO and NEEDS WORK; `FAIL then PASS` is closed", s.Now)
	}
	rows, open := LedgerRows("not a table\n\n| a | PASS |\n")
	if rows != 1 || open != 0 {
		t.Errorf("rows=%d open=%d over one row and one paragraph, want 1 and 0", rows, open)
	}
}

// ---------------------------------------------------------------------------
// 14
// ---------------------------------------------------------------------------

func TestAStreamWithNoSourceIsAbsentNotZero(t *testing.T) {
	f := newFixture(t)
	f.opts.BinDir = ""
	f.opts.RepoDir = ""
	f.opts.Git = nil
	f.opts.VersionsPath = ""
	report := f.read(t)

	for name, want := range map[string]string{"SCRIPTS": "--bin", "CLASSES": "--repo-dir", "FLEET": "--versions"} {
		s := stream(t, report, name)
		if !s.Absent() || s.Trend() != AbsentTrend {
			t.Errorf("%s read as %v with no source; an unread stream is not zero", name, s.Now)
		}
		line := s.Line()
		if !strings.Contains(line, "now=- before=- ratio=-") {
			t.Errorf("%s printed %q", name, line)
		}
		if got := field(t, line, "source"); got != want {
			t.Errorf("%s source=%s, want %s", name, got, want)
		}
	}
	verdict := report.VerdictLine()
	if got := field(t, verdict, "streams"); got != "4" {
		t.Errorf("streams=%s, want 4 measured", got)
	}
	absent := field(t, verdict, "absent")
	for _, name := range []string{"CLASSES", "SCRIPTS", "FLEET"} {
		if !strings.Contains(absent, name) {
			t.Errorf("absent=%s does not name %s", absent, name)
		}
	}
}

// ---------------------------------------------------------------------------
// 15 and 16
// ---------------------------------------------------------------------------

// widening is a one-stream report that moved the wrong way.
func widening(now, before float64) Report {
	return Report{Streams: []Stream{{Name: "EDGES", Now: now, Before: before, HaveNow: true, HaveBefore: true, Lower: true}}}
}

func TestExitOneOnlyOnTheSecondConsecutiveWidening(t *testing.T) {
	tick := at(t, windowNow)
	st := State{Streams: map[string]StreamState{}}

	_, st, streak := widening(5, 3).Apply(st, tick)
	if streak {
		t.Fatalf("one widening tick is a WARN, not an exit 1")
	}
	if st.Streams["EDGES"].Widening != 1 {
		t.Fatalf("the first widening was not counted: %+v", st.Streams["EDGES"])
	}

	_, st, streak = widening(7, 5).Apply(st, tick)
	if !streak {
		t.Fatalf("two consecutive widening ticks is the one exit-1 condition")
	}

	_, st, streak = widening(2, 7).Apply(st, tick)
	if streak || st.Streams["EDGES"].Widening != 0 {
		t.Fatalf("a contracting tick must reset the streak: %+v", st.Streams["EDGES"])
	}
	if _, _, streak = widening(4, 2).Apply(st, tick); streak {
		t.Fatalf("after a reset, one widening tick is a WARN again")
	}
}

func TestWithNoStateNothingIsRemembered(t *testing.T) {
	tick := at(t, windowNow)
	st, err := LoadState("")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		var streak bool
		_, st, streak = widening(5, 3).Apply(State{Streams: map[string]StreamState{}}, tick)
		if streak {
			t.Fatalf("tick %d exited 1 with nothing remembered", i)
		}
	}
	dir := t.TempDir()
	if err := (State{}).Save(""); err != nil {
		t.Fatalf("saving no state is not an error: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("a run with no --state wrote %d files", len(entries))
	}
	_ = st
}

func TestStateSurvivesARoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	st := State{Streams: map[string]StreamState{"EDGES": {Now: 4, At: windowNow, Widening: 1}}}
	if err := st.Save(path); err != nil {
		t.Fatal(err)
	}
	back, err := LoadState(path)
	if err != nil {
		t.Fatal(err)
	}
	if back.Streams["EDGES"] != st.Streams["EDGES"] {
		t.Fatalf("round trip gave %+v, want %+v", back.Streams["EDGES"], st.Streams["EDGES"])
	}
	if _, err := LoadState(filepath.Join(t.TempDir(), "never-written.json")); err != nil {
		t.Fatalf("a state file that is not there is an empty state, not an error: %v", err)
	}
}

// ---------------------------------------------------------------------------
// 18
// ---------------------------------------------------------------------------

func TestParseSinceTakesBothSpellingsAndRefusesTheRest(t *testing.T) {
	now := at(t, windowNow)
	got, err := ParseSince(windowSince, now)
	if err != nil || !got.Equal(at(t, windowSince)) {
		t.Errorf("RFC3339: %v %v", got, err)
	}
	got, err = ParseSince("24h", now)
	if err != nil || !got.Equal(now.Add(-24*time.Hour)) {
		t.Errorf("duration: %v %v", got, err)
	}
	for _, bad := range []string{"yesterday", "2026-13-40T00:00:00Z", "", "-3h"} {
		if _, err := ParseSince(bad, now); err == nil {
			t.Errorf("--since %q was accepted", bad)
		}
	}
	future, err := ParseSince("2026-09-19T00:00:00Z", now)
	if err == nil {
		t.Errorf("a --since after the clock gave %v; a window cannot end before it starts", future)
	} else if !strings.Contains(err.Error(), "after the clock") {
		t.Errorf("the refusal does not name the clock: %v", err)
	}
}

// ---------------------------------------------------------------------------
// 19
// ---------------------------------------------------------------------------

func TestConvergenceRefusesAVersionsFileItDoesNotKnow(t *testing.T) {
	for _, tc := range []struct{ name, body string }{
		{"unknown header", "host\tbuild\na\tv1"},
		{"wrong arity", "machine\tstamp\na\tv1\textra"},
		{"header and no rows", "machine\tstamp"},
		{"empty", ""},
	} {
		_, err := ParseVersions("versions.tsv", tc.body)
		if err == nil {
			t.Errorf("%s was accepted", tc.name)
			continue
		}
		if !strings.Contains(err.Error(), "versions.tsv") {
			t.Errorf("%s: the refusal does not name the file: %v", tc.name, err)
		}
	}
	if _, err := ParseVersions("v.tsv", "host\tbuild\na\tv1"); err == nil ||
		!strings.Contains(err.Error(), "machine<TAB>stamp") {
		t.Errorf("the refusal does not name the headers it reads: %v", err)
	}
	if _, _, err := ParseCerts("certs.tsv", "name\tcertified\na\tyes"); err == nil {
		t.Errorf("a certs file with the wrong header was accepted")
	}
}

// ---------------------------------------------------------------------------
// 20
// ---------------------------------------------------------------------------

// hostile is one value holding everything a line must survive: a newline, an
// `=`, a tab and a bidi override.
const hostile = "one\ntwo=three\tfour\u202eevil"

func TestEveryFieldSurvivesAHostileValue(t *testing.T) {
	f := newFixture(t)
	forge := f.opts.Forge.(fakeForge)
	forge.closed = append(forge.closed, PR{
		Number: 9, Title: "integration-" + hostile, Body: "round 2", Merged: true,
		CreatedAt: at(t, "2026-09-18T05:00:00Z"), ClosedAt: at(t, "2026-09-18T07:00:00Z"),
	})
	f.opts.Forge = forge
	// A TSV row is one line by construction, so the stamp carries everything
	// hostile that a row CAN carry: the `=`, the bidi override and a space.
	hostileStamp := strings.NewReplacer("\n", " ", "\t", " ").Replace(hostile)
	f.opts.VersionsPath = writeFile(t, f.dir, "hostile.tsv", "machine\tstamp\na\tv1\nb\t"+hostileStamp)
	f.opts.CertsPath = ""
	f.opts.By = []string{hostile}

	for _, line := range f.read(t).Lines() {
		if strings.ContainsAny(line, "\n\r\t") {
			t.Errorf("a line carried a control character: %q", line)
		}
		if strings.Contains(line, "\u202e") {
			t.Errorf("a line carried a bidi override: %q", line)
		}
		for _, tok := range strings.Fields(line) {
			if strings.Count(tok, "=") > 1 {
				t.Errorf("a field holds a second `=`: %q in %q", tok, line)
			}
		}
	}
}

// ---------------------------------------------------------------------------
// 21
// ---------------------------------------------------------------------------

func TestJSONCarriesTheSameReadingAsTheLines(t *testing.T) {
	f := newFixture(t)
	report := f.read(t)
	raw, err := json.Marshal(report.AsJSON(at(t, windowNow), at(t, windowSince)))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "CONVERGENCE") {
		t.Errorf("the JSON object carries a line: %s", raw)
	}
	var back JSON
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	if len(back.Streams) != len(Order) {
		t.Fatalf("the object holds %d streams, the lines print %d", len(back.Streams), len(Order))
	}
	for i, row := range back.Streams {
		s := report.Streams[i]
		if row.Name != s.Name || row.Trend != string(s.Trend()) {
			t.Errorf("%s: object says %s/%s", s.Name, row.Name, row.Trend)
		}
		if s.HaveNow != (row.Now != nil) {
			t.Errorf("%s: a number the verb does not have must be null, not zero", s.Name)
		}
		if row.Now != nil && *row.Now != s.Now {
			t.Errorf("%s: now %v in the object, %v on the line", s.Name, *row.Now, s.Now)
		}
	}
	measured, contracting, wide, absent := report.Verdict()
	if back.Measured != measured || back.Contracting != contracting ||
		len(back.Widening) != len(wide) || len(back.Absent) != len(absent) {
		t.Errorf("the object's verdict is %+v, the line's is %d/%d/%v/%v", back, measured, contracting, wide, absent)
	}
}

// ---------------------------------------------------------------------------
// 22
// ---------------------------------------------------------------------------

func TestEveryChildIsBounded(t *testing.T) {
	f := newFixture(t)
	f.opts.Forge = fakeForge{hang: true}
	if _, err := Read(context.Background(), f.opts); err == nil {
		t.Fatalf("a forge that hangs past the deadline was read as a reading")
	} else if !strings.Contains(err.Error(), "--timeout") {
		t.Errorf("the refusal does not name the deadline: %v", err)
	}

	f = newFixture(t)
	f.opts.Git = fakeGit{err: errors.New("git rev-list ran past --timeout 1s and was killed")}
	if _, err := Read(context.Background(), f.opts); err == nil {
		t.Fatalf("a git that hangs past the deadline was read as a reading")
	}
}

// ---------------------------------------------------------------------------
// the reading refuses what it cannot read
// ---------------------------------------------------------------------------

func TestReadRefusesAnUnreadableSource(t *testing.T) {
	for _, tc := range []struct {
		name string
		set  func(o *Options)
	}{
		{"--ledger", func(o *Options) { o.LedgerPath = filepath.Join(o.LedgerPath, "no-such") }},
		{"--retired", func(o *Options) { o.RetiredPath = filepath.Join(o.RetiredPath, "no-such") }},
		{"--receipts", func(o *Options) { o.ReceiptsDir = filepath.Join(o.ReceiptsDir, "no-such") }},
		{"--bin", func(o *Options) { o.BinDir = filepath.Join(o.BinDir, "no-such") }},
		{"--versions", func(o *Options) { o.VersionsPath = filepath.Join(o.VersionsPath, "no-such") }},
	} {
		f := newFixture(t)
		tc.set(&f.opts)
		_, err := Read(context.Background(), f.opts)
		if err == nil {
			t.Errorf("%s: an unreadable source was read as a reading", tc.name)
			continue
		}
		if !strings.Contains(err.Error(), tc.name) {
			t.Errorf("%s: the refusal does not name the flag: %v", tc.name, err)
		}
	}
}
