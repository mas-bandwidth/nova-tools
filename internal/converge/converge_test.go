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

	"github.com/mas-bandwidth/nova-tools/pkg/dogfood"
	"github.com/mas-bandwidth/nova-tools/pkg/readregular"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
	require.NoError(t, err, "bad fixture time %q: %v", s, err)
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
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(body), 0o644))
	return path
}

// writeReceipt writes one dogfood receipt into a receipts directory.
func writeReceipt(t *testing.T, dir, name string, r dogfood.Receipt) {
	t.Helper()
	require.NoError(t, os.MkdirAll(dir, 0o755))
	raw, err := json.Marshal(r)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, name), raw, 0o644))
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
	require.NoError(t, err, "Read")
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
	require.Failf(t, "no such stream", "no %s stream in the reading; got %d streams", name, len(r.Streams))
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
	require.Failf(t, "no such field", "no %s= field in %q", key, line)
	return ""
}

// assertField checks one key=value extra of a printed line; msg is the failure
// message and takes the value read as its one %s.
func assertField(t *testing.T, line, key, want, msg string) {
	t.Helper()
	got := field(t, line, key)
	assert.Equal(t, want, got, msg, got)
}

// ---------------------------------------------------------------------------
// 1
// ---------------------------------------------------------------------------

func TestConvergencePrintsOneLinePerStream(t *testing.T) {
	t.Parallel()

	lines := newFixture(t).read(t).Lines()
	require.Len(t, lines, len(Order)+1, "want %d stream lines and one verdict, got %d:\n%s", len(Order), len(lines), strings.Join(lines, "\n"))
	for i, name := range Order {
		want := "CONVERGENCE " + name + " "
		assert.True(t, strings.HasPrefix(lines[i], want), "line %d is %q, want it to start %q", i, lines[i], want)
		for _, key := range []string{"now", "before", "ratio", "trend"} {
			field(t, lines[i], key)
		}
	}
	last := lines[len(lines)-1]
	assert.True(t, strings.HasPrefix(last, "CONVERGENCE OK ") || strings.HasPrefix(last, "CONVERGENCE WARN "), "verdict line is %q", last)
	for _, key := range []string{"streams", "contracting", "widening", "absent"} {
		field(t, last, key)
	}
}

// ---------------------------------------------------------------------------
// 2
// ---------------------------------------------------------------------------

func TestATrendIsTheDirectionTheStreamConverges(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name        string
		now, before float64
		lower       bool
		want        Trend
	}{
		{"fewer open edges", 3, 5, true, Contracting},
		{"more open edges", 7, 5, true, Widening},
		{"unmoved", 5, 5, true, Flat},
		{"more class tests", 29, 27, false, Contracting},
		{"fewer class tests", 25, 27, false, Widening},
		{"unmoved class tests", 27, 27, false, Flat},
	} {
		s := Stream{Name: "X", Now: tc.now, Before: tc.before, HaveNow: true, HaveBefore: true, Lower: tc.lower}
		got := s.Trend()
		assert.Equal(t, tc.want, got, "%s: trend %s, want %s", tc.name, got, tc.want)
	}
}

// ---------------------------------------------------------------------------
// 3
// ---------------------------------------------------------------------------

func TestRatioIsAlwaysNowOverBefore(t *testing.T) {
	t.Parallel()

	for _, lower := range []bool{true, false} {
		s := Stream{Now: 3, Before: 4, HaveNow: true, HaveBefore: true, Lower: lower}
		assert.Contains(t, s.Line(), "ratio=0.75", "lower=%v: %q wants ratio=0.75", lower, s.Line())
	}
	zero := Stream{Now: 3, Before: 0, HaveNow: true, HaveBefore: true, Lower: true}
	assert.Contains(t, zero.Line(), "ratio=-", "a before of zero printed %q; an infinity is not a measurement", zero.Line())
	both := Stream{Now: 0, Before: 0, HaveNow: true, HaveBefore: true, Lower: true}
	assert.Contains(t, both.Line(), "ratio=1", "zero against zero printed %q trend %s", both.Line(), both.Trend())
	assert.Equal(t, Flat, both.Trend(), "zero against zero printed %q trend %s", both.Line(), both.Trend())
}

