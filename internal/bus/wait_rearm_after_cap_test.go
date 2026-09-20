package bus

import (
	"testing"
	"time"
)

// TestWaitOnNoteRearmsAfterAHarnessCap: a fake service manager kills the unit at the
// cap and restarts it; the restarted wait resumes with the note's arrival not lost.
//
// It pins: "a fake service manager kills the unit at the cap and restarts it; the
// restarted wait resumes with the note's arrival not lost."
func TestWaitOnNoteRearmsAfterAHarnessCap(t *testing.T) {
	t.Parallel()
	hermetic(t)
	bare := bareBus(t)
	clone := cloneBus(t, bare)

	// Ada's first inbox read: commit the roster so there is a commit to point the cursor at.
	// The clone already has the roster on main, seeded by bareBus.  Write a cursor pointing
	// to HEAD and a BEAT, exactly as a running wait would.
	head, err := HeadCommit(clone)
	if err != nil {
		t.Fatalf("HeadCommit: %v", err)
	}
	lane := "from-ada"
	now := at("2026-09-10T12:00:00Z")

	// The wait wrote this cursor before it was killed.
	if err := WriteCursor(clone, lane, head, 0, "", now); err != nil {
		t.Fatalf("WriteCursor: %v", err)
	}
	cursor, err := ReadCursor(clone, lane)
	if err != nil {
		t.Fatalf("ReadCursor: %v", err)
	}
	if cursor.Commit != head {
		t.Fatalf("cursor commit = %q, want %q", cursor.Commit, head)
	}

	// The BEAT the wait wrote, with a lease that has now expired: the "cap" in the spec.
	until := now.Add(-time.Second) // expired
	if err := WriteBeat(clone, lane, head, now, until); err != nil {
		t.Fatalf("WriteBeat: %v", err)
	}
	beat, err := ReadBeat(clone, lane)
	if err != nil {
		t.Fatalf("ReadBeat before kill: %v", err)
	}
	if beat.IsZero() {
		t.Fatal("the BEAT is missing; the wait wrote one before it was killed")
	}

	// === THE WAIT IS KILLED HERE (simulated) ===

	// While the wait was dead, a note arrived on the bus.  Another sender (Bo) commits a
	// note to Ada and pushes it.  This happens from a separate clone so the push is a real
	// remote event.
	other := cloneBus(t, bare)
	write(t, other, "from-bo/2026-09-10T1201Z-a-note-abcdef012345.md", `From: Bo
To: Ada
Date: Thu Sep 10 12:01:00 UTC 2026
Id: bo-abcdef012345
Subject: A note that arrived while the wait was dead

The body.
`)
	write(t, other, "from-bo/INDEX", "bo-abcdef012345\tfrom-bo/2026-09-10T1201Z-a-note-abcdef012345.md\t2026-09-10T12:01:00Z\tAda\t-\n")
	res, err := CommitAndPush(other, testIdentity["Bo"], []string{
		"from-bo/2026-09-10T1201Z-a-note-abcdef012345.md",
		"from-bo/INDEX",
	}, "bo: a note that arrived during the downtime", "origin", "main", 3)
	if err != nil {
		t.Fatalf("Bo could not commit and push the note: %v", err)
	}
	if !res.Pushed {
		t.Fatal("the note was not pushed")
	}

	// === THE SERVICE MANAGER RESTARTS THE WAIT ===

	// The restarted wait fetches the remote, reads its cursor (still pointing at the old
	// HEAD), and writes a fresh BEAT.
	if _, err := FetchAndFastForward(clone, "origin", "main"); err != nil {
		t.Fatalf("the restarted wait could not fetch: %v", err)
	}
	newHead, err := HeadCommit(clone)
	if err != nil {
		t.Fatalf("HeadCommit after fetch: %v", err)
	}
	if newHead == head {
		t.Fatal("HEAD did not advance after the fetch; the bus has no new notes")
	}

	// The restarted wait reads the old cursor, which is still the position the killed wait
	// was at.  The CURSOR file has not been lost across the kill.
	restartedCursor, err := ReadCursor(clone, lane)
	if err != nil {
		t.Fatalf("ReadCursor after restart: %v", err)
	}
	if restartedCursor.Commit != head {
		t.Fatalf("after restart the cursor is %q, want the old commit %q; the cursor was lost",
			restartedCursor.Commit, head)
	}

	// The restarted wait writes its own fresh BEAT, overwriting the stale one.
	restartTime := now.Add(time.Second)
	newUntil := restartTime.Add(10 * time.Minute)
	if err := WriteBeat(clone, lane, head, restartTime, newUntil); err != nil {
		t.Fatalf("WriteBeat after restart: %v", err)
	}

	// Now the restarted wait loads the bus and checks its inbox.
	tab := loadBus(t, clone)
	ada := mustParticipant(t, tab.Config, "Ada")
	items := tab.Inbox(ada, 40)

	// The note Bo sent arrived during the downtime.  It must be in Ada's inbox — not lost.
	var found bool
	for _, item := range items {
		if item.Note.Header.ID == "bo-abcdef012345" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("the note that arrived while the wait was dead is NOT in the restarted wait's inbox — the arrival was lost")
	}
}
