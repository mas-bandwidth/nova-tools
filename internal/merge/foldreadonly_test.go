package merge

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Read 4b, finding 2 -- rule 22 ("Every Git operation on a lane checkout ... runs under
// one checkout lock") read against rule 23 ("`packet` ... takes no lock"):
// A LOCK-FREE FOLD READS A TREE A CONCURRENT FLUSH IS WRITING.
//
// The flush restores the outbox's bytes over the record's path with a truncate-then-write
// (records.go's restore), so a fold that takes no lock can catch a record HALF WRITTEN.
// It then parses as a problem -- and a problem is a blocked entry, which for a packet is a
// reader told their entry is broken when nothing is. Rule 23 keeps the lock off the
// packet, so the fix is not a lock: a file that parses as a problem is READ AGAIN once,
// and only a file that refuses BOTH times is a real refusal.
//
// Neither of these tests is parallel: each replaces this package's Sleep, which is the
// pause between the two reads, so that the writer lands exactly inside it.

func recordLane(t *testing.T) (lane, file string, whole []byte) {
	t.Helper()
	lane = t.TempDir()
	head := "f454cea1c383f454cea1c383f454cea1c383f454"
	file = filepath.Join(ReadsDir, "951", "stella-"+Short(head)+"-20260911T131500Z-abcdef.json")
	whole = []byte(fmt.Sprintf(`{"who":"stella","verdict":"hold","note":"the wire's shape","at":"2026-09-11T13:15:00Z","head":%q,"file":%q}`, head, filepath.ToSlash(file)))
	if err := os.MkdirAll(filepath.Dir(filepath.Join(lane, file)), 0o755); err != nil {
		t.Fatal(err)
	}
	return lane, file, whole
}

func laneRecords(lane string) *Records {
	return &Records{Lane: lane, Branch: "nova-merge/lane", Remote: "origin",
		Git: NewGit(lane, time.Second, nil), Wait: time.Second}
}

func TestAReadOnlyFoldReadsAgainARecordItCaughtHalfWritten(t *testing.T) {
	lane, file, whole := recordLane(t)
	full := filepath.Join(lane, file)
	if err := os.WriteFile(full, whole[:len(whole)/2], 0o644); err != nil {
		t.Fatal(err)
	}
	// The restore finishes inside the fold's own pause: this is the flush's window, held
	// still rather than raced.
	old := Sleep
	defer func() { Sleep = old }()
	Sleep = func(time.Duration) {
		if err := os.WriteFile(full, whole, 0o644); err != nil {
			t.Error(err)
		}
	}
	f, err := laneRecords(lane).FoldReadOnly()
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Problems) != 0 {
		t.Fatalf("a record caught half written is read again, not refused: %+v", f.Problems)
	}
	if got := f.Reads["951"]; len(got) != 1 || got[0].Verdict != "hold" {
		t.Fatalf("the hold is in the fold after the second read, got %+v", got)
	}
}

func TestAReadOnlyFoldKeepsARecordThatRefusesTwice(t *testing.T) {
	lane, file, whole := recordLane(t)
	if err := os.WriteFile(filepath.Join(lane, file), whole[:len(whole)/2], 0o644); err != nil {
		t.Fatal(err)
	}
	reread := 0
	old := Sleep
	defer func() { Sleep = old }()
	Sleep = func(time.Duration) { reread++ }
	f, err := laneRecords(lane).FoldReadOnly()
	if err != nil {
		t.Fatal(err)
	}
	if reread != 1 {
		t.Errorf("a file that parses as a problem is read again exactly once, got %d reads after the first", reread)
	}
	if len(f.Problems) != 1 || f.Problems[0].Entry != "951" {
		t.Fatalf("a file that refuses both times is the fold's refusal, got %+v", f.Problems)
	}
	if len(f.Reads["951"]) != 0 {
		t.Errorf("nothing was folded out of it, got %+v", f.Reads["951"])
	}
}
