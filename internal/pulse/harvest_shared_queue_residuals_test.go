package pulse

// Stella's three residual HOLDs on PR #1984, at head 5d4256a8 (#1950). Each one is her own
// witness, reproduced through the production path Harvest(HarvestInput):
//
//  1. a card whose rename succeeded and whose marker rename failed was reported `drained=1`
//     over a SPLIT PAIR: the card in one directory, its only record in another;
//  2. an omitted --session acted as a WILDCARD and drained another session's marker on the
//     same bench;
//  3. a nonempty listing is not proof that an absent job is gone. Her fixture, run under
//     /bin/sh: root A with a readable job, root B with a LIVE job under a `jobs` directory
//     at mode 000. The script exits 0, reports both roots ok, emits A's job and silently
//     omits B's -- and B's live card was then moved to failed on the strength of A's job.

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// --- Residual 1: the card and its marker move as a pair, or not at all ----------------

// oneFinishedCard is the fixture the pair tests share: one launched card of the caller's
// own, whose job finished on the bench, so the only thing standing between it and the done
// directory is the move itself.
func oneFinishedCard(t *testing.T, root string) (launched string, in HarvestInput) {
	t.Helper()
	launched = filepath.Join(root, "queue", "launched")
	writeSharedQueue(t, launched, []launchedCard{{"card-9601.md", "schema", "vision", "s-42"}})
	shell := &fakeShell{answer: func(bench, script string) (string, error) {
		if strings.Contains(script, "touch") {
			return "", nil
		}
		return benchJobListing("/home/gaffer/rowan-swarm-root/0/jobs/card-9601", []string{
			"RESULT card-9601 sha=abc", "BRANCH rowan/card-9601", "REPO mas-bandwidth/nova-tools",
			"SESSION s-42",
		}), nil
	}}
	in = benchHarvestInput(t, root, shell, &fakeForge{})
	in.Bench = "vision"
	in.Session = "s-42"
	in.Launched = launched
	return launched, in
}

// TestHarvestNeverSplitsACardFromItsMarkerWhenTheMarkerCannotMove: the marker moves FIRST,
// so a marker that cannot move leaves the card exactly where it is, with its record beside
// it. Nothing is counted drained, the reason is named, and the verb exits non-zero.
func TestHarvestNeverSplitsACardFromItsMarkerWhenTheMarkerCannotMove(t *testing.T) {
	root, specs, arglog := setupPulse(t)
	benchGit(t, specs, arglog, nil)
	launched, in := oneFinishedCard(t, root)
	before := listing(t, launched)

	// The injected failure: the marker's own rename fails, and nothing else does.
	swapRenameForDrain(t, func(from, to string) error {
		if strings.HasSuffix(from, ".launched") {
			return &os.LinkError{Op: "rename", Old: from, New: to, Err: os.ErrPermission}
		}
		return os.Rename(from, to)
	})

	code, out, _ := runBenchHarvest(t, in)
	if code == 0 {
		t.Errorf("a drain that could not complete exited 0:\n%s", out)
	}
	if !strings.Contains(out, "HARVEST DRAIN-FAIL card=card-9601.md") || !strings.Contains(out, "reason=marker-move") {
		t.Errorf("the failure is not reported with its reason:\n%s", out)
	}
	if !strings.Contains(out, "drained=0") {
		t.Errorf("a drain that did not complete was counted:\n%s", out)
	}
	assertNoSplitPair(t, root, launched, before)
}

// TestHarvestRollsTheMarkerBackWhenTheCardCannotMove is the recovery path: the marker has
// already moved when the card's own rename fails, so the marker is put back and the pair is
// recoverable in ONE place.
func TestHarvestRollsTheMarkerBackWhenTheCardCannotMove(t *testing.T) {
	root, specs, arglog := setupPulse(t)
	benchGit(t, specs, arglog, nil)
	launched, in := oneFinishedCard(t, root)
	before := listing(t, launched)

	// The injected failure: the CARD's rename fails. The marker's has already
	// succeeded, so only a rollback can keep the pair together.
	swapRenameForDrain(t, func(from, to string) error {
		if strings.HasSuffix(from, ".md") {
			return &os.LinkError{Op: "rename", Old: from, New: to, Err: os.ErrPermission}
		}
		return os.Rename(from, to)
	})

	code, out, _ := runBenchHarvest(t, in)
	if code == 0 {
		t.Errorf("a drain that could not complete exited 0:\n%s", out)
	}
	if !strings.Contains(out, "reason=card-move") {
		t.Errorf("the failure is not reported as the card's:\n%s", out)
	}
	if strings.Contains(out, "reason=rollback") {
		t.Errorf("the rollback itself failed, which it should not have here:\n%s", out)
	}
	if !strings.Contains(out, "drained=0") {
		t.Errorf("a drain that did not complete was counted:\n%s", out)
	}
	assertNoSplitPair(t, root, launched, before)
}

