package main

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/merge"
)

// #2693: gate receipts are not independently verifiable after the fact. An
// integration PR's body quotes a BATCH OK line, but no verb names where a
// reader on another machine fetches the receipt itself: the line, the bench,
// the base and merged tree shas, and the go test -json stream (#2626, #2645).
// `nova-merge receipt <pr>` prints the receipt a PR's body carries, so a
// reader who can name the pull request can name the evidence.
//
// On base the verb does not exist: `nova-merge receipt` exits 2 with
// "unknown subcommand". With the fix it reads the PR's body from the forge,
// finds the BATCH OK line, and prints it on stdout -- the same line a caller
// hands to `nova-merge land --receipt`. A PR whose body has no receipt is
// refused with `RECEIPT REFUSED`, exit 1, and the reason names the absence.

func runReceipt(t *testing.T, h *merge.FakeHost, args ...string) (int, string, string) {
	t.Helper()
	deps := Deps{
		NewHost: func(string, time.Duration) merge.Host { return h },
	}
	var out, errb bytes.Buffer
	exit := run(args, &out, &errb, deps)
	return exit, out.String(), errb.String()
}

// The receipt verb reads the BATCH OK line a PR's body quotes, so a reader on
// another machine can fetch it without ssh and without the lander's word.
func TestIssue2693(t *testing.T) {
	head := strings.Repeat("9", 40)
	base := strings.Repeat("0", 40)
	body := "integration-2693\n\n" +
		"closes #2693\n\n" +
		"BATCH OK name=integration-2693 base=" + base + " head=" + head + " members=2693 dropped=none skipped=lisp checks=required check=ci-ok\n"

	h := merge.NewFakeHost()
	h.PRs[2693] = merge.PR{
		Number:  2693,
		HeadRef: "rowan/integration-2693",
		HeadOID: head,
		Body:    body,
	}

	want := "BATCH OK name=integration-2693 base=" + base + " head=" + head + " members=2693 dropped=none"
	exit, stdout, stderr := runReceipt(t, h,
		"receipt", "--repo", "o/n", "--pr", "2693")
	if exit != 0 {
		t.Fatalf("nova-merge receipt: exit %d\nstdout: %s\nstderr: %s", exit, stdout, stderr)
	}
	if !strings.Contains(stdout, want) {
		t.Fatalf("nova-merge receipt did not print the receipt line\nwant substring: %s\ngot:\n%s", want, stdout)
	}
}

// A pull request whose body quotes no BATCH OK line is refused with a reason
// that names the absence, not a silent empty answer: a reader who names a PR
// the gate never built for is not handed a green receipt over nothing.
func TestIssue2693RefusesAPullRequestWithNoReceipt(t *testing.T) {
	h := merge.NewFakeHost()
	h.PRs[2693] = merge.PR{
		Number:  2693,
		HeadRef: "some-branch",
		HeadOID: strings.Repeat("9", 40),
		Body:    "no gate receipt on this pull request",
	}
	exit, stdout, stderr := runReceipt(t, h,
		"receipt", "--repo", "o/n", "--pr", "2693")
	if exit != 1 {
		t.Fatalf("a PR with no receipt is exit 1 (refused), got %d\nstdout: %s\nstderr: %s", exit, stdout, stderr)
	}
	if !strings.Contains(stderr, "RECEIPT") || !strings.Contains(stderr, "2693") {
		t.Fatalf("a refused receipt names the PR and the word RECEIPT on stderr; got:\n%s", stderr)
	}
}
