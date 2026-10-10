package friend

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The lane hands the brief by absolute path, and a no-report exit is a harness fault
// (the-lane-hands-the-brief-by-absolute-path-bb). On 2026-10-07 five lanes of a flash friend
// ended "File not found: Volumes/nova/ai/<friend>/working/inbox/<card>/BRIEF.md": the model
// dropped the leading slash, read the path relative to her working directory, wrote no report,
// and every card walked toward the brief-is-wrong bound for $0.00.

// strayErr is the harness's first error line of 2026-10-07, the path with its slash dropped.
const strayErr = "File not found: Volumes/nova/ai/bob/working/inbox/c1~15/BRIEF.md"

// A relative brief path is made absolute: in the job the lane hands, in the prompt, and in
// the command the harness runs (its working directory the card's job directory, and the
// brief's text inline); a path that cannot be made absolute refuses the lane with a REFUSED
// line naming it.
func TestARelativeBriefPathBecomesAbsoluteInTheCommandAndThePrompt(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	abs := func(p string) (string, error) { // filepath.Abs, against a known working directory
		if filepath.IsAbs(p) {
			return p, nil
		}
		return filepath.Join(root, p), nil
	}
	rel := Card{ID: "c1", Brief: filepath.Join("bob", "inbox", "c1~15", "BRIEF.md"), Outbox: filepath.Join("bob", "outbox", "c1~15")}
	require.NoError(t, os.MkdirAll(filepath.Join(root, "bob", "inbox", "c1~15"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, rel.Brief), []byte("STATUS: nova-sprint card c1\nDo the one thing.\n"), 0o644))

	job, err := LaneJobOf("bob", rel, abs)
	require.NoError(t, err)
	brief, outbox, jobDir := filepath.Join(root, rel.Brief), filepath.Join(root, rel.Outbox), filepath.Join(root, "bob", "jobs", "c1~15")
	assert.Equal(t, LaneJob{Dir: jobDir, Card: Card{ID: "c1", Brief: brief, Outbox: outbox}}, job)
	raw, err := os.ReadFile(job.Card.Brief)
	require.NoError(t, err)
	job.Brief = string(raw)

	text := CardText(job, 1, 2, "nova-bus send ...", "", "", "", nil)
	assert.Contains(t, text, "Its brief is "+brief+", and its whole text is below")
	assert.Contains(t, text, "2. Write "+outbox+"/REPORT.md and "+outbox+"/RESULT.md")
	assert.Contains(t, text, "Your working directory is the card's job directory, "+jobDir+".")
	assert.Contains(t, text, "THE BRIEF ("+brief+"):\n\nSTATUS: nova-sprint card c1\nDo the one thing.\nEND OF THE BRIEF\n", "the brief rides inline")
	assert.NotContains(t, strings.ReplaceAll(text, root, ""), " "+rel.Brief, "no path is named relative")

	// the command: opencode runs in the job directory with the prompt naming every path absolute
	var mu sync.Mutex
	var dirs []string
	var prompts []string
	run := func(_ context.Context, dir, _ string, args []string, _ string) (string, int, error) {
		mu.Lock()
		defer mu.Unlock()
		dirs, prompts = append(dirs, dir), append(prompts, args[len(args)-1])
		return "Reading the brief\n" + strayErr + "\nDone.\n", 0, nil
	}
	o := &OpenCode{Dir: filepath.Join(root, "bob"), Run: run}
	lt, err := o.DeliverTo(WithLaneDir(LaneContext(t.Context()), job.Dir), "ses_1", text)
	require.NoError(t, err)
	assert.Equal(t, []string{jobDir}, dirs, "the harness runs in the card's job directory")
	assert.Equal(t, []string{text}, prompts)
	assert.Equal(t, strayErr, lt.FirstError, "the harness's first error line is read off its output")
	_, err = o.DeliverTo(t.Context(), "ses_1", "no lane")
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(root, "bob"), dirs[1], "a run no lane names stays in the friend's directory")

	// the card runner too: claude -p runs in the job directory, and a run with no report says so typed
	var claudeDir string
	cl := &Claude{Stub: Stub{Harness: "claude"}, Friend: "bob", Dir: filepath.Join(root, "bob"), ConfigDir: func() string { return "/accounts/a" },
		Run: func(_ context.Context, dir, _ string, _ []string, _ string) (string, int, error) {
			claudeDir = dir
			return "Error: " + strayErr + "\n", 0, nil
		}}
	lt, err = cl.RunCard(WithLaneDir(LaneContext(t.Context()), job.Dir), job.Card)
	assert.Equal(t, jobDir, claudeDir)
	var none NoReport
	require.ErrorAs(t, err, &none)
	assert.Equal(t, "claude -p exited 0 and "+outbox+" holds no REPORT.md and no RESULT.md", err.Error())
	assert.Equal(t, "Error: "+strayErr, lt.FirstError)

	// with the real filepath.Abs a relative path comes back absolute
	real, err := LaneJobOf("bob", rel, filepath.Abs)
	require.NoError(t, err)
	for _, p := range []string{real.Dir, real.Card.Brief, real.Card.Outbox} {
		assert.True(t, filepath.IsAbs(p), p)
	}

	// a path that cannot be made absolute refuses the lane, naming it
	for name, bad := range map[string]func(string) (string, error){
		"abs fails":          func(string) (string, error) { return "", errors.New("getwd: no such file or directory") },
		"abs stays relative": func(p string) (string, error) { return p, nil },
	} {
		_, err := LaneJobOf("bob", rel, bad)
		require.Error(t, err, name)
		assert.True(t, strings.HasPrefix(err.Error(), "REFUSED lane path not absolute: bob: "), "%s: %v", name, err)
		assert.NotContains(t, err.Error(), "\n", "one line")
	}

	// a report written under the outbox's relative spelling, inside the job, is moved home
	stray := filepath.Join(jobDir, strings.TrimPrefix(outbox, "/"))
	require.NoError(t, os.MkdirAll(stray, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(stray, "REPORT.md"), []byte("Verdict: LAND\n"), 0o644))
	assert.Equal(t, []string{filepath.Join(outbox, "REPORT.md")}, RescueStray(job))
	assert.FileExists(t, filepath.Join(outbox, "REPORT.md"))
	assert.Empty(t, RescueStray(job), "nothing is moved twice")
}

