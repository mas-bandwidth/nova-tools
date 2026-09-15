package main

import (
	"strings"
	"testing"
)

// TestFriendSequenceWaitSendReceiptInbox is the friend sequence a line runs through the
// bus in one go: wait for a note that arrives mid-call, answer it with send, receipt the
// note, and advance the inbox. Each verb must leave the checkout clean -- the sequence is
// four verbs, and a dirty checkout after any one of them is the next verb refusing, which
// is exactly the two-regressions Glenn called out. wait's beat wrote a BEAT file that made
// the next send refuse "dirty checkout", so the one property asserted after every step is
// that git status --porcelain is empty.
func TestFriendSequenceWaitSendReceiptInbox(t *testing.T) {
	t.Skip("pending #558 (nova-bus wait must not leave its BEAT dirty after a short wait)")
	t.Parallel()
	hermetic(t)
	checkout, bare := busDir(t)
	settled(t, checkout)

	clean := func(step string) {
		t.Helper()
		if dirty := strings.TrimSpace(gitIn(t, checkout, "status", "--porcelain")); dirty != "" {
			t.Fatalf("after %s the checkout is dirty:\n%s", step, dirty)
		}
	}

	// wait: a note from a second clone arrives mid-call and ends the wait.
	other := bench(t, bare)
	note(t, other, "bo-777777777777", "a friend arrives")
	if err := push(other); err != nil {
		t.Fatal(err)
	}
	invoke(t, "", waitFlags(checkout, "Ada", "5s", "--advance")...).
		mustCode(t, 0).
		mustContain(t, "stdout", "WAIT OK new=1").
		mustContain(t, "stdout", "INBOX NOTE id=bo-777777777777")
	clean("wait")

	// send: the answer.
	invoke(t, "", draft, "send", "--bus", checkout, "--stdin", "--remote", "origin", "--branch", "main", "--attempts", "3").
		mustCode(t, 0).
		mustContain(t, "stdout", "SEND OK id=ada-")
	clean("send")

	// receipt: Ada heard the note that woke the wait.
	invoke(t, "", "receipt", "--bus", checkout, "--as", "Ada", "--note", "bo-777777777777",
		"--remote", "origin", "--branch", "main", "--attempts", "3").
		mustCode(t, 0).
		mustContain(t, "stdout", "RECEIPT OK recorded=1 already=0")
	clean("receipt")

	// inbox --advance: the cursor moves to head over what was read and heard.
	invoke(t, "", advance(checkout, "Ada", "--open")...).
		mustCode(t, 0).
		mustContain(t, "stdout", "INBOX OK as=Ada")
	clean("inbox --advance")
}
