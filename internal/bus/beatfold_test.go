package bus

import "testing"

// A BEAT-ONLY LOCAL COMMIT AHEAD OF THE REMOTE IS THE SENDER'S OWN WORK, not
// somebody else's unfinished push. `wait` commits its lane's BEAT as its own
// "beat <name>" commit and pushes it, so a beat whose push could not land leaves
// the branch ahead of the remote by exactly that one commit -- the state the
// ahead-of-remote guard exists to refuse. The Nova-Bus trailer is how the guard
// tells its own work from a person's, and the beat-only commit carries it: the
// guard passes, and the send's own commit pushes the note and the beat out
// together, no rebase abort, no refusal.
func TestSendFoldsOwnBeatOnlyCommit(t *testing.T) {
	t.Parallel()
	hermetic(t)
	bare := bareBus(t)
	mine := cloneBus(t, bare)

	// The state a beat push that could not land leaves: one commit ahead of the
	// remote, touching from-ada/BEAT and nothing else, carrying the wait's own
	// trailer.
	write(t, mine, "from-ada/BEAT", "2026-09-09T12:35:00Z - until=2026-09-09T12:45:00Z\n")
	if _, err := CommitOnly(mine, testIdentity["Ada"], []string{"from-ada/BEAT"},
		WithTrailer("beat ada", TrailerBeat)); err != nil {
		t.Fatal(err)
	}
	if ahead, err := CommitsBetween(mine, "origin/main", "HEAD"); err != nil || ahead != 1 {
		t.Fatalf("the fixture is not one commit ahead of the remote, it is %d (err=%v)", ahead, err)
	}

	// The guard that refuses a branch ahead of the remote passes over the caller's
	// own beat-only commit: it is this tool's work, and carrying it is the recovery.
	if err := EnsureLevelWith(mine, "origin", "main"); err != nil {
		t.Fatalf("the ahead-of-remote guard refused the branch over the caller's own beat-only commit: %v", err)
	}

	// The send lands: the note's commit is pushed with the branch, beat commit and
	// all -- no rebase abort -- and both are on the remote, with the branch level.
	write(t, mine, "from-ada/a.md", noteText("Ada", "folded", "body"))
	res, err := CommitAndPush(mine, testIdentity["Ada"], []string{"from-ada/a.md"},
		WithTrailer("ada: folded", TrailerSend+" ada-aaaaaaaaaaaa"), "origin", "main", 3)
	if err != nil {
		t.Fatalf("the send over a beat-only commit ahead of the remote failed: %v", err)
	}
	if !res.Pushed {
		t.Fatalf("res = %+v, want pushed", res)
	}
	for _, p := range []string{"from-ada/a.md", "from-ada/BEAT"} {
		if _, cerr := git(bare, "cat-file", "-e", "main:"+p); cerr != nil {
			t.Fatalf("%s is not on the remote; the beat was not carried with the note", p)
		}
	}
	if ahead, err := CommitsBetween(mine, "origin/main", "HEAD"); err != nil || ahead != 0 {
		t.Fatalf("the checkout is still %d commits ahead of the remote after a pushed send (err=%v)", ahead, err)
	}
}
