package pulse

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The working layout: a bench leaves jobs under
// <working>/tmp/<guid>-<label>/jobs/<label>, beside the swarm roots harvest
// already folds. Every fixture here fakes the network (gh, git), the bench and
// the clock; nothing reaches a network.

// wkJob writes one job's RESULT.md and returns its directory. uuidLabel is the
// <guid>-<label> directory the bench made; label is the job directory.
func wkJob(t *testing.T, working, guidLabel, label, result string) string {
	t.Helper()
	job := filepath.Join(working, "tmp", guidLabel, "jobs", label)
	if err := os.MkdirAll(job, 0o755); err != nil {
		t.Fatal(err)
	}
	if result != "" {
		if err := os.WriteFile(filepath.Join(job, "RESULT.md"), []byte(result), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return job
}

// wkOldJob writes one job under an old swarm root: <root>/<slot>/jobs/<label>.
func wkOldJob(t *testing.T, root, slot, label, result string) string {
	t.Helper()
	job := filepath.Join(root, slot, "jobs", label)
	if err := os.MkdirAll(job, 0o755); err != nil {
		t.Fatal(err)
	}
	if result != "" {
		if err := os.WriteFile(filepath.Join(job, "RESULT.md"), []byte(result), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return job
}

// wkResult is a RESULT.md whose line 1 is the contract and whose BRANCH and REPO
// lines are the two harvest reads.
func wkResult(label, branch, repo string) string {
	return "RESULT " + label + " sha=aaa\nDONE\nBRANCH " + branch + "\nREPO " + repo + "\n"
}

// wkPerJobGit teaches the fake git a per-clone spec: the fake reads .fake/git.json
// out of its working directory, which is the job's own clone, so two jobs in one
// run answer differently with identical argv.
func wkPerJobGit(t *testing.T, job string, s fakeSpec) {
	t.Helper()
	dir := filepath.Join(job, ".fake")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	fakeTool(t, dir, "git", s)
}

// wkRun drives HarvestWorking with the fixture clock.
func wkRun(t *testing.T, in HarvestInput) (out, errs string, code int) {
	t.Helper()
	var o, e bytes.Buffer
	in.Stdout, in.Stderr = &o, &e
	if in.Now == nil {
		in.Now = func() time.Time { return time.Unix(0, 0).UTC() }
	}
	code = HarvestWorking(in)
	return o.String(), e.String(), code
}

func wkHasLine(t *testing.T, out, sub string) {
	t.Helper()
	for _, l := range strings.Split(out, "\n") {
		if strings.Contains(l, sub) {
			return
		}
	}
	t.Fatalf("no line contains %q in:\n%s", sub, out)
}

// rule 1: a fixture working root of <guid>-<label> jobs plus a fixture old root
// yields one HARVEST JOB line per job.
func TestHarvestWorkingReadsTheGuidLayout(t *testing.T) {
	working := t.TempDir()
	old := t.TempDir()
	specs := fakePATH(t)
	arglog := filepath.Join(working, "argv.log")
	fakeTool(t, specs, "git", fakeSpec{Log: arglog, Rules: []fakeRule{
		{Arg: 1, Equals: "log", Stdout: "0123456789abcdef0123456789abcdef01234567 2026-09-17T10:00:00+00:00"},
		{Arg: 1, Equals: "ls-remote", Stdout: "0123456789abcdef0123456789abcdef01234567\trefs/heads/rowan/a"},
	}})
	fakeTool(t, specs, "gh", fakeSpec{Log: arglog, Rules: []fakeRule{
		{Arg: 2, Equals: "list", Stdout: "[]"},
		{Arg: 2, Equals: "create", Stdout: "https://github.com/o/r/pull/1"},
	}})
	wkJob(t, working, "g1-a", "a", wkResult("a", "rowan/a", "o/r"))
	wkJob(t, working, "g2-b", "b", wkResult("b", "rowan/b", "o/r"))
	wkOldJob(t, old, "1", "c", wkResult("c", "rowan/c", "o/r"))

	out, _, _ := wkRun(t, HarvestInput{Working: working, Roots: old, Base: "0000000000000000000000000000000000000000", Max: 20})
	if n := strings.Count(out, "HARVEST JOB "); n != 3 {
		t.Fatalf("want one HARVEST JOB line per job (3), got %d:\n%s", n, out)
	}
	wkHasLine(t, out, "HARVEST OK jobs=3")
	wkHasLine(t, out, "label=a ")
	wkHasLine(t, out, "label=b ")
	wkHasLine(t, out, "label=c ")
}

// rule 10: no --working and no --roots is `refusing to guess`, exit 2, and the
// fixture git log is empty.
func TestHarvestWorkingRefusesWithoutAFlag(t *testing.T) {
	specs := fakePATH(t)
	arglog := filepath.Join(t.TempDir(), "argv.log")
	fakeTool(t, specs, "git", fakeSpec{Log: arglog})
	out, errs, code := wkRun(t, HarvestInput{Base: "0000000000000000000000000000000000000000", Max: 20})
	if code != 2 {
		t.Fatalf("exit = %d, want 2; out=%s", code, out)
	}
	if !strings.Contains(errs, "HARVEST REFUSED: refusing to guess (name --working or --roots)") {
		t.Fatalf("stderr = %q", errs)
	}
	if lines := arglogLines(t, arglog); len(lines) != 0 {
		t.Fatalf("a refusal ran git: %v", lines)
	}
}

// rule 5: a fixture job whose HEAD equals base prints the NO-COMMIT line as class
// no-change, and the fixture git log records no push.
func TestHarvestWorkingNoCommitIsSkipped(t *testing.T) {
	working := t.TempDir()
	specs := fakePATH(t)
	arglog := filepath.Join(working, "argv.log")
	fakeTool(t, specs, "git", fakeSpec{Log: arglog})
	fakeTool(t, specs, "gh", fakeSpec{Log: arglog, Rules: []fakeRule{{Arg: 2, Equals: "list", Stdout: "[]"}}})
	job := wkJob(t, working, "g-n", "n", wkResult("n", "rowan/n", "o/r"))
	_ = job
	out, _, _ := wkRun(t, HarvestInput{Working: working, Base: "0123456789ab", Max: 20})
	wkHasLine(t, out, "HARVEST JOB label=n class=no-change base=0123456789ab branch=- pr=- commit=-")
	for _, l := range arglogLines(t, arglog) {
		if strings.HasPrefix(l, "git push") {
			t.Fatalf("a no-change job pushed: %s", l)
		}
	}
}

// rule 7: the fixture git log records ls-remote then push --force-with-lease, and
// a fixture remote whose sha moved is failed, no push.
func TestHarvestWorkingLeaseComesFromLsRemote(t *testing.T) {
	working := t.TempDir()
	specs := fakePATH(t)
	arglog := filepath.Join(working, "argv.log")
	fakeTool(t, specs, "git", fakeSpec{Log: arglog, Rules: []fakeRule{
		{Arg: 1, Equals: "log", Stdout: "aaaa000000000000000000000000000000000000 2026-09-17T10:00:00+00:00"},
		{Arg: 1, Equals: "ls-remote", Stdout: "bbbb000000000000000000000000000000000000\trefs/heads/rowan/y"},
	}})
	fakeTool(t, specs, "gh", fakeSpec{Log: arglog, Rules: []fakeRule{
		{Arg: 2, Equals: "list", Stdout: `[{"number":9,"headRefName":"rowan/x","headRefOid":"9999","state":"OPEN","title":"t"}]`},
		{Arg: 2, Equals: "create", Stdout: "https://github.com/o/r/pull/7"},
	}})
	wkJob(t, working, "g-x", "x", wkResult("x", "rowan/x", "o/r"))
	wkJob(t, working, "g-y", "y", wkResult("y", "rowan/y", "o/r"))

	out, _, _ := wkRun(t, HarvestInput{Working: working, Base: "0123456789ab", Max: 20})
	wkHasLine(t, out, "label=x class=failed")
	wkHasLine(t, out, "label=y class=fixed")
	lsThenPush := false
	leased := false
	seenLS := false
	for _, l := range arglogLines(t, arglog) {
		if strings.HasPrefix(l, "git ls-remote") {
			seenLS = true
		}
		if strings.HasPrefix(l, "git push") {
			if strings.Contains(l, "rowan/y") {
				if !seenLS {
					t.Fatalf("push before ls-remote: %s", l)
				}
				lsThenPush = true
				if strings.Contains(l, "--force-with-lease=refs/heads/rowan/y:bbbb000000000000000000000000000000000000") {
					leased = true
				}
			}
			if strings.Contains(l, "rowan/x") {
				t.Fatalf("a moved lease was pushed: %s", l)
			}
		}
	}
	if !lsThenPush || !leased {
		t.Fatalf("want ls-remote then leased push (lsThenPush=%v leased=%v):\n%s", lsThenPush, leased, strings.Join(arglogLines(t, arglog), "\n"))
	}
}

// rule 6: fixture jobs on feature/x and on a commit before --since (fixture clock)
// are class off-branch, with a reason and no push.
func TestHarvestWorkingOffBranchAndBeforeSession(t *testing.T) {
	working := t.TempDir()
	specs := fakePATH(t)
	arglog := filepath.Join(working, "argv.log")
	fakeTool(t, specs, "git", fakeSpec{Log: arglog, Rules: []fakeRule{
		{Arg: 1, Equals: "log", Stdout: "cccc000000000000000000000000000000000000 2026-09-01T10:00:00+00:00"},
		{Arg: 1, Equals: "ls-remote", Stdout: "cccc000000000000000000000000000000000000\trefs/heads/rowan/s"},
	}})
	fakeTool(t, specs, "gh", fakeSpec{Log: arglog, Rules: []fakeRule{{Arg: 2, Equals: "list", Stdout: "[]"}}})
	wkJob(t, working, "g-f", "f", wkResult("f", "feature/x", "o/r"))
	wkJob(t, working, "g-s", "s", wkResult("s", "rowan/s", "o/r"))

	now := func() time.Time { return time.Date(2026, 9, 17, 12, 0, 0, 0, time.UTC) }
	out, _, _ := wkRun(t, HarvestInput{Working: working, Base: "0123456789ab", SinceStamp: "2026-09-15T00:00:00Z", Max: 20, Now: now})
	wkHasLine(t, out, "label=f class=off-branch branch=feature/x reason=not-rowan")
	wkHasLine(t, out, "label=s class=off-branch branch=rowan/s reason=before-session")
	for _, l := range arglogLines(t, arglog) {
		if strings.HasPrefix(l, "git push") {
			t.Fatalf("an off-branch job pushed: %s", l)
		}
	}
}

// rule 3: a fixture gh with two similar-titled PRs matches pr=<n> only on the
// exact head branch and leaves the other pr=-.
func TestHarvestWorkingMatchesPRByExactHead(t *testing.T) {
	working := t.TempDir()
	specs := fakePATH(t)
	arglog := filepath.Join(working, "argv.log")
	fakeTool(t, specs, "git", fakeSpec{Log: arglog, Rules: []fakeRule{
		{Arg: 1, Equals: "log", Stdout: "dddd000000000000000000000000000000000000 2026-09-17T10:00:00+00:00"},
	}})
	fakeTool(t, specs, "gh", fakeSpec{Log: arglog, Rules: []fakeRule{
		{Arg: 2, Equals: "list", Stdout: `[
			{"number":42,"headRefName":"rowan/x","headRefOid":"dddd000000000000000000000000000000000000","state":"OPEN","title":"same title"},
			{"number":43,"headRefName":"rowan/x-extra","headRefOid":"eeee000000000000000000000000000000000000","state":"OPEN","title":"same title"}
		]`},
	}})
	wkJob(t, working, "g-x", "x", wkResult("x", "rowan/x", "o/r"))

	out, _, _ := wkRun(t, HarvestInput{Working: working, Base: "0123456789ab", Max: 20})
	wkHasLine(t, out, "label=x class=already-fixed")
	wkHasLine(t, out, "pr=42")
	if strings.Contains(out, "pr=43") {
		t.Fatalf("the near-miss PR matched:\n%s", out)
	}
}

// rule 8: after a run every fixture job holds .harvested and a second run prints
// no line for it (fixture clock and fixture git).
func TestHarvestWorkingMarksHarvested(t *testing.T) {
	working := t.TempDir()
	specs := fakePATH(t)
	arglog := filepath.Join(working, "argv.log")
	fakeTool(t, specs, "git", fakeSpec{Log: arglog, Rules: []fakeRule{
		{Arg: 1, Equals: "log", Stdout: "ffff000000000000000000000000000000000000 2026-09-17T10:00:00+00:00"},
		{Arg: 1, Equals: "ls-remote", Stdout: "ffff000000000000000000000000000000000000\trefs/heads/rowan/h"},
	}})
	fakeTool(t, specs, "gh", fakeSpec{Log: arglog, Rules: []fakeRule{
		{Arg: 2, Equals: "list", Stdout: "[]"},
		{Arg: 2, Equals: "create", Stdout: "https://github.com/o/r/pull/3"},
	}})
	job := wkJob(t, working, "g-h", "h", wkResult("h", "rowan/h", "o/r"))

	first, _, _ := wkRun(t, HarvestInput{Working: working, Base: "0123456789ab", Max: 20})
	if !strings.Contains(first, "HARVEST JOB label=h") {
		t.Fatalf("first run printed no job line:\n%s", first)
	}
	if _, err := os.Stat(filepath.Join(job, ".harvested")); err != nil {
		t.Fatalf("no .harvested marker: %v", err)
	}
	second, _, _ := wkRun(t, HarvestInput{Working: working, Base: "0123456789ab", Max: 20})
	if strings.Contains(second, "HARVEST JOB label=h") {
		t.Fatalf("a harvested job printed again:\n%s", second)
	}
}

// rule 4: one fixture job per class prints exactly the five classes and an
// HARVEST OK whose counts sum.
func TestHarvestWorkingClassesAreTheFive(t *testing.T) {
	working := t.TempDir()
	specs := fakePATH(t)
	arglog := filepath.Join(working, "argv.log")
	fakeTool(t, specs, "git", fakeSpec{Log: arglog, Rules: []fakeRule{
		{Arg: 1, Equals: "log", Stdout: "abcd000000000000000000000000000000000000 2026-09-17T10:00:00+00:00"},
		{Arg: 1, Equals: "ls-remote", Stdout: "abcd000000000000000000000000000000000000\trefs/heads/rowan/f"},
	}})
	fakeTool(t, specs, "gh", fakeSpec{Log: arglog, Rules: []fakeRule{
		{Arg: 2, Equals: "list", Stdout: `[
			{"number":1,"headRefName":"rowan/a","headRefOid":"abcd000000000000000000000000000000000000","state":"OPEN","title":"t"},
			{"number":2,"headRefName":"rowan/x","headRefOid":"abcd000000000000000000000000000000000000","state":"MERGED","title":"t"}
		]`},
		{Arg: 2, Equals: "create", Stdout: "https://github.com/o/r/pull/5"},
	}})
	wkJob(t, working, "g-f", "f", wkResult("f", "rowan/f", "o/r"))
	wkJob(t, working, "g-a", "a", wkResult("a", "rowan/a", "o/r"))
	nochange := wkJob(t, working, "g-n", "n", wkResult("n", "rowan/n", "o/r"))
	wkPerJobGit(t, nochange, fakeSpec{Log: arglog})
	wkJob(t, working, "g-o", "o", wkResult("o", "feature/o", "o/r"))
	wkJob(t, working, "g-x", "x", wkResult("x", "rowan/x", "o/r"))

	out, _, _ := wkRun(t, HarvestInput{Working: working, Base: "0123456789ab", Max: 20})
	for _, class := range []string{"class=fixed", "class=already-fixed", "class=no-change", "class=off-branch", "class=failed"} {
		wkHasLine(t, out, class)
	}
	wkHasLine(t, out, "HARVEST OK jobs=5 fixed=1 already-fixed=1 no-change=1 off-branch=1 failed=1")
}

// rule 9: a fixture systemctl records daemon-reload and enable --now, both unit
// files carry the harvest flags, and an unknown action is exit 2 with one remedy.
func TestHarvestWorkingTimerInstall(t *testing.T) {
	working := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	specs := fakePATH(t)
	arglog := filepath.Join(working, "argv.log")
	fakeTool(t, specs, "systemctl", fakeSpec{Log: arglog})
	fakeTool(t, specs, "git", fakeSpec{Log: arglog})

	_, _, code := wkRun(t, HarvestInput{Working: working, Root: "x", Base: "0123456789ab", SinceStamp: "2026-09-15T00:00:00Z", Timer: "install", Max: 20})
	if code != 0 {
		t.Fatalf("timer install exit = %d, want 0", code)
	}
	log := string(mustRead(t, arglog))
	if !strings.Contains(log, "systemctl --user daemon-reload") || !strings.Contains(log, "systemctl --user enable --now nova-pulse-harvest.timer") {
		t.Fatalf("systemctl log = %q", log)
	}
	unitDir := filepath.Join(home, ".config", "systemd", "user")
	svc := string(mustRead(t, filepath.Join(unitDir, "nova-pulse-harvest.service")))
	timer := string(mustRead(t, filepath.Join(unitDir, "nova-pulse-harvest.timer")))
	for _, f := range []string{svc, timer} {
		if !strings.Contains(f, "--working "+working) {
			t.Fatalf("unit file lacks the harvest flags:\n%s", f)
		}
	}
	if !strings.Contains(string(mustRead(t, filepath.Join(working, "harvest.log"))), "HARVEST OK") {
		t.Fatalf("harvest.log has no run line")
	}

	_, errs, code := wkRun(t, HarvestInput{Working: working, Timer: "reload", Max: 20})
	if code != 2 || !strings.Contains(errs, "HARVEST REFUSED timer=reload (only install)") {
		t.Fatalf("unknown timer action: code=%d errs=%q", code, errs)
	}
}

// rule 11: 200 fixture jobs print at most --max lines plus one HARVEST MORE,
// every value one token.
func TestHarvestWorkingOutputIsBounded(t *testing.T) {
	working := t.TempDir()
	specs := fakePATH(t)
	arglog := filepath.Join(working, "argv.log")
	fakeTool(t, specs, "git", fakeSpec{Log: arglog})
	fakeTool(t, specs, "gh", fakeSpec{Log: arglog, Rules: []fakeRule{{Arg: 2, Equals: "list", Stdout: "[]"}}})
	for i := 0; i < 200; i++ {
		wkJob(t, working, "g-j", "j"+itoa(i), wkResult("j"+itoa(i), "rowan/j"+itoa(i), "o/r"))
	}
	out, _, _ := wkRun(t, HarvestInput{Working: working, Base: "0123456789ab", Max: 20})
	if n := strings.Count(out, "HARVEST JOB "); n > 20 {
		t.Fatalf("printed %d job lines, want at most 20:\n%s", n, out)
	}
	if !strings.Contains(out, "HARVEST MORE") {
		t.Fatalf("no HARVEST MORE line:\n%s", out)
	}
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, "HARVEST JOB ") && strings.Contains(l, "  ") {
			t.Fatalf("a value held whitespace: %q", l)
		}
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return raw
}
