package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/merge"
)

// Demanded test 22, the REJECT, RESET, RESTORE, RETRY half, against a real bare remote and
// two real lane clones.
//
// The failure this pins is the one a fixture taught on 2026-09-11: after a rejected push
// the loser's new record was tracked in its local commit, the reset to the winner's tip
// removed it, and the next add failed with `pathspec ... did not match any files`. The
// outbox is what puts the bytes back, so a mutation that drops the restore turns this red.

// outboxBytes reads what a lane's outbox holds right now, keyed by file name. It is how
// this test learns the EXACT bytes a writer's outbox held, before delivery empties it.
func outboxBytes(lane string) map[string][]byte {
	out := map[string][]byte{}
	entries, err := os.ReadDir(filepath.Join(lane, merge.OutboxDir))
	if err != nil {
		return out
	}
	for _, e := range entries {
		if e.IsDir() || strings.HasSuffix(e.Name(), ".tmp") {
			continue
		}
		body, err := os.ReadFile(filepath.Join(lane, merge.OutboxDir, e.Name()))
		if err != nil {
			continue
		}
		out[e.Name()] = body
	}
	return out
}

// destOf reads the `file` field the record itself carries: where these bytes belong in the
// lane branch.
func destOf(t *testing.T, body []byte) string {
	t.Helper()
	var probe struct {
		File string `json:"file"`
	}
	if err := json.Unmarshal(body, &probe); err != nil {
		t.Fatalf("an outbox item is not a record: %v", err)
	}
	if probe.File == "" {
		t.Fatalf("an outbox item names no path: %s", body)
	}
	return probe.File
}

func TestEveryVerdictSurvivesTheFold(t *testing.T) {
	t.Parallel()
	l := newLab(t)
	oid := setupPR(t, l, 951, "feature-a", "a.txt", true)
	alice := l.lane
	bob := filepath.Join(l.dir, "bob")

	// A SECOND LANE ON THE SAME BRANCH: bob's own checkout of nova-merge/lane, which
	// init joins rather than creates.
	exit, stdout, stderr := l.run("init", "--lane", bob, "--repo", "o/n", "--base", "main", "--lane-branch", "nova-merge/lane")
	if exit != 0 {
		t.Fatalf("bob's init: exit %d\n%s\n%s", exit, stdout, stderr)
	}
	contains(t, stdout, "joined=true")
	if exit, _, errb := l.run("add", "--lane", bob, "--pr", "951"); exit != 0 {
		t.Fatalf("bob's add: %s", errb)
	}

	// Both writers start from the same tip, and alice's record LANDS between bob's fetch
	// and bob's push -- the window a compare-and-swap loop exists for. The hand runs once,
	// immediately before bob's first push of the lane branch.
	sent := map[string]map[string][]byte{}
	fired := false
	l.runner = &hookRunner{inner: merge.Exec{}, before: func(dir string, args []string) {
		if len(args) == 0 || args[0] != "push" {
			return
		}
		// The bytes this writer's outbox holds at the moment it tries to push.
		if _, seen := sent[dir]; !seen {
			if b := outboxBytes(dir); len(b) > 0 {
				sent[dir] = b
			}
		}
		if fired || dir != bob {
			return
		}
		fired = true
		if exit, _, errb := l.run("read", "--lane", alice, "--pr", "951", "--who", "alice",
			"--head", oid, "--verdict", "approve"); exit != 0 {
			t.Errorf("alice's read, inside bob's push window: exit %d: %s", exit, errb)
		}
	}}

	// Bob's push is rejected, its reset moves to alice's tip, the outbox restores its
	// exact bytes, and the second push lands.
	exit, stdout, stderr = l.run("read", "--lane", bob, "--pr", "951", "--who", "bob",
		"--head", oid, "--verdict", "approve")
	if exit != 0 {
		t.Fatalf("the loser of the race retries and lands: exit %d\n%s\n%s", exit, stdout, stderr)
	}
	contains(t, stdout, "READ OK entry=951")
	contains(t, stdout, "pushed=true")
	if !fired {
		t.Fatal("the hand never ran, so no push was ever rejected and this test proved nothing")
	}

	// THE REMOTE HOLDS BOTH FILES, with the bytes each writer's outbox held, byte for
	// byte. The reset removed nothing the outbox did not put back.
	side := filepath.Join(l.dir, "verify")
	l.git(l.dir, "clone", "-q", "--branch", "nova-merge/lane", l.remote, side)
	if len(sent) != 2 {
		t.Fatalf("two writers each had an outbox; this run saw %d", len(sent))
	}
	var dests []string
	for lane, items := range sent {
		for name, body := range items {
			dest := destOf(t, body)
			got, err := os.ReadFile(filepath.Join(side, filepath.FromSlash(dest)))
			if err != nil {
				t.Fatalf("%s's outbox item %s is not at the remote tip at %s: %v", lane, name, dest, err)
			}
			if string(got) != string(body) {
				t.Errorf("%s at the remote tip is not the bytes %s's outbox held:\nwant %q\ngot  %q", dest, lane, body, got)
			}
			dests = append(dests, dest)
		}
	}
	sort.Strings(dests)
	if len(dests) != 2 {
		t.Fatalf("the branch holds the records of both writers; this run compared %d", len(dests))
	}
	if dests[0] == dests[1] {
		t.Errorf("two writers never touch one path, and both wrote %s", dests[0])
	}

	// The outbox is empty on both sides -- delivered means seen at the remote tip -- and
	// every lane's git status is clean after the verb.
	for _, lane := range []string{alice, bob} {
		if b := outboxBytes(lane); len(b) != 0 {
			t.Errorf("%s's outbox still holds %d items after a landed push", lane, len(b))
		}
		if out := l.git(lane, "status", "--porcelain"); strings.TrimSpace(out) != "" {
			t.Errorf("git status in %s is not clean after the verb:\n%s", lane, out)
		}
	}

	// And both verdicts are in the coordinator's next fold: the lists are the fold and
	// the files are the truth.
	exit, stdout, stderr = l.run("status", "--lane", alice)
	if exit != 0 {
		t.Fatalf("status: exit %d\n%s\n%s", exit, stdout, stderr)
	}
	contains(t, stdout, "reads=2a/0h")
}

