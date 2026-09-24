package pulse

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Standing probe for issue #233: a worker-writable RESULT.md that is a symlink is not a
// result. harvest still os.ReadFile it, follows the link, and folds the file outside the
// job as a published report. These tests go red if harvest treats the planted path as a
// normal result (follow). The FIFO half lives in planted_result_unix_test.go so the
// package still builds on Windows.

const plantedResultBody = "RESULT plant sha=aaa\nDONE\nBRANCH rowan/exfil-233\nREPO owner/repo\n"

func skipWindowsPlant(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("windows: no symlink privilege / no FIFO")
	}
}

func plantResultSymlink(t *testing.T, job, body string) string {
	t.Helper()
	skipWindowsPlant(t)
	outside := filepath.Join(filepath.Dir(job), "outside-the-wall")
	if err := os.WriteFile(outside, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(job, "RESULT.md")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("this filesystem will not make a symlink: %v", err)
	}
	return outside
}

// Harvest must not follow a symlink planted at a job's RESULT.md and score it done.
func TestHarvestDoesNotTreatAPlantedSymlinkAsADoneResult(t *testing.T) {
	root, specs, arglog := setupPulse(t)
	fakeGit(t, specs, arglog)
	fakeGH(t, specs, arglog, "https://forge.invalid/owner/repo/pull/233")

	addCard(t, root, "plant", "1", "flash", "RESULT plant sha=aaa", "")
	job := filepath.Join(root, "1", "jobs", "plant")
	outside := plantResultSymlink(t, job, plantedResultBody)

	out, errs := runHarvest(t, root)
	combined := out + errs
	if strings.Contains(combined, "done=1") {
		t.Fatalf("harvest scored a planted symlink at RESULT.md as a done result:\n%s", combined)
	}
	if strings.Contains(combined, "rowan/exfil-233") {
		t.Fatalf("harvest followed the planted symlink and folded the outside branch:\n%s", combined)
	}
	if strings.Contains(combined, "exfil-233") {
		t.Fatalf("harvest followed the planted symlink:\n%s", combined)
	}
	raw, err := os.ReadFile(outside)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != plantedResultBody {
		t.Fatalf("the file outside the job was rewritten through the link: %q", string(raw))
	}
	for _, l := range arglogLines(t, arglog) {
		if strings.HasPrefix(l, "git push") || strings.HasPrefix(l, "gh pr") {
			t.Fatalf("harvest pushed a planted symlink as a result: %s", l)
		}
	}
}

// Harvest --working must not follow a symlink planted at a job's RESULT.md.
func TestHarvestWorkingDoesNotFollowAPlantedSymlinkAtResult(t *testing.T) {
	working := t.TempDir()
	specs := fakePATH(t)
	arglog := filepath.Join(working, "argv.log")
	fakeTool(t, specs, "git", fakeSpec{Log: arglog, Rules: []fakeRule{
		{Arg: 1, Equals: "log", Stdout: "0123456789abcdef0123456789abcdef01234567 2026-09-17T10:00:00+00:00"},
		{Arg: 1, Equals: "ls-remote", Stdout: "0123456789abcdef0123456789abcdef01234567\trefs/heads/rowan/exfil-233"},
		originRule("o/r"),
	}})
	fakeTool(t, specs, "gh", fakeSpec{Log: arglog, Rules: []fakeRule{
		{Arg: 2, Equals: "list", Stdout: "[]"},
		{Arg: 2, Equals: "create", Stdout: "https://example.invalid/o/r/pull/233"},
	}})
	job := wkJob(t, working, "g-plant", "plant", "")
	outside := plantResultSymlink(t, job, wkResult("plant", "rowan/exfil-233", "o/r"))

	out, errs, _ := wkRun(t, HarvestInput{Working: working, Base: "0000000000000000000000000000000000000000", Max: 20})
	combined := out + errs
	if strings.Contains(combined, "rowan/exfil-233") {
		t.Fatalf("harvest --working followed the planted symlink and folded the outside branch:\n%s", combined)
	}
	if strings.Contains(combined, "class=fixed") {
		t.Fatalf("harvest --working treated a planted symlink as a fixed result:\n%s", combined)
	}
	raw, err := os.ReadFile(outside)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "rowan/exfil-233") {
		t.Fatalf("the file outside the job was rewritten through the link: %q", string(raw))
	}
	for _, l := range arglogLines(t, arglog) {
		if strings.HasPrefix(l, "git push") || strings.HasPrefix(l, "gh pr") {
			t.Fatalf("harvest --working pushed a planted symlink as a result: %s", l)
		}
	}
}
