package friend

import (
	"context"
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
	assert.Equal(t, 1, count("outbox: left outbox/gone.w1~15/REPORT.md: refused: card gone.w1 is not on her row, no longer hers; no row the daemon reads says who holds it now (nova-sprint view coordinator does)"), "%v", r.records)
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

// The daemon's duty is nova-sprint collect's for her own tree: a LAND finishes only at
// origin's tip of the card's branch (refused naming the branch otherwise), and a lane her
// runner ENDed with no report (and no LIMIT) is a dead lane, its REPORT.md written FAIL and
// its card finished --failed so it is dealt again.
func TestTheDaemonFinishesADeadLaneAndALandOnlyAtOriginsTip(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	dir := r.d.Dir
	row := &twinRow{}
	r.d.Held = row.held
	f := &finishes{}
	r.d.Finish = f.finish
	const head, other = "0123456789abcdef0123456789abcdef01234567", "fedcba9876543210fedcba9876543210fedcba98"
	r.d.Tip = func(_ context.Context, repo, branch string) (string, error) {
		assert.Equal(t, "mas-bandwidth/nova-tools", repo)
		if branch == "sprint/off.w1.g1.e15" {
			return other, nil
		}
		return head, nil
	}
	on, off, dead, limit := workCard("on.w1", "working"), workCard("off.w1", "working"), workCard("dead.w1", "working"), workCard("limit.w1", "working")
	for _, c := range []*HeldCard{&on, &off, &dead, &limit} {
		c.Repo = "mas-bandwidth/nova-tools"
		inboxJob(t, dir, c.Job, c.Brief)
	}
	outboxReport(t, dir, on.Job, "Verdict: LAND\nHead: "+head+"\n\nOn the tip.\n")
	outboxReport(t, dir, off.Job, "Verdict: LAND\nHead: "+head+"\n\nNot pushed.\n")
	// the runner's runs wrote RESULT.md and no REPORT.md: no lane of the daemon takes them
	for _, job := range []string{dead.Job, limit.Job} {
		require.NoError(t, os.MkdirAll(filepath.Join(dir, "outbox", job), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "outbox", job, "RESULT.md"), []byte("done\n"), 0o644))
	}
	end := "2026-10-06 07:10:00 AM END dead.w1~15 model=m exit=1 wall=600s report=no"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "runner.log"), []byte(strings.Join([]string{
		"2026-10-06 07:00:00 AM START dead.w1~15 tier=heavy model=m", end,
		"2026-10-06 07:00:00 AM START limit.w1~15 tier=heavy model=m",
		"2026-10-06 07:01:00 AM LIMIT limit.w1~15 model=m until=later: limit",
		"2026-10-06 07:01:00 AM END limit.w1~15 model=m exit=1 wall=60s report=no",
	}, "\n")+"\n"), 0o644))
	row.set(on, off, dead, limit)

	r.run(t, 3)

	got := map[string][]string{}
	for _, argv := range f.got() {
		got[argv[3]] = argv
	}
	require.Len(t, got, 2, "the LAND on origin's tip and the dead lane, once each: %v", f.got())
	assert.Equal(t, []string{"finish", "--as", "friend.bob", "on.w1@1", "--epoch", "15", "--head", head, "--branch", "sprint/on.w1.g1.e15", "--report", "friend bob LAND: On the tip."}, got["on.w1@1"])
	d := got["dead.w1@1"]
	require.Len(t, d, 11)
	assert.Equal(t, []string{"finish", "--as", "friend.bob", "dead.w1@1", "--epoch", "15", "--failed", "--branch", "sprint/dead.w1.g1.e15", "--report"}, d[:10])
	assert.Contains(t, d[10], "friend bob FAIL: Verdict: FAIL nova-friend of bob: the runner ended job dead.w1~15 with no report, and no run of it is live: "+end)
	report, err := os.ReadFile(filepath.Join(dir, "outbox", dead.Job, "REPORT.md"))
	require.NoError(t, err)
	assert.Equal(t, DeadLaneReport("bob", dead.Job, end), string(report))
	_, err = os.Stat(filepath.Join(dir, "outbox", limit.Job, "REPORT.md"))
	assert.True(t, os.IsNotExist(err), "a run stopped at its usage limit is run again, never dead")
	said := strings.Join(r.records, "\n")
	assert.Contains(t, said, "outbox: left outbox/off.w1~15/REPORT.md: Head "+head+" is not origin's tip of sprint/off.w1.g1.e15, "+other)
}

func TestRunnerEndedReadsTheJobsLastEventAsCollectDoes(t *testing.T) {
	t.Parallel()
	for log, want := range map[string]bool{
		"": false,
		"t START j~1 x\nt END j~1 exit=0 report=Verdict: LAND":     false,
		"t START j~1 x\nt END j~1 exit=0 report=no":                true,
		"t START j~1 x\nt END j~1 exit=0 report=no\nt START j~1 x": false,
		"t START j~1 x\nt LIMIT j~1 x\nt END j~1 exit=1 report=no": false,
		"t START j~10 x\nt END j~10 exit=1 report=no":              false,
	} {
		_, dead := RunnerEnded(log, "j~1")
		assert.Equal(t, want, dead, "%q", log)
	}
}