// Demanded test 22, THE FIRST HALF, under its own name.
//
// docs/SPEC-MERGE.md, demanded test 22: "two readers on two lane checkouts of one branch
// and a coordinator on a third record, concurrently and from the same starting branch, two
// reads (`--head H1` and `--head H1` by different names), one gate, and one `add`; every
// push lands (the CAS loop retries on rejection), the branch holds three record files, the
// coordinator's next pass prints `pulled=3`, `status` shows `reads=2a/0h` and the gate, and
// each fold record's `head` is the sha its reader supplied, byte for byte."
//
// TestEveryVerdictSurvivesTheFold pins the reject/reset/restore/retry sentence that
// follows it, with two writers and no gate; nothing pinned this half. What it holds that
// the other does not: THREE RECORD KINDS THROUGH THREE CLONES -- a gate written on a
// machine that is not the coordinator's reaches the coordinator's fold, `pulled=` counts
// what the fetch really brought, and an `add` on a lane whose last push was reset still
// writes no record file into the branch.
func TestTwoReadsAndAGateFromOneStartingBranchReachTheCoordinator(t *testing.T) {
	t.Parallel()
	l := newLab(t)
	oid := setupPR(t, l, 951, "feature-a", "a.txt", false)
	base := l.baseSHA()
	coordinator := l.lane

	// The coordinator's first pass BUILDS the integration object and waits for a gate. It
	// pulls nothing -- no record has been written by anyone yet -- and it is where the
	// merge sha a gate must name comes from.
	exit, stdout, stderr := l.run("run", "--lane", coordinator, "--once")
	if exit != 0 {
		t.Fatalf("a lane waiting on a gate is not a failure: exit %d\n%s\n%s", exit, stdout, stderr)
	}
	contains(t, stdout, "pulled=0")
	m := mergeSHAOf(t, stdout, "951")

	// Two readers, each with its own checkout of the one lane branch, both joined at the
	// SAME STARTING TIP: no record has been written by anyone yet.
	emma := filepath.Join(l.dir, "emma")
	stella := filepath.Join(l.dir, "stella")
	for _, lane := range []string{emma, stella} {
		exit, stdout, stderr := l.run("init", "--lane", lane, "--repo", "o/n", "--base", "main", "--lane-branch", "nova-merge/lane")
		if exit != 0 {
			t.Fatalf("a reader's lane on the same branch: exit %d\n%s\n%s", exit, stdout, stderr)
		}
		contains(t, stdout, "joined=true")
	}

	// Emma's read lands INSIDE stella's push window, so stella's push is rejected and the
	// CAS loop is what makes it land: both readers started from the tip above.
	fired := false
	l.runner = &hookRunner{inner: merge.Exec{}, before: func(dir string, args []string) {
		if fired || len(args) == 0 || args[0] != "push" || dir != stella {
			return
		}
		fired = true
		if exit, _, errb := l.run("read", "--lane", emma, "--pr", "951", "--who", "emma",
			"--head", oid, "--verdict", "approve"); exit != 0 {
			t.Errorf("emma's read, inside stella's push window: exit %d: %s", exit, errb)
		}
	}}
	exit, stdout, stderr = l.run("read", "--lane", stella, "--pr", "951", "--who", "stella",
		"--head", oid, "--verdict", "approve")
	if exit != 0 {
		t.Fatalf("stella's read: exit %d\n%s\n%s", exit, stdout, stderr)
	}
	contains(t, stdout, "pushed=true")
	if !fired {
		t.Fatal("the hand never ran, so no push was ever rejected and the retry proved nothing")
	}

	// The `add`: a queueing verb on the lane whose push was just reset and restored. It
	// writes state and NO RECORD FILE, which the count below is what proves.
	if exit, _, errb := l.run("add", "--lane", emma, "--pr", "951"); exit != 0 {
		t.Fatalf("emma's add: exit %d: %s", exit, errb)
	}

	// The gate, for the entry, FROM A MACHINE THAT IS NOT THE COORDINATOR'S: it names the
	// object the coordinator built, by sha.
	if exit, _, errb := l.run("gate", "--lane", stella, "--pr", "951", "--head", oid,
		"--base-sha", base, "--merge", m, "--verdict", "green", "--summary", l.summary("gate")); exit != 0 {
		t.Fatalf("stella's gate: exit %d: %s", exit, errb)
	}

	// THE BRANCH HOLDS THREE RECORD FILES, and each read's head is the sha its reader
	// supplied, byte for byte.
	side := filepath.Join(l.dir, "verify")
	l.git(l.dir, "clone", "-q", "--branch", "nova-merge/lane", l.remote, side)
	records := recordFiles(t, side)
	if len(records) != 3 {
		t.Fatalf("the branch holds three record files, got %d: %v", len(records), records)
	}
	who := map[string]string{}
	for _, rel := range records {
		body, err := os.ReadFile(filepath.Join(side, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatal(err)
		}
		var probe struct {
			Who, Head, Base, Merge, Verdict string
		}
		if err := json.Unmarshal(body, &probe); err != nil {
			t.Fatalf("%s is not a record: %v", rel, err)
		}
		// EVERY RECORD'S head IS THE SHA ITS WRITER SUPPLIED, byte for byte: a fold that
		// shortened, re-resolved or inherited one would be a record for another commit.
		if probe.Head != oid {
			t.Errorf("%s carries head=%q, want the sha its writer supplied, %q", rel, probe.Head, oid)
		}
		if strings.HasPrefix(rel, merge.GatesDir+"/") {
			if probe.Base != base || probe.Merge != m {
				t.Errorf("%s carries base=%q merge=%q, want the pair the gate was taken for, %q and %q", rel, probe.Base, probe.Merge, base, m)
			}
			continue
		}
		if probe.Verdict != "approve" {
			t.Errorf("%s carries verdict=%q, want approve", rel, probe.Verdict)
		}
		if probe.Who != "" {
			who[probe.Who] = rel
		}
	}
	if len(who) != 2 || who["emma"] == "" || who["stella"] == "" {
		t.Fatalf("the two reads are emma's and stella's, got %v", who)
	}

	// A READER'S OWN status, before the coordinator acts, shows all three: both reads as a
	// count and the gate on the entry they are for.
	_, stdout, _ = l.run("status", "--lane", emma)
	contains(t, stdout, "reads=2a/0h")
	contains(t, stdout, "STATUS ENTRY kind=pr entry=951")
	contains(t, stdout, "gate=merge state=MERGEABLE-GREEN")

	// THE COORDINATOR'S NEXT PASS pulls all three -- two reads and a gate no coordinator
	// wrote -- says so, and acts on them.
	exit, stdout, stderr = l.run("run", "--lane", coordinator, "--once")
	if exit != 0 {
		t.Fatalf("the pass with its reads and its gate: exit %d\n%s\n%s", exit, stdout, stderr)
	}
	contains(t, stdout, "pulled=3")
	contains(t, stdout, "read=2a/0h")
	contains(t, stdout, "MERGE OK entry=951")
	// Every lane is clean afterwards, on all three machines.
	for _, lane := range []string{coordinator, emma, stella} {
		if out := l.git(lane, "status", "--porcelain"); out != "" {
			t.Errorf("%s is not clean:\n%s", lane, out)
		}
	}
}

// recordFiles is every record file a checkout of the lane branch holds, as slash-separated
// paths: the reads and the gates, never their summaries.
func recordFiles(t *testing.T, checkout string) []string {
	t.Helper()
	var out []string
	for _, dir := range []string{merge.ReadsDir, merge.GatesDir} {
		root := filepath.Join(checkout, dir)
		err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
			if err != nil || d == nil || d.IsDir() || !strings.HasSuffix(p, ".json") {
				return nil
			}
			rel, relErr := filepath.Rel(checkout, p)
			if relErr != nil {
				return relErr
			}
			out = append(out, filepath.ToSlash(rel))
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	sort.Strings(out)
	return out
}

// Rule 22, the outbox: A FLUSH REMOVES WHAT IT DELIVERED AND NOTHING ELSE.
//
// The outbox is written BEFORE the checkout lock is taken, which is what makes the bytes
// durable before any reset can move a tree under them -- and it means a second verb on
// this lane can have its record sitting in the outbox, waiting for the lock, while the
// first is pushing. `deliveredOK` swept the whole directory, so that waiting record was
// deleted: a read or a gate whose writer had already been told "recorded" would never
// reach the branch, and nothing would say so.
func TestAFlushRemovesOnlyWhatItDelivered(t *testing.T) {
	t.Parallel()
	l := newLab(t)
	oid := setupPR(t, l, 951, "feature-a", "a.txt", true)

	// A second verb's record, written into the outbox in the window the first verb's
	// flush is pushing through: the hand runs once, immediately before that push.
	waiting := filepath.Join(l.lane, merge.OutboxDir, "20260911T131500Z-zzzzzz.json")
	body := []byte(`{"pr":951,"who":"stella","head":"` + oid + `","verdict":"approve","at":"2026-09-11T13:15:00Z","run":"zzzzzz","file":"reads/951/stella-` + oid[:12] + `-20260911T131500Z-zzzzzz.json"}` + "\n")
	fired := false
	l.runner = &hookRunner{inner: merge.Exec{}, before: func(dir string, args []string) {
		if fired || len(args) == 0 || args[0] != "push" || dir != l.lane {
			return
		}
		fired = true
		if err := os.MkdirAll(filepath.Dir(waiting), 0o755); err != nil {
			t.Error(err)
			return
		}
		if err := os.WriteFile(waiting, body, 0o644); err != nil {
			t.Error(err)
		}
	}}
	exit, stdout, stderr := l.run("read", "--lane", l.lane, "--pr", "951", "--who", "emma",
		"--head", oid, "--verdict", "approve")
	if exit != 0 {
		t.Fatalf("emma's read: exit %d\n%s\n%s", exit, stdout, stderr)
	}
	contains(t, stdout, "pushed=true")
	if !fired {
		t.Fatal("the hand never ran, so nothing was ever waiting in the outbox and this test proved nothing")
	}

	// THE WAITING RECORD IS EITHER STILL WAITING OR ALREADY DELIVERED, and never neither:
	// this flush removes what it carried, so an item that appeared behind it is kept for
	// its own flush -- or, if this flush's next round picked it up, it is at the remote.
	dest := "reads/951/stella-" + oid[:12] + "-20260911T131500Z-zzzzzz.json"
	if got, err := os.ReadFile(waiting); err == nil {
		if string(got) != string(body) {
			t.Errorf("the waiting outbox item is not the bytes its writer left:\nwant %q\ngot  %q", body, got)
		}
	} else if !onBranch(t, l, dest) {
		t.Fatal("the record another verb left in the outbox is neither in the outbox nor at the remote: a flush that never carried it deleted it, and its writer was told it was recorded")
	}
	// Emma's own item is gone: delivered is delivered, and nothing it did not carry
	// stayed behind either.
	for _, name := range outboxNames(t, l.lane) {
		if name == filepath.Base(waiting) {
			continue
		}
		t.Errorf("the outbox still holds %s, which this flush delivered", name)
	}
	// The next verb on this lane delivers whatever is left, which is what the outbox is
	// for, and the waiting record reaches the branch with its own bytes.
	if exit, _, errb := l.run("status", "--lane", l.lane); exit != 0 {
		t.Fatalf("status: %s", errb)
	}
	if !onBranch(t, l, dest) {
		t.Error("the waiting record never reached the branch")
	}
}

// onBranch reports whether the lane branch at the remote holds this path.
func onBranch(t *testing.T, l *lab, path string) bool {
	t.Helper()
	side, err := os.MkdirTemp(l.dir, "verify-branch")
	if err != nil {
		t.Fatal(err)
	}
	l.git(l.dir, "clone", "-q", "--branch", "nova-merge/lane", l.remote, side)
	_, statErr := os.Stat(filepath.Join(side, filepath.FromSlash(path)))
	return statErr == nil
}

// outboxNames is what the lane's outbox holds right now.
func outboxNames(t *testing.T, lane string) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(lane, merge.OutboxDir))
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		out = append(out, e.Name())
	}
	return out
}