// ---------------------------------------------------------------------------
// 4 and 5
// ---------------------------------------------------------------------------

func TestLandingReadsRoundsFromTheBodyAndTheLogs(t *testing.T) {
	t.Parallel()

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
	assert.Equal(t, 3.5, s.Now, "rounds per batch now=%v, want 3.5 (logs beat the body)", s.Now)
	assertField(t, s.Line(), "batches", "2", "batches=%s, want 2")
}

func TestLandingComparesTheWindowWithTheOneBefore(t *testing.T) {
	t.Parallel()

	s := stream(t, newFixture(t).read(t), "LANDING")
	assert.Equal(t, float64(3), s.Now, "now=%v, want 3", s.Now) // #3 round 2 and #4 round 4
	// #5, merged in the twelve hours before --since
	assert.Equal(t, float64(6), s.Before, "before=%v, want 6 (the batch in the window before --since)", s.Before)
	assert.Equal(t, Contracting, s.Trend(), "trend %s, want contracting: fewer rounds per batch is landing getting cheaper", s.Trend())
	assertField(t, s.Line(), "per-hour", "0.17", "per-hour=%s, want 0.17 (two batches in twelve hours)")
	assertField(t, s.Line(), "prev-batches", "1", "prev-batches=%s, want 1")
}

// ---------------------------------------------------------------------------
// 6
// ---------------------------------------------------------------------------

func TestClassesCountsTheIndexEntriesAtBothRevisions(t *testing.T) {
	t.Parallel()

	s := stream(t, newFixture(t).read(t), "CLASSES")
	require.Equal(t, float64(29), s.Now, "now=%v before=%v, want 29 and 27; the `###` outside the index must not be counted", s.Now, s.Before)
	require.Equal(t, float64(27), s.Before, "now=%v before=%v, want 29 and 27; the `###` outside the index must not be counted", s.Now, s.Before)
	assert.Equal(t, Contracting, s.Trend(), "trend %s, want contracting: a class made mechanical cannot come back", s.Trend())
	assertField(t, s.Line(), "rev", "abc123456789", "rev=%s, want the revision the fake git named")
}

// ---------------------------------------------------------------------------
// 7 and 8
// ---------------------------------------------------------------------------

func TestScriptsCountsWhatIsLeftAndWhatTheWindowRetired(t *testing.T) {
	t.Parallel()

	s := stream(t, newFixture(t).read(t), "SCRIPTS")
	assert.Equal(t, float64(3), s.Now, "now=%v, want 3: two .sh, one shebang, and neither the binary nor the subdirectory", s.Now)
	assert.Equal(t, float64(5), s.Before, "before=%v, want 5: three left plus the two rows dated inside the window", s.Before)
	assertField(t, s.Line(), "retired-in-window", "2", "retired-in-window=%s, want 2")
}

func TestRetiredRowsInheritTheNearestDateAbove(t *testing.T) {
	t.Parallel()

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
	require.Len(t, rows, 3, "parsed %d rows, want 3: %+v", len(rows), rows)
	assert.False(t, rows[0].HaveDate, "the row above every date carries %v; a row nobody dated is not this window's", rows[0].Date)
	in, undated := RetiredInWindow(rows, at(t, windowSince), at(t, windowNow))
	assert.Equal(t, 1, in, "in-window=%d undated=%d, want 1 and 1", in, undated)
	assert.Equal(t, 1, undated, "in-window=%d undated=%d, want 1 and 1", in, undated)
}

// ---------------------------------------------------------------------------
// 9
// ---------------------------------------------------------------------------

func TestPRsCountsWhatWasOpenAtSince(t *testing.T) {
	t.Parallel()

	s := stream(t, newFixture(t).read(t), "PRS")
	assert.Equal(t, float64(2), s.Now, "now=%v, want 2 open", s.Now)
	// #1 open and older than --since, plus #6 created before it and closed inside.
	assert.Equal(t, float64(2), s.Before, "before=%v, want 2", s.Before)
	assertField(t, s.Line(), "opened", "1", "opened=%s, want 1")
	assertField(t, s.Line(), "closed", "3", "closed=%s, want 3 (two batches and one plain pull request)")
}