// TestHarvestRefusesToDrainOntoExistingEvidence: a destination that already holds either
// half of the pair is somebody's evidence, and a rename would overwrite it silently.
func TestHarvestRefusesToDrainOntoExistingEvidence(t *testing.T) {
	root, specs, arglog := setupPulse(t)
	benchGit(t, specs, arglog, nil)
	launched, in := oneFinishedCard(t, root)
	done := filepath.Join(root, "queue", "done")
	if err := os.MkdirAll(done, 0o755); err != nil {
		t.Fatal(err)
	}
	// An older drain of a card of the same name, whole: card AND record, as this verb
	// leaves them. Neither half may be overwritten.
	const older = "AN OLDER CARD OF THE SAME NAME\n"
	if err := os.WriteFile(filepath.Join(done, "card-9601.md"), []byte(older), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(done, "card-9601.md.launched"), []byte("lane=older\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	before := listing(t, launched)
	beforeDone := listing(t, done)

	code, out, _ := runBenchHarvest(t, in)
	if code == 0 {
		t.Errorf("a drain onto existing evidence exited 0:\n%s", out)
	}
	if !strings.Contains(out, "reason=destination-exists") {
		t.Errorf("the collision is not named:\n%s", out)
	}
	if got, _ := os.ReadFile(filepath.Join(done, "card-9601.md")); string(got) != older {
		t.Errorf("the harvest overwrote evidence it did not write: %q", got)
	}
	if diff := diffListing(beforeDone, listing(t, done)); len(diff) > 0 {
		t.Errorf("the destination directory was written into:\n%s", strings.Join(diff, "\n"))
	}
	assertNoSplitPair(t, root, launched, before)
}

// swapRenameForDrain installs a failing rename for the length of one test.
func swapRenameForDrain(t *testing.T, fn func(from, to string) error) {
	t.Helper()
	was := renameForDrain
	renameForDrain = fn
	t.Cleanup(func() { renameForDrain = was })
}

// assertNoSplitPair is the whole of residual 1: wherever the card ended up, its launch
// record is in the SAME directory, and a drain that did not complete left the launched
// directory byte-identical.
func assertNoSplitPair(t *testing.T, root, launched string, before map[string]string) {
	t.Helper()
	if diff := diffListing(before, listing(t, launched)); len(diff) > 0 {
		t.Errorf("a drain that did not complete changed --launched:\n%s", strings.Join(diff, "\n"))
	}
	for _, dir := range []string{launched, filepath.Join(root, "queue", "done"), filepath.Join(root, "queue", "failed")} {
		card := exists(filepath.Join(dir, "card-9601.md"))
		record := exists(filepath.Join(dir, "card-9601.md.launched"))
		if card != record {
			t.Errorf("SPLIT PAIR in %s: card=%v marker=%v -- the card and its only record are in different places",
				filepath.Base(dir), card, record)
		}
	}
}

// --- Residual 2, as Johnny sharpened it: no --session, no drain ----------------------

// TestHarvestWithNoSessionDrainsNothing: Johnny's finding on #1984 -- "`--launched` without
// `--session` must not drain". An omitted session is not a wildcard and is not inferred
// from anything either (not from the job's RESULT.md, not from the lane, not from the
// bench): the caller must say whose cards these are. Every card is left, with the reason.
func TestHarvestWithNoSessionDrainsNothing(t *testing.T) {
	root, specs, arglog := setupPulse(t)
	benchGit(t, specs, arglog, nil)
	launched := filepath.Join(root, "queue", "launched")
	writeSharedQueue(t, launched, []launchedCard{
		{"card-9601.md", "schema", "vision", "s-42"},
		{"card-9602.md", "pulse", "vision", "tools18-0919b"},
		{"card-9603.md", "bus", "vision", ""},
	})
	before := listing(t, launched)

	shell := &fakeShell{answer: func(bench, script string) (string, error) {
		if strings.Contains(script, "touch") || strings.Contains(script, "PROBE") {
			return "", nil
		}
		out := ""
		for _, label := range []string{"card-9601", "card-9602", "card-9603"} {
			out += benchJobListing("/home/gaffer/rowan-swarm-root/0/jobs/"+label, []string{
				"RESULT " + label, "BRANCH rowan/" + label, "REPO mas-bandwidth/nova-tools", "SESSION s-42",
			})
		}
		return out, nil
	}}
	in := benchHarvestInput(t, root, shell, &fakeForge{})
	in.Bench = "vision"
	in.Session = "" // THE RESIDUAL: omitted, and it must not mean "every session".
	in.Launched = launched
	code, out, errb := runBenchHarvest(t, in)
	if code != 0 {
		t.Fatalf("exit = %d\n%s\n%s", code, out, errb)
	}
	// The jobs are still folded -- the session rule is the DRAIN's, not the fold's.
	if !strings.Contains(out, "drained=0 left=3") {
		t.Errorf("a harvest with no --session drained something:\n%s", out)
	}
	if !strings.Contains(out, "HARVEST LEFT reason=no-session cards=3") {
		t.Errorf("the reason the cards were left is not named:\n%s", out)
	}
	if !strings.Contains(errb, "--launched without --session drains nothing") {
		t.Errorf("the remedy is not named on stderr:\n%s", errb)
	}
	if diff := diffListing(before, listing(t, launched)); len(diff) > 0 {
		t.Errorf("a harvest with no --session touched the shared queue:\n%s", strings.Join(diff, "\n"))
	}
}

// --- Johnny's second finding: job-dir-gone is proven per label, never inferred ---------

// TestHarvestProvesJobDirGonePerLabelAndNeverFromASibling: two cards of the caller's own on
// the same bench, neither named by the listing. The bench is asked about each BY NAME: one
// job directory is there (the card is alive, whatever its siblings are doing) and one is
// not. Before this, ANY sibling job in the listing made every unnamed card `job-dir-gone`.
func TestHarvestProvesJobDirGonePerLabelAndNeverFromASibling(t *testing.T) {
	root, specs, arglog := setupPulse(t)
	benchGit(t, specs, arglog, nil)
	launched := filepath.Join(root, "queue", "launched")
	writeSharedQueue(t, launched, []launchedCard{
		{"card-9601.md", "schema", "vision", "s-42"}, // the sibling the listing names
		{"card-9602.md", "pulse", "vision", "s-42"},  // alive: its own job dir is there
		{"card-9603.md", "bus", "vision", "s-42"},    // gone: its own job dir is not
	})
	before := listing(t, launched)

	var probed []string
	shell := &fakeShell{answer: func(bench, script string) (string, error) {
		switch {
		case strings.Contains(script, "touch"):
			return "", nil
		case strings.Contains(script, "PROBE"):
			probed = append(probed, script)
			return "PROBE\tcard-9602\tpresent\nPROBE\tcard-9603\tabsent\n", nil
		}
		return benchJobListing("/home/gaffer/rowan-swarm-root/0/jobs/card-9601", []string{
			"RESULT card-9601", "BRANCH rowan/card-9601", "REPO mas-bandwidth/nova-tools", "SESSION s-42",
		}), nil
	}}
	in := benchHarvestInput(t, root, shell, &fakeForge{})
	in.Bench = "vision"
	in.Session = "s-42"
	in.Launched = launched
	code, out, errb := runBenchHarvest(t, in)
	if code != 0 {
		t.Fatalf("exit = %d\n%s\n%s", code, out, errb)
	}
	if len(probed) != 1 {
		t.Fatalf("the bench was asked about the labels %d times, want 1: %v", len(probed), probed)
	}
	for _, want := range []string{"'card-9602'", "'card-9603'"} {
		if !strings.Contains(probed[0], want) {
			t.Errorf("the probe does not name %s:\n%s", want, probed[0])
		}
	}
	if strings.Contains(probed[0], "'card-9601'") {
		t.Errorf("a label the listing already answered for was probed again:\n%s", probed[0])
	}
	if !strings.Contains(out, "drained=2 left=1") {
		t.Errorf("the per-label probe did not decide the two cards:\n%s", out)
	}
	if !strings.Contains(out, "HARVEST DRAIN card=card-9603.md lane=bus state=failed bench=vision why=job-dir-gone") {
		t.Errorf("the card whose OWN job directory is gone was not failed:\n%s", out)
	}
	if !strings.Contains(out, "HARVEST LEFT reason=probe-present cards=1") {
		t.Errorf("the live card was not left with its reason:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(launched, "card-9602.md")); err != nil {
		t.Errorf("a card whose own job directory is there was drained: %v", err)
	}
	if _, ok := listing(t, launched)["card-9602.md.launched"]; !ok {
		t.Error("the live card's launch record was taken")
	}
	for _, name := range []string{"card-9601.md", "card-9601.md.launched", "card-9603.md", "card-9603.md.launched"} {
		delete(before, name)
	}
	if diff := diffListing(before, listing(t, launched)); len(diff) > 0 {
		t.Errorf("the live card's pair was touched:\n%s", strings.Join(diff, "\n"))
	}
}

// TestHarvestLeavesACardTheProbeCouldNotAnswerFor: an unreadable `jobs` directory at the
// label's own scope answers `unknown`, and unknown is never absence.
func TestHarvestLeavesACardTheProbeCouldNotAnswerFor(t *testing.T) {
	root, specs, arglog := setupPulse(t)
	benchGit(t, specs, arglog, nil)
	launched := filepath.Join(root, "queue", "launched")
	writeSharedQueue(t, launched, []launchedCard{
		{"card-9601.md", "schema", "vision", "s-42"},
		{"card-9602.md", "pulse", "vision", "s-42"},
	})
	before := listing(t, launched)
	shell := &fakeShell{answer: func(bench, script string) (string, error) {
		switch {
		case strings.Contains(script, "touch"):
			return "", nil
		case strings.Contains(script, "PROBE"):
			return "PROBE\tcard-9602\tunknown\n", nil
		}
		return benchJobListing("/home/gaffer/rowan-swarm-root/0/jobs/card-9601", []string{
			"RESULT card-9601", "BRANCH rowan/card-9601", "REPO mas-bandwidth/nova-tools", "SESSION s-42",
		}), nil
	}}
	in := benchHarvestInput(t, root, shell, &fakeForge{})
	in.Bench = "vision"
	in.Session = "s-42"
	in.Launched = launched
	code, out, errb := runBenchHarvest(t, in)
	if code != 0 {
		t.Fatalf("exit = %d\n%s\n%s", code, out, errb)
	}
	if !strings.Contains(out, "drained=1 left=1") || !strings.Contains(out, "HARVEST LEFT reason=probe-unknown cards=1") {
		t.Errorf("a probe that could not answer was read as an absence:\n%s", out)
	}
	for _, name := range []string{"card-9601.md", "card-9601.md.launched"} {
		delete(before, name)
	}
	if diff := diffListing(before, listing(t, launched)); len(diff) > 0 {
		t.Errorf("the unprobed card's pair was touched:\n%s", strings.Join(diff, "\n"))
	}
}

// TestBenchProbeScriptAsksAboutEachLabelByName runs the generated probe under /bin/sh
// against a real two-root fixture: a job that is there, one that is not, and one whose
// `jobs` directory cannot be read.
func TestBenchProbeScriptAsksAboutEachLabelByName(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: mode 000 does not refuse root")
	}
	dir := t.TempDir()
	rootA, rootB := filepath.Join(dir, "a"), filepath.Join(dir, "b")
	if err := os.MkdirAll(filepath.Join(rootA, "0", "jobs", "card-alive"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(rootB, "0", "jobs"), 0o755); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("/bin/sh", "-c",
		benchProbeScript([]string{rootA, rootB}, []string{"card-alive", "card-gone"})).Output()
	if err != nil {
		t.Fatalf("the probe script failed: %v", err)
	}
	probed := parseProbes(string(out))
	if probed["card-alive"] != "present" || probed["card-gone"] != "absent" {
		t.Errorf("probe = %v, want card-alive present and card-gone absent", probed)
	}
	// Now the same question with the jobs directory unreadable: unknown, not absent.
	jobsB := filepath.Join(rootB, "0", "jobs")
	if err := os.Chmod(jobsB, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(jobsB, 0o755) })
	out, err = exec.Command("/bin/sh", "-c",
		benchProbeScript([]string{rootB}, []string{"card-gone"})).Output()
	if err != nil {
		t.Fatalf("the probe script failed: %v", err)
	}
	if got := parseProbes(string(out))["card-gone"]; got != "unknown" {
		t.Errorf("an unreadable jobs directory answered %q, want unknown", got)
	}
}

// TestHarvestWithASessionStillRefusesAMismatchedOrUnstampedMarker holds the other two arms
// of Stella's "omitted/empty/mismatched" table: a named --session binds strictly, and a
// marker carrying no session is not proved to be the caller's either.
func TestHarvestWithASessionStillRefusesAMismatchedOrUnstampedMarker(t *testing.T) {
	root, specs, arglog := setupPulse(t)
	benchGit(t, specs, arglog, nil)
	launched := filepath.Join(root, "queue", "launched")
	writeSharedQueue(t, launched, []launchedCard{
		{"card-9601.md", "schema", "vision", "s-42"},
		{"card-9602.md", "pulse", "vision", "tools18-0919b"},
		{"card-9603.md", "bus", "vision", ""},
	})
	before := listing(t, launched)
	shell := &fakeShell{answer: func(bench, script string) (string, error) {
		if strings.Contains(script, "touch") {
			return "", nil
		}
		out := ""
		for _, label := range []string{"card-9601", "card-9602", "card-9603"} {
			out += benchJobListing("/home/gaffer/rowan-swarm-root/0/jobs/"+label, []string{
				"RESULT " + label, "BRANCH rowan/" + label, "REPO mas-bandwidth/nova-tools", "SESSION s-42",
			})
		}
		return out, nil
	}}
	in := benchHarvestInput(t, root, shell, &fakeForge{})
	in.Bench = "vision"
	in.Session = "s-42"
	in.Launched = launched
	code, out, errb := runBenchHarvest(t, in)
	if code != 0 {
		t.Fatalf("exit = %d\n%s\n%s", code, out, errb)
	}
	if !strings.Contains(out, "drained=1 left=2") {
		t.Errorf("--session did not bind strictly:\n%s", out)
	}
	if !strings.Contains(out, "HARVEST LEFT reason=other-session cards=2") {
		t.Errorf("the mismatched and unstamped markers are not both named:\n%s", out)
	}
	for _, name := range []string{"card-9601.md", "card-9601.md.launched"} {
		delete(before, name)
	}
	if diff := diffListing(before, listing(t, launched)); len(diff) > 0 {
		t.Errorf("a marker this run cannot bind was touched:\n%s", strings.Join(diff, "\n"))
	}
}

// --- Residual 3: a nonempty listing is not proof of absence ---------------------------

// shellUnderSh runs the REAL generated script through /bin/sh, which is what makes this
// Stella's reproduction rather than a fixture of the answer: the script's own treatment of
// an unreadable directory is the thing under test.
type shellUnderSh struct{}

func (s shellUnderSh) Run(bench, script string) (string, error) {
	out, err := exec.Command("/bin/sh", "-c", script).Output()
	return string(out), err
}

// TestHarvestInfersNoAbsenceFromARootItCouldNotReadWhole is Stella's exact two-root
// fixture. Root A holds a readable job; root B holds a LIVE job under a `jobs` directory at
// mode 000. The glob matches nothing under B, the script exits 0, and before this the
// global "state is nonempty" made B's live card `job-dir-gone`.
func TestHarvestInfersNoAbsenceFromARootItCouldNotReadWhole(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: mode 000 does not refuse root, so the unreadable-descendant fixture cannot be built")
	}
	root, specs, arglog := setupPulse(t)
	benchGit(t, specs, arglog, nil)

	rootA, rootB := filepath.Join(root, "swarm-a"), filepath.Join(root, "swarm-b")
	jobA := filepath.Join(rootA, "0", "jobs", "card-9601")
	jobsB := filepath.Join(rootB, "0", "jobs")
	jobB := filepath.Join(jobsB, "card-9602")
	for _, d := range []string{jobA, jobB} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// A's job is finished and readable; B's job is ALIVE (no RESULT.md).
	if err := os.WriteFile(filepath.Join(jobA, "RESULT.md"),
		[]byte("RESULT card-9601\nBRANCH rowan/card-9601\nREPO mas-bandwidth/nova-tools\nSESSION s-42\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(jobsB, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(jobsB, 0o755) })

	launched := filepath.Join(root, "queue", "launched")
	writeSharedQueue(t, launched, []launchedCard{
		{"card-9601.md", "schema", "vision", "s-42"},
		{"card-9602.md", "pulse", "vision", "s-42"}, // LIVE, under the unreadable root
	})
	before := listing(t, launched)

	in := benchHarvestInputShell(t, root, shellUnderSh{}, &fakeForge{})
	in.Bench = "vision"
	in.Root = rootA + "," + rootB
	in.Session = "s-42"
	in.Launched = launched
	code, out, errb := runBenchHarvest(t, in)
	if code != 0 {
		t.Fatalf("exit = %d\n%s\n%s", code, out, errb)
	}
	// The script must say so itself, and the verb must say so out loud.
	if !strings.Contains(out, "HARVEST ROOT-INCOMPLETE") || !strings.Contains(out, "swarm-b") {
		t.Errorf("the unreadable root was reported as a complete traversal:\n%s", out)
	}
	// A's job is still folded -- an incomplete root costs the run its ABSENCE claims,
	// not its harvest.
	if !strings.Contains(out, "jobs=1") {
		t.Errorf("the readable root's job was not listed:\n%s", out)
	}
	if !strings.Contains(out, "HARVEST LEFT reason=incomplete-listing cards=1") {
		t.Errorf("the live card under the unreadable root was not left with its reason:\n%s", out)
	}
	if strings.Contains(out, "why=job-dir-gone") {
		t.Errorf("a live card was called job-dir-gone on another root's evidence:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(launched, "card-9602.md")); err != nil {
		t.Errorf("the live card under the unreadable root was drained: %v", err)
	}
	for _, name := range []string{"card-9601.md", "card-9601.md.launched"} {
		delete(before, name)
	}
	if diff := diffListing(before, listing(t, launched)); len(diff) > 0 {
		t.Errorf("the live card's pair was touched:\n%s", strings.Join(diff, "\n"))
	}
}

// TestBenchListScriptReportsAnUnreadableRootAsIncomplete holds the script boundary on its
// own: the same fixture, parsed, without the drain.
func TestBenchListScriptReportsAnUnreadableRootAsIncomplete(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: mode 000 does not refuse root")
	}
	dir := t.TempDir()
	rootA, rootB := filepath.Join(dir, "a"), filepath.Join(dir, "b")
	if err := os.MkdirAll(filepath.Join(rootA, "0", "jobs", "card-1"), 0o755); err != nil {
		t.Fatal(err)
	}
	jobsB := filepath.Join(rootB, "0", "jobs")
	if err := os.MkdirAll(filepath.Join(jobsB, "card-2"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(jobsB, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(jobsB, 0o755) })

	out, err := exec.Command("/bin/sh", "-c", benchListScript([]string{rootA, rootB})).Output()
	if err != nil {
		t.Fatalf("the listing script failed: %v", err)
	}
	jobs, missing, incomplete := parseBenchJobs(string(out))
	if len(missing) != 0 {
		t.Errorf("missing=%v, want none (both roots are there)", missing)
	}
	if len(incomplete) != 1 || incomplete[0] != rootB {
		t.Errorf("incomplete=%v, want [%s]: an unreadable jobs directory is not an empty one", incomplete, rootB)
	}
	if len(jobs) != 1 {
		t.Errorf("jobs=%d, want 1 (only the readable root's job is visible)", len(jobs))
	}
}
