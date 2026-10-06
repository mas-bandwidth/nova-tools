package friend

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The daemon reads every outbox job (the night of 2026-10-05: eight working cards whose
// outbox REPORT.md said Verdict: LAND with a Head sat unread for two hours, because the
// daemon finished only the jobs its own lanes ran, and the coordinator's stopgap had
// written those briefs). Here no brief is the daemon's: each is put in her inbox by
// another hand before the daemon starts, and its card is on her row.
func outboxReport(t *testing.T, dir, job, report string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "outbox", job), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "outbox", job, "REPORT.md"), []byte(report), 0o644))
}

func TestTheDaemonFinishesAReportItDidNotStage(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	dir := r.d.Dir
	row := &twinRow{}
	r.d.Held = row.held
	f := &finishes{}
	r.d.Finish = f.finish

	const head = "0123456789abcdef0123456789abcdef01234567"
	land, hold, fail := workCard("landed.w1", "working"), workCard("held.w1", "working"), workCard("failed.w1", "working")
	gen := workCard("again.w1", "working")
	gen.Job, gen.Gen = "again.w1~15.g3", 3
	silent, ready := workCard("silent.w1", "working"), workCard("ready.w1", "ready")
	for _, c := range []HeldCard{land, hold, fail, gen, silent, ready} {
		inboxJob(t, dir, c.Job, c.Brief) // the coordinator's stopgap wrote them: none is the daemon's
	}
	long := "the bench went red.\n" + strings.Repeat("x", 700) + "\n"
	outboxReport(t, dir, land.Job, "Verdict: LAND\nHead: "+head+"\n\nThe card is done.\n")
	outboxReport(t, dir, hold.Job, "Verdict: HOLD\nHead: "+head+"\n\nno push\n")
	outboxReport(t, dir, fail.Job, "Verdict: FAIL\n\n"+long)
	outboxReport(t, dir, gen.Job, "**Verdict:** land\nHead: "+strings.ToUpper(head)+"\n\nthird time.\n")
	outboxReport(t, dir, silent.Job, "I am still working on it.\n")
	outboxReport(t, dir, ready.Job, "Verdict: LAND\nHead: "+head+"\n")
	outboxReport(t, dir, "gone.w1~15", "Verdict: LAND\nHead: "+head+"\n") // its card is not on her row
	outboxReport(t, dir, "not-a-job", "Verdict: LAND\nHead: "+head+"\n")  // no <work>~<epoch> name
	row.set(land, hold, fail, gen, silent, ready)

	r.run(t, 3)

	got := map[string][]string{}
	for _, argv := range f.got() {
		require.GreaterOrEqual(t, len(argv), 4, "%v", argv)
		got[argv[3]] = argv
	}
	require.Len(t, got, 4, "one finish per working card with a verdict, sent once over three loops: %v", f.got())
	assert.Equal(t, []string{"finish", "--as", "friend.bob", "landed.w1@1", "--epoch", "15", "--head", head, "--branch", "sprint/landed.w1.g1.e15", "--report", "friend bob LAND: The card is done."}, got["landed.w1@1"])
	assert.Equal(t, []string{"finish", "--as", "friend.bob", "again.w1@3", "--epoch", "15", "--head", head, "--branch", "sprint/again.w1.g1.e15", "--report", "friend bob LAND: third time."}, got["again.w1@3"], "a job's generation is its finish's; a sha is read in lower case")
	assert.Equal(t, []string{"finish", "--as", "friend.bob", "held.w1@1", "--epoch", "15", "--failed", "--head", head, "--branch", "sprint/held.w1.g1.e15", "--report", "friend bob HOLD: Verdict: HOLD Head: " + head + " no push"}, got["held.w1@1"])
	failed := got["failed.w1@1"]
	require.Len(t, failed, 11)
	assert.Equal(t, []string{"finish", "--as", "friend.bob", "failed.w1@1", "--epoch", "15", "--failed", "--branch", "sprint/failed.w1.g1.e15", "--report"}, failed[:10])
	assert.Equal(t, "friend bob FAIL: "+oneLine(("Verdict: FAIL\n\n" + long)[:600], 600), failed[10], "a failed finish carries the report's first 600 characters")

	count := func(sub string) int {
		n := 0
		for _, l := range r.records {
			if strings.Contains(l, sub) {
				n++
			}
		}
		return n
	}
	assert.Equal(t, 1, count("outbox: left outbox/silent.w1~15/REPORT.md: it has no Verdict line"), "a report with no verdict is noted once: %v", r.records)
	assert.Equal(t, 1, count("outbox: left outbox/ready.w1~15/REPORT.md: card ready.w1 is ready on her row, not working"), "%v", r.records)
	assert.Equal(t, 1, count("outbox: left outbox/gone.w1~15/REPORT.md: card gone.w1 is not on her row"), "%v", r.records)
	assert.Equal(t, 0, count("not-a-job"), "a directory no card names is not hers to finish")
	assert.Equal(t, 4, count("outbox: finished card "), "one line per finish: %v", r.records)

	// she writes the verdict: the next pass finishes it; a finished job is not noted after it leaves her row
	outboxReport(t, dir, silent.Job, "Verdict: FAIL\n\nstuck.\n")
	row.set(silent, ready)
	r.run(t, 2)
	assert.Len(t, f.got(), 5)
	assert.Equal(t, "silent.w1@1", f.got()[4][3])
	assert.Equal(t, 0, count("card landed.w1 is not on her row"), "a job the daemon finished is not noted when its card leaves her row")
}

func TestReportVerdictReadsTheFriendsWords(t *testing.T) {
	t.Parallel()
	for report, want := range map[string][2]string{
		"Verdict: LAND\nHead: ABC\n":        {"LAND", "abc"},
		"## Verdict: hold.\n":               {"HOLD", ""},
		"- **Verdict**: FAIL (no bench)\n":  {"FAIL", ""},
		"no verdict here\nHead: abc\n":      {"", "abc"},
		"Head: `abc`, pushed\nVerdict: L\n": {"L", "abc"},
	} {
		v, h := reportVerdict(report)
		assert.Equal(t, want, [2]string{v, h}, report)
	}
}