// ---------------------------------------------------------------------------
// 10
// ---------------------------------------------------------------------------

func TestEdgesIsTheGateAndTheRounds(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	s := stream(t, f.read(t), "EDGES")
	// Stella's Edges: note and Rowan's older one are open; the not-ok receipt
	// has an issue and is not.
	assert.Equal(t, float64(2), s.Now, "now=%v, want 2 open edges", s.Now)
	assert.Equal(t, float64(1), s.Before, "before=%v, want 1: only the receipt written before --since", s.Before)
	assertField(t, s.Line(), "rounds", "2", "rounds=%s, want 2 friends in the window")
	assertField(t, s.Line(), "not-ok", "1", "not-ok=%s, want 1")
	assertField(t, s.Line(), "not-ok-per-round", "0.50", "not-ok-per-round=%s, want 0.50")

	f.opts.By = []string{"Stella"}
	narrowed := stream(t, f.read(t), "EDGES")
	assertField(t, narrowed.Line(), "rounds", "1", "--by Stella left rounds=%s, want 1")
	assert.Equal(t, float64(1), narrowed.Now, "--by Stella left now=%v, want 1", narrowed.Now)
}

// ---------------------------------------------------------------------------
// 11
// ---------------------------------------------------------------------------

func TestFleetIsTheUnitsOffTheMajorityStamp(t *testing.T) {
	t.Parallel()

	s := stream(t, newFixture(t).read(t), "FLEET")
	assert.Equal(t, float64(1), s.Now, "now=%v, want 1 machine off the one build", s.Now)
	assertField(t, s.Line(), "certified", "2/4", "certified=%s, want 2/4")

	one := "machine\tstamp\na\tv1\nb\tv1\nc\tv1\nd\tv1\ne\tv1"
	rows, err := ParseVersions("roll.tsv", one)
	require.NoError(t, err)
	got := Fleet(rows, 0, 0, false)
	assert.Equal(t, float64(0), got.Now, "a fleet on one build read %v, want 0", got.Now)

	snap := "name\tstamp\trevision\tplatform\nnova-bus\tv1\tabc\tdarwin/arm64\nnova-check\tv1\tabc\tdarwin/arm64"
	snapRows, err := ParseVersions("snapshot.tsv", snap)
	require.NoError(t, err)
	require.Len(t, snapRows, 2, "the snapshot header read as %+v", snapRows)
	assert.Equal(t, "nova-bus", snapRows[0].Unit, "the snapshot header read as %+v", snapRows)
	assert.Equal(t, "v1", snapRows[0].Stamp, "the snapshot header read as %+v", snapRows)
}

// ---------------------------------------------------------------------------
// 12
// ---------------------------------------------------------------------------

func TestFleetAndLedgerTakeTheirBeforeFromTheState(t *testing.T) {
	t.Parallel()

	report := newFixture(t).read(t)
	for _, name := range []string{"FLEET", "LEDGER"} {
		s := stream(t, report, name)
		assert.False(t, s.HaveBefore, "%s had a before with no state; a snapshot is one instant", name)
		assert.Equal(t, Flat, s.Trend(), "%s trend %s on a first tick, want flat", name, s.Trend())
	}

	st := State{Streams: map[string]StreamState{
		"FLEET":  {Now: 3, At: windowSince},
		"LEDGER": {Now: 2, At: windowSince},
	}}
	applied, _, _ := report.Apply(st, at(t, windowNow))
	fleet := stream(t, applied, "FLEET")
	assert.Equal(t, float64(3), fleet.Before, "FLEET before=%v trend=%s, want 3 and contracting", fleet.Before, fleet.Trend())
	assert.Equal(t, Contracting, fleet.Trend(), "FLEET before=%v trend=%s, want 3 and contracting", fleet.Before, fleet.Trend())
	assertField(t, fleet.Line(), "before-from", "state", "before-from=%s, want state")
	ledger := stream(t, applied, "LEDGER")
	assert.Equal(t, float64(2), ledger.Before, "LEDGER before=%v trend=%s, want 2 and widening (three open rows now)", ledger.Before, ledger.Trend())
	assert.Equal(t, Widening, ledger.Trend(), "LEDGER before=%v trend=%s, want 2 and widening (three open rows now)", ledger.Before, ledger.Trend())
}

