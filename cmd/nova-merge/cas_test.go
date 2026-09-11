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
