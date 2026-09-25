package main

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

// Issue #1540. On the coordinator's lane, with the cursor ~800 notes behind:
//
//	$ nova-bus close --bus . --as rowan --before 2026-09-18T12:00:00Z --dry-run
//	CLOSE OK closed=2964 kept=184 commit=-
//	$ nova-bus close --bus . --as rowan --before 2026-09-18T12:00:00Z --remote origin --branch main
//	CLOSE FAIL from-rowan/2026-09-19T0146Z-closed-unanswered-before-2026-09-18t12-00-00z-42cb99b820c2.md: open ...: file exists
//
// No commit, the cursor untouched, thousands of receipts unwritten -- and the remedy the
// tool's own `INBOX WALK bounded` line prescribes therefore unusable on the lane that
// needed it most (`ADOPT REFUSED bus-inbox no INBOX OK` downstream of it).
//
// The cause is one file per closed note. Every receipt in a run carries the same subject --
// the stamp -- so every one got the same slug and the same minute, leaving the id as the
// only thing telling two filenames apart; and the id is a hash over
// (from, date, to, cc, re, subject, kind, body), every field of which two receipts shared
// except `re`. So two open notes sharing a TARGET ID -- a hand-made id a sender reused,
// which `freddy-pong-002` is -- produce one filename twice, and the second Save fails.
//
// This fixture is that, at two notes instead of 2964.
func TestCloseDoesNotCollideAndIsOneReceiptPerLane(t *testing.T) {
	t.Parallel()
	hermetic(t)
	checkout, _ := busDir(t)

	// Two old notes from Bo carrying the SAME hand-made Id, beside the fixture's own two.
	for i, hhmm := range []string{"0900", "0930"} {
		path := fmt.Sprintf("from-bo/2026-09-07T%sZ-pong-%d.md", hhmm, i)
		writeFile(t, checkout, path, fmt.Sprintf(
			"From: Bo\nTo: Ada\nDate: Mon Sep  7 %s:%s:00 UTC 2026\nId: bo-pong-002\nSubject: pong %d\n\nPing.\n",
			hhmm[:2], hhmm[2:], i))
		appendFile(t, checkout, "from-bo/INDEX",
			fmt.Sprintf("bo-pong-002\t%s\t2026-09-07T%s:%s:00Z\tAda\t-\n", path, hhmm[:2], hhmm[2:]))
	}
	commitAs(t, checkout, "Bo", "two pongs sharing a hand-made id")

	// The dry run promises four, and the real run must keep that promise or write nothing.
	invoke(t, "", "close", "--bus", checkout, "--as", "Ada", "--before", "2026-09-08T00:00:00Z", "--dry-run").
		mustCode(t, 0).
		mustContain(t, "stdout", "CLOSE OK closed=4 kept=0 commit=-")

	r := invoke(t, "", "close", "--bus", checkout, "--as", "Ada", "--before", "2026-09-08T00:00:00Z",
		"--remote", "origin", "--branch", "main", "--no-push").
		mustCode(t, 0)
	if strings.Contains(r.stderr, "file exists") {
		t.Fatalf("the close collided with itself:\n%s", r.stderr)
	}
	// closed= counts NOTES and receipts= counts the files it took: one per sender lane.
	r.mustContain(t, "stdout", "CLOSE OK closed=4 kept=0 receipts=1 commit=")

	names := mdFiles(t, checkout, "from-ada")
	if len(names) != 1 {
		t.Fatalf("one receipt per sender lane, want 1 file, got %d: %v", len(names), names)
	}
	got := read(t, checkout, "from-ada/"+names[0])
	// Every target is named once, the duplicated one included -- once, not twice.
	for _, want := range []string{"To: Bo", "Re: bo-abcdef012345", "Re: bo-111111111111", "Re: bo-pong-002", "Kind: receipt"} {
		if !strings.Contains(got, want) {
			t.Fatalf("the receipt does not carry %q:\n%s", want, got)
		}
	}
	if n := strings.Count(got, "Re: bo-pong-002"); n != 1 {
		t.Fatalf("the duplicated target is named %d times, want 1:\n%s", n, got)
	}

	// And every one of the four is off the open list afterwards, which is the point: one
	// receipt naming three ids closes three notes, because internal/bus reads every id a
	// Re line names.
	invoke(t, "", "inbox", "--bus", checkout, "--as", "Ada", "--receipt-max-words", "40", "--open").
		mustCode(t, 0).
		mustContain(t, "stdout", "INBOX OPEN carrying=0 heard=0")
}

// A partial close leaves nothing behind. The file this close cannot write is made
// unwritable on purpose, so the run stops after it has already saved a receipt for another
// lane -- and that receipt must be gone when it returns, not left in the working tree for
// the next run to trip over.
func TestCloseThatCannotFinishWritesNothing(t *testing.T) {
	t.Parallel()
	hermetic(t)
	checkout, _ := busDir(t)

	// A second sender, so the close has two receipts to write and can stop between them.
	writeFile(t, checkout, "from-cy/2026-09-07T0800Z-hello-aaaaaaaaaaaa.md",
		"From: Cy\nTo: Ada\nDate: Mon Sep  7 08:00:00 UTC 2026\nId: cy-aaaaaaaaaaaa\nSubject: Hello\n\nOpen.\n")
	appendFile(t, checkout, "from-cy/INDEX",
		"cy-aaaaaaaaaaaa\tfrom-cy/2026-09-07T0800Z-hello-aaaaaaaaaaaa.md\t2026-09-07T08:00:00Z\tAda\t-\n")
	commitAs(t, checkout, "Cy", "a note from a second sender")

	// The lane's INDEX is made read-only while the lane DIRECTORY stays writable, so the
	// first receipt SAVES and its AppendIndex then fails. The run therefore stops with a
	// receipt already on disk -- which is exactly the state the collision used to leave
	// behind, and exactly the state this fix has to clear up after itself.
	if os.Geteuid() == 0 {
		t.Skip("root writes through a read-only file, so this control would prove nothing here")
	}
	lane := checkout + "/from-ada"
	mkdirAll(t, lane)
	writeFile(t, checkout, "from-ada/INDEX", "")
	commitAs(t, checkout, "Ada", "an empty index for Ada's lane")
	chmod(t, lane+"/INDEX", 0o444)
	r := invoke(t, "", "close", "--bus", checkout, "--as", "Ada", "--before", "2026-09-08T00:00:00Z",
		"--remote", "origin", "--branch", "main", "--no-push").
		mustCode(t, 1)
	// `CLOSE FAIL <path>:` and not a bare `CLOSE FAIL:` -- the path is how this asserts the
	// run reached the WRITE loop and stopped inside it, rather than being turned away
	// earlier by checkoutReady over a dirty tree, which would prove nothing about rollback.
	if !strings.Contains(r.stderr, "CLOSE FAIL from-ada/") || !strings.Contains(r.stderr, "INDEX") {
		t.Fatalf("this close did not stop inside its write loop, so the rollback is untested:\n%s", r.stderr)
	}
	chmod(t, lane+"/INDEX", 0o644)
	if names := mdFiles(t, checkout, "from-ada"); len(names) != 0 {
		t.Fatalf("a close that failed left %d receipts in the tree: %v", len(names), names)
	}

	invoke(t, "", "close", "--bus", checkout, "--as", "Ada", "--before", "2026-09-08T00:00:00Z",
		"--remote", "origin", "--branch", "main", "--no-push").
		mustCode(t, 0).
		mustContain(t, "stdout", "receipts=2")
}

func mkdirAll(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
}

func chmod(t *testing.T, path string, mode os.FileMode) {
	t.Helper()
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
}