// ---------------------------------------------------------------------------
// 13
// ---------------------------------------------------------------------------

func TestLedgerCountsTheRowsNotYetPass(t *testing.T) {
	t.Parallel()

	s := stream(t, newFixture(t).read(t), "LEDGER")
	assertField(t, s.Line(), "rows", "5", "rows=%s, want 5")
	assert.Equal(t, float64(3), s.Now, "open=%v, want 3: PARTIAL, TODO and NEEDS WORK; `FAIL then PASS` is closed", s.Now)
	rows, open := LedgerRows("not a table\n\n| a | PASS |\n")
	assert.Equal(t, 1, rows, "rows=%d open=%d over one row and one paragraph, want 1 and 0", rows, open)
	assert.Equal(t, 0, open, "rows=%d open=%d over one row and one paragraph, want 1 and 0", rows, open)
}

// ---------------------------------------------------------------------------
// 14
// ---------------------------------------------------------------------------

func TestAStreamWithNoSourceIsAbsentNotZero(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	f.opts.BinDir = ""
	f.opts.RepoDir = ""
	f.opts.Git = nil
	f.opts.VersionsPath = ""
	report := f.read(t)

	for name, want := range map[string]string{"SCRIPTS": "--bin", "CLASSES": "--repo-dir", "FLEET": "--versions"} {
		s := stream(t, report, name)
		assert.True(t, s.Absent(), "%s read as %v with no source; an unread stream is not zero", name, s.Now)
		assert.Equal(t, AbsentTrend, s.Trend(), "%s read as %v with no source; an unread stream is not zero", name, s.Now)
		line := s.Line()
		assert.Contains(t, line, "now=- before=- ratio=-", "%s printed %q", name, line)
		got := field(t, line, "source")
		assert.Equal(t, want, got, "%s source=%s, want %s", name, got, want)
	}
	verdict := report.VerdictLine()
	assertField(t, verdict, "streams", "4", "streams=%s, want 4 measured")
	absent := field(t, verdict, "absent")
	for _, name := range []string{"CLASSES", "SCRIPTS", "FLEET"} {
		assert.Contains(t, absent, name, "absent=%s does not name %s", absent, name)
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
	t.Parallel()

	tick := at(t, windowNow)
	st := State{Streams: map[string]StreamState{}}

	_, st, streak := widening(5, 3).Apply(st, tick)
	require.False(t, streak, "one widening tick is a WARN, not an exit 1")
	require.Equal(t, 1, st.Streams["EDGES"].Widening, "the first widening was not counted: %+v", st.Streams["EDGES"])

	_, st, streak = widening(7, 5).Apply(st, tick.Add(time.Hour))
	require.True(t, streak, "two consecutive widening ticks is the one exit-1 condition")

	_, st, streak = widening(2, 7).Apply(st, tick.Add(2*time.Hour))
	require.False(t, streak, "a contracting tick must reset the streak: %+v", st.Streams["EDGES"])
	require.Equal(t, 0, st.Streams["EDGES"].Widening, "a contracting tick must reset the streak: %+v", st.Streams["EDGES"])
	_, _, streak = widening(4, 2).Apply(st, tick.Add(3*time.Hour))
	require.False(t, streak, "after a reset, one widening tick is a WARN again")
}

// The edge the first real run of this verb found: two invocations over one
// window are one tick read twice, and counting the second would have gone red
// on a reading nobody took.
func TestTheSameTickReadTwiceIsNotTwoTicks(t *testing.T) {
	t.Parallel()

	tick := at(t, windowNow)
	st := State{Streams: map[string]StreamState{}}

	_, st, streak := widening(5, 3).Apply(st, tick)
	require.False(t, streak, "the first tick: streak=%v state=%+v", streak, st.Streams["EDGES"])
	require.Equal(t, 1, st.Streams["EDGES"].Widening, "the first tick: streak=%v state=%+v", streak, st.Streams["EDGES"])
	for i := 0; i < 3; i++ {
		var again State
		_, again, streak = widening(5, 3).Apply(st, tick)
		require.False(t, streak, "re-reading the same tick %d times went red", i+1)
		require.Equal(t, 1, again.Streams["EDGES"].Widening, "re-reading the same tick counted it again: %+v", again.Streams["EDGES"])
		st = again
	}
	// A tick of the clock later, the same widening IS the second one.
	_, _, streak = widening(6, 3).Apply(st, tick.Add(time.Hour))
	require.True(t, streak, "a later tick that widens again is the exit-1 condition")
}

func TestWithNoStateNothingIsRemembered(t *testing.T) {
	t.Parallel()

	tick := at(t, windowNow)
	_, err := LoadState("")
	require.NoError(t, err)
	for i := 0; i < 2; i++ {
		_, _, streak := widening(5, 3).Apply(State{Streams: map[string]StreamState{}}, tick)
		require.False(t, streak, "tick %d exited 1 with nothing remembered", i)
	}
	dir := t.TempDir()
	require.NoError(t, (State{}).Save(""), "saving no state is not an error")
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Empty(t, entries, "a run with no --state wrote %d files", len(entries))
}

func TestStateSurvivesARoundTrip(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "state.json")
	st := State{Streams: map[string]StreamState{"EDGES": {Now: 4, At: windowNow, Widening: 1}}}
	require.NoError(t, st.Save(path))
	back, err := LoadState(path)
	require.NoError(t, err)
	require.Equal(t, st.Streams["EDGES"], back.Streams["EDGES"], "round trip gave %+v, want %+v", back.Streams["EDGES"], st.Streams["EDGES"])
	_, err = LoadState(filepath.Join(t.TempDir(), "never-written.json"))
	require.NoError(t, err, "a state file that is not there is an empty state, not an error")
}

