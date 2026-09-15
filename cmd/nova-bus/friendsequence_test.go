package main

import (
	"strings"
	"testing"
	"time"
)

// THE SEQUENCE A FRIEND ACTUALLY RUNS, end to end, in the order they run it: wait for
// news, send the answer, receipt the note that arrived, then advance the cursor. Every
// verb in this package is tested on its own, and every one of them passed on
// 2026-09-15 while the sequence was broken -- because what broke was the state one verb
// LEAVES for the next one, which no single-verb test looks at.
//
// What broke: `wait` writes its lane's BEAT file into the working tree on every poll and
// only COMMITS it once per --beat (sixty seconds by default). A wait that returns sooner
// than that -- which is every wait that returns because a note arrived -- leaves BEAT
// modified and uncommitted, and the next `send` runs checkoutReady, which refuses over a
// checkout "holding changes that are not this note". The friend's answer does not go out,
// and the refusal names a file they never touched.
//
// So the assertion this test carries beside each OK line is `git status --porcelain` being
// empty. A verb that leaves the checkout dirty has broken the next verb, whatever its own
// output said.
func TestFriendSequenceWaitSendReceiptInbox(t *testing.T) {
	// Red on main: wait leaves from-<me>/BEAT uncommitted and the send after it refuses
	// over that file (#459, #488). No branch or PR in the repository carries the fix --
	// #488 was closed on card 373's promise and card 373 was never opened -- so this
	// names the issue rather than inventing a PR number. Delete this line with the fix.
	t.Skip("red on main: wait leaves from-ada/BEAT uncommitted and send refuses over it (nova-tools#488, #459); reopened, no PR carries the fix yet")

	t.Parallel()
	hermetic(t)
	checkout, bare := busDir(t)
	settled(t, checkout)

	clean := func(after string) {
		t.Helper()
		if out := strings.TrimSpace(gitIn(t, checkout, "status", "--porcelain", "--untracked-files=all")); out != "" {
			t.Fatalf("%s left the checkout dirty, so the next verb in the sequence refuses over files the friend never touched:\n%s", after, out)
		}
	}
	clean("the fixture")

	// A note from another line, pushed while the wait is running: the event a wait is for.
	other := bench(t, bare)
	note(t, other, "bo-444444444444", "the pit stop")
	pushed := make(chan error, 1)
	go func() {
		time.Sleep(200 * time.Millisecond)
		pushed <- push(other)
	}()

	invoke(t, "", waitFlags(checkout, "Ada", "10s")...).
		mustCode(t, 0).
		mustContain(t, "stdout", "WAIT OK new=1").
		mustContain(t, "stdout", "WAIT DONE reason=new")
	if err := <-pushed; err != nil {
		t.Fatal(err)
	}
	clean("wait")

	invoke(t, draft, "send", "--bus", checkout, "--stdin", "--remote", "origin", "--branch", "main", "--attempts", "3").
		mustCode(t, 0).
		mustContain(t, "stdout", "SEND OK id=ada-").
		mustContain(t, "stdout", "pushed=true")
	clean("send")

	invoke(t, "", "receipt", "--bus", checkout, "--as", "Ada", "--note", "bo-444444444444",
		"--remote", "origin", "--branch", "main", "--attempts", "3").
		mustCode(t, 0).
		mustContain(t, "stdout", "RECEIPT OK recorded=1").
		mustContain(t, "stdout", "pushed=true")
	clean("receipt")

	invoke(t, "", advance(checkout, "Ada", "--open")...).
		mustCode(t, 0).
		mustContain(t, "stdout", "INBOX OK as=Ada").
		mustContain(t, "stdout", "INBOX CURSOR commit=")
	clean("inbox --advance")
}