// faultHarness is a lanes harness whose card turns exit 0 with no report, saying the stray
// path's error first, each turn's working directory kept.
type faultHarness struct {
	*lanesHarness
	dirs []string
}

func (h *faultHarness) DeliverTo(ctx context.Context, session, text string) (LaneTurn, error) {
	lt, err := h.lanesHarness.DeliverTo(ctx, session, text)
	h.mu.Lock()
	h.dirs = append(h.dirs, LaneDirOf(ctx, ""))
	h.mu.Unlock()
	lt.FirstError = strayErr
	return lt, err
}

// A lane whose run exits 0 and leaves no report is a harness fault, said with the harness's
// first error line: the card stays in the lane's hand, its attempt not counted, no failed
// REPORT.md written and no finish sent, so no reader ever finds it; three of the fault within
// ten minutes on her row mark her down once, with the reason and until fifteen minutes on, and
// the seat gets one judgment, not one per card.
func TestANoReportExitIsAHarnessFaultAndThreeMarkTheRowDownOnce(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		dir := cardDirFixture(t, [][2]string{{"c1", "queued"}, {"c2", "queued"}}, []string{"c1", "c2"}, nil)
		h := &faultHarness{lanesHarness: &lanesHarness{dir: dir, finish: map[string]bool{}, active: map[string]int{}}}
		r, state := laneRig(t, h.lanesHarness, 1) // one lane: the faults come one at a time
		r.d.Deliver = h
		var finishes [][]string
		r.d.Finish = func(_ context.Context, argv []string) error { finishes = append(finishes, argv); return nil }
		type down struct {
			until  time.Time
			reason string
		}
		var downs []down
		r.d.FaultDown = func(until time.Time, reason string) { downs = append(downs, down{until, reason}) }
		r.run(t, 40)

		reason := "harness-fault: no report; first error: " + strayErr
		records := strings.Join(r.records, "\n")
		assert.Contains(t, records, `card=kept turn=0/2 reason="`+reason+`"`, records)
		assert.NotContains(t, records, "card=again", "a harness fault is no attempt")
		assert.NotContains(t, records, "card=set_aside")
		assert.NotContains(t, records, "finish=failed")
		assert.Empty(t, finishes, "no failed finish goes to the sprint server")
		assert.Empty(t, state.GivenUp)
		for _, id := range []string{"c1", "c2"} {
			assert.NoFileExists(t, filepath.Join(dir, "outbox", id+"~15", "REPORT.md"), "no report is written for the worker")
		}
		assert.Equal(t, FaultRepeats, strings.Count(records, "card=kept"), "the third fault holds the lanes: no fourth run")

		require.Len(t, downs, 1, "one down, not one per card")
		assert.Equal(t, reason, downs[0].reason)
		at := downs[0].until.Add(-FaultDownFor)
		assert.True(t, at.After(t0), "until is fifteen minutes after the third fault")
		assert.Contains(t, records, "harness fault: 3 alike within 10m0s: her row down until "+downs[0].until.UTC().Format(time.RFC3339)+", her lanes held: "+reason)
		got := r.adaGot(t)
		var judgments []string
		for _, g := range got {
			if strings.HasPrefix(g, "friend bob down until ") {
				judgments = append(judgments, g)
			}
		}
		require.Len(t, judgments, 1, "%v", got)
		assert.Contains(t, judgments[0], reason)
		assert.Contains(t, r.last().Lanes, ":paused", "her lanes are held until the down passes")

		h.mu.Lock()
		defer h.mu.Unlock()
		require.NotEmpty(t, h.dirs)
		for _, d := range h.dirs {
			assert.True(t, filepath.IsAbs(d) && strings.HasPrefix(d, filepath.Join(dir, "jobs")+string(filepath.Separator)), "each turn runs in its card's job directory: %s", d)
		}
		for _, text := range h.texts {
			assert.Contains(t, text, "THE BRIEF ("+filepath.Join(dir, "inbox"), "the brief rides inline")
		}
	})
}