// ---------------------------------------------------------------------------
// 18
// ---------------------------------------------------------------------------

func TestParseSinceTakesBothSpellingsAndRefusesTheRest(t *testing.T) {
	t.Parallel()

	now := at(t, windowNow)
	got, err := ParseSince(windowSince, now)
	require.NoError(t, err, "RFC3339: %v %v", got, err)
	assert.True(t, got.Equal(at(t, windowSince)), "RFC3339: %v %v", got, err)
	got, err = ParseSince("24h", now)
	require.NoError(t, err, "duration: %v %v", got, err)
	assert.True(t, got.Equal(now.Add(-24*time.Hour)), "duration: %v %v", got, err)
	for _, bad := range []string{"yesterday", "2026-13-40T00:00:00Z", "", "-3h"} {
		_, err := ParseSince(bad, now)
		assert.Error(t, err, "--since %q was accepted", bad)
	}
	future, err := ParseSince("2026-09-19T00:00:00Z", now)
	require.Error(t, err, "a --since after the clock gave %v; a window cannot end before it starts", future)
	assert.ErrorContains(t, err, "after the clock", "the refusal does not name the clock: %v", err)
}

// ---------------------------------------------------------------------------
// 19
// ---------------------------------------------------------------------------

func TestConvergenceRefusesAVersionsFileItDoesNotKnow(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct{ name, body string }{
		{"unknown header", "host\tbuild\na\tv1"},
		{"wrong arity", "machine\tstamp\na\tv1\textra"},
		{"header and no rows", "machine\tstamp"},
		{"empty", ""},
	} {
		_, err := ParseVersions("versions.tsv", tc.body)
		if assert.Error(t, err, "%s was accepted", tc.name) {
			assert.ErrorContains(t, err, "versions.tsv", "%s: the refusal does not name the file: %v", tc.name, err)
		}
	}
	_, err := ParseVersions("v.tsv", "host\tbuild\na\tv1")
	assert.ErrorContains(t, err, "machine<TAB>stamp", "the refusal does not name the headers it reads")
	_, _, err = ParseCerts("certs.tsv", "name\tcertified\na\tyes")
	assert.Error(t, err, "a certs file with the wrong header was accepted")
}

// ---------------------------------------------------------------------------
// 20
// ---------------------------------------------------------------------------

