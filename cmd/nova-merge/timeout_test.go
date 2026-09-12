package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/merge"
)

// Rule 2, in the spec's own words: "A verb that cannot take the lock within its --timeout
// exits 2 and says who holds it." ITS --timeout, not a constant. Every state write waited
// merge.LockWait, a package constant of ten seconds, whatever the caller asked for, so a
// verb given `--timeout 1` waited ten and a verb given `--timeout 60` gave up at ten.
func TestTheStateLockWaitsTheVerbsOwnTimeout(t *testing.T) {
	t.Parallel()
	l := newLab(t)
	l.init("main")
	release, err := merge.Lock(filepath.Join(l.lane, merge.StateLock), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	start := time.Now()
	exit, stdout, stderr := l.run("add", "--lane", l.lane, "--pr", "951", "--timeout", "1")
	waited := time.Since(start)
	if exit != 2 {
		t.Fatalf("a verb that cannot take the lock exits 2: got %d\n%s\n%s", exit, stdout, stderr)
	}
	contains(t, stderr, "pid=")
	// One second, not ten. The slack is generous; the constant is not within it.
	if waited > 5*time.Second {
		t.Errorf("--timeout 1 waited %s for the state lock; the wait is the verb's own --timeout and not a package constant", waited.Round(time.Millisecond))
	}
}

// Rule 22: the record is FIRST written, byte for byte, to a durable outbox, and only then
// committed and pushed. Deliver took the checkout lock before writing the outbox, so a
// contended read printed `READ FAIL ... file=<path> pushed=false: ...; re-run the same verb
// to push it` naming a file that was never written -- a remedy that cannot work, about a
// record that does not exist.
func TestAContendedReadNamesAFileItReallyWrote(t *testing.T) {
	t.Parallel()
	l := newLab(t)
	setupPR(t, l, 951, "feature-a", "a.txt", false)
	release, err := merge.Lock(filepath.Join(l.lane, merge.CheckoutLock), 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	exit, stdout, stderr := l.run("read", "--lane", l.lane, "--pr", "951", "--who", "emma",
		"--head", strings.Repeat("a", 40), "--verdict", "approve", "--timeout", "1")
	if exit != 1 {
		t.Fatalf("a record that is written and not pushed is exit 1: got %d\n%s\n%s", exit, stdout, stderr)
	}
	contains(t, stderr, "READ FAIL entry=951")
	contains(t, stderr, "pushed=false")
	contains(t, stderr, "re-run the same verb to push it")
	// The outbox really holds it: the remedy the line names is one a person can run.
	entries, err := os.ReadDir(filepath.Join(l.lane, merge.OutboxDir))
	if err != nil {
		t.Fatalf("the outbox does not exist, and READ FAIL named a file in it: %v", err)
	}
	held := 0
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".json") {
			held++
		}
	}
	if held != 1 {
		t.Errorf("the outbox holds %d records after a refusal that named one; the record is written BEFORE the lock is taken, so a contended read never names an unwritten file", held)
	}
}