// The bound itself: the same fault three times within ten minutes is one down until fifteen
// minutes on; two, or three spread past ten minutes, or another fault, are none; a fault while
// the down stands adds nothing; once it has passed the count starts again.
func TestThreeFaultsInTenMinutesMarkTheRowDownOnceWithUntil(t *testing.T) {
	t.Parallel()
	at := func(m int) time.Time { return t0.Add(time.Duration(m) * time.Minute) }
	var w FaultWatch
	for _, m := range []int{0, 4} {
		_, down := w.Observe(HarnessFaultNoReport, at(m))
		assert.False(t, down, "fault at %dm", m)
	}
	_, down := w.Observe("harness-fault: other", at(5))
	assert.False(t, down, "another fault is its own count")
	until, down := w.Observe(HarnessFaultNoReport, at(9))
	require.True(t, down, "the third within ten minutes")
	assert.Equal(t, at(9).Add(FaultDownFor), until)
	_, down = w.Observe(HarnessFaultNoReport, at(10))
	assert.False(t, down, "the row is down already: once")
	_, down = w.Observe("harness-fault: other", at(11))
	assert.False(t, down, "any fault while down adds nothing")
	for _, m := range []int{25, 26} {
		_, down = w.Observe(HarnessFaultNoReport, at(m))
		assert.False(t, down, "after the down the count starts again: %dm", m)
	}
	until, down = w.Observe(HarnessFaultNoReport, at(27))
	assert.True(t, down)
	assert.Equal(t, at(27).Add(15*time.Minute), until)

	var spread FaultWatch
	for _, m := range []int{0, 6, 12, 18} {
		_, down := spread.Observe(HarnessFaultNoReport, at(m))
		assert.False(t, down, "three spread past ten minutes are none: %dm", m)
	}

	assert.Equal(t, "harness-fault: no report; first error: the harness printed no error line", FaultWords(HarnessFaultNoReport, ""))
	assert.Equal(t, strayErr, HarnessFirstError("\x1b[31m"+strayErr+"\x1b[0m\nlater error\n"))
	assert.Empty(t, HarnessFirstError("all is well\n"))
}