// hostile is one value holding everything a line must survive: a newline, an
// `=`, a tab and a bidi override.
const hostile = "one\ntwo=three\tfour\u202eevil"

func TestEveryFieldSurvivesAHostileValue(t *testing.T) {
	t.Parallel()

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
		assert.False(t, strings.ContainsAny(line, "\n\r\t"), "a line carried a control character: %q", line)
		assert.NotContains(t, line, "\u202e", "a line carried a bidi override: %q", line)
		for _, tok := range strings.Fields(line) {
			assert.LessOrEqual(t, strings.Count(tok, "="), 1, "a field holds a second `=`: %q in %q", tok, line)
		}
	}
}

// ---------------------------------------------------------------------------
// 21
// ---------------------------------------------------------------------------

func TestJSONCarriesTheSameReadingAsTheLines(t *testing.T) {
	t.Parallel()

	report := newFixture(t).read(t)
	raw, err := json.Marshal(report.AsJSON(at(t, windowNow), at(t, windowSince)))
	require.NoError(t, err)
	assert.NotContains(t, string(raw), "CONVERGENCE", "the JSON object carries a line: %s", raw)
	var back JSON
	require.NoError(t, json.Unmarshal(raw, &back))
	require.Len(t, back.Streams, len(Order), "the object holds %d streams, the lines print %d", len(back.Streams), len(Order))
	for i, row := range back.Streams {
		s := report.Streams[i]
		assert.Equal(t, s.Name, row.Name, "%s: object says %s/%s", s.Name, row.Name, row.Trend)
		assert.Equal(t, string(s.Trend()), row.Trend, "%s: object says %s/%s", s.Name, row.Name, row.Trend)
		assert.Equal(t, s.HaveNow, row.Now != nil, "%s: a number the verb does not have must be null, not zero", s.Name)
		if row.Now != nil {
			assert.Equal(t, s.Now, *row.Now, "%s: now %v in the object, %v on the line", s.Name, *row.Now, s.Now)
		}
	}
	measured, contracting, wide, absent := report.Verdict()
	verdict := fmt.Sprintf("the object's verdict is %+v, the line's is %d/%d/%v/%v", back, measured, contracting, wide, absent)
	assert.Equal(t, measured, back.Measured, verdict)
	assert.Equal(t, contracting, back.Contracting, verdict)
	assert.Len(t, back.Widening, len(wide), verdict)
	assert.Len(t, back.Absent, len(absent), verdict)
}

// ---------------------------------------------------------------------------
// 22
// ---------------------------------------------------------------------------

func TestEveryChildIsBounded(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	f.opts.Forge = fakeForge{hang: true}
	_, err := Read(context.Background(), f.opts)
	require.Error(t, err, "a forge that hangs past the deadline was read as a reading")
	assert.ErrorContains(t, err, "--timeout", "the refusal does not name the deadline: %v", err)

	f = newFixture(t)
	f.opts.Git = fakeGit{err: errors.New("git rev-list ran past --timeout 1s and was killed")}
	_, err = Read(context.Background(), f.opts)
	require.Error(t, err, "a git that hangs past the deadline was read as a reading")
}

// ---------------------------------------------------------------------------
// the reading refuses what it cannot read
// ---------------------------------------------------------------------------

func TestReadRefusesAnUnreadableSource(t *testing.T) {
	t.Parallel()

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
		if assert.Error(t, err, "%s: an unreadable source was read as a reading", tc.name) {
			assert.ErrorContains(t, err, tc.name, "%s: the refusal does not name the flag: %v", tc.name, err)
		}
	}
}

func TestLoadStateRefusesAFileOverTheReadCap(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "state.json")
	require.NoError(t, os.WriteFile(path, nil, 0o644))
	require.NoError(t, os.Truncate(path, readregular.DefaultMax+1))

	_, err := LoadState(path)
	require.Error(t, err, "a state file over the read cap was accepted")
	require.ErrorContains(t, err, fmt.Sprintf("exceeds limit %d", readregular.DefaultMax))

	st, err := LoadState(filepath.Join(t.TempDir(), "missing-state.json"))
	require.NoError(t, err)
	require.Empty(t, st.Streams)
}
