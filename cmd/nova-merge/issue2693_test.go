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

// The receipt verb reads the BATCH OK line a PR's body quotes. The body is
// editable, so the verb says on stderr that the line is a pr-body quote and not
// a stored gate artifact (#3183 is the store).
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
	if !strings.Contains(stderr, "RECEIPT SOURCE pr-body") {
		t.Fatalf("a receipt read from a PR body says so on stderr; got:\n%s", stderr)
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

// --repo is required, like every other verb that names the repository: an empty
// one would ask gh about a pull request in whatever repository the cwd is.
func TestIssue2693ReceiptRequiresRepo(t *testing.T) {
	h := merge.NewFakeHost()
	exit, stdout, stderr := runReceipt(t, h, "receipt", "--pr", "2693")
	if exit != 2 {
		t.Fatalf("receipt with no --repo is exit 2, got %d\nstdout: %s\nstderr: %s", exit, stdout, stderr)
	}
	if !strings.Contains(stderr, "--repo") {
		t.Fatalf("the refusal names --repo; got:\n%s", stderr)
	}
}

// A receipt whose fields are in another order is still a receipt: the parser
// reads key=value fields, never positions, and the finder agrees with it.
func TestIssue2693ReceiptFieldOrderDoesNotMatter(t *testing.T) {
	head := strings.Repeat("9", 40)
	line := "BATCH OK head=" + head + " members=2693 name=integration-2693 base=" + strings.Repeat("0", 40) + " dropped=none"
	h := merge.NewFakeHost()
	h.PRs[2693] = merge.PR{Number: 2693, HeadRef: "b", HeadOID: head, Body: "prose\n" + line + "\n"}
	exit, stdout, stderr := runReceipt(t, h, "receipt", "--repo", "o/n", "--pr", "2693")
	if exit != 0 || !strings.Contains(stdout, line) {
		t.Fatalf("a permuted receipt is read; exit %d\nstdout: %s\nstderr: %s", exit, stdout, stderr)
	}
}

// A body that quotes an earlier batch's receipt before the current one is
// answered with the one naming the PR's head, not the first match.
func TestIssue2693ReceiptPicksTheOneNamingTheHead(t *testing.T) {
	head := strings.Repeat("9", 40)
	stale := "BATCH OK name=integration-old base=" + strings.Repeat("0", 40) + " head=" + strings.Repeat("8", 40) + " members=2693 dropped=none"
	fresh := "BATCH OK name=integration-new base=" + strings.Repeat("0", 40) + " head=" + head + " members=2693 dropped=none"
	h := merge.NewFakeHost()
	h.PRs[2693] = merge.PR{Number: 2693, HeadRef: "b", HeadOID: head, Body: stale + "\n\n" + fresh + "\n"}
	exit, stdout, stderr := runReceipt(t, h, "receipt", "--repo", "o/n", "--pr", "2693")
	if exit != 0 || !strings.Contains(stdout, "integration-new") || strings.Contains(stdout, "integration-old") {
		t.Fatalf("the receipt naming the head is printed; exit %d\nstdout: %s\nstderr: %s", exit, stdout, stderr)
	}

	// Only stale receipts: refused, and the refusal names the heads it saw.
	h.PRs[2693] = merge.PR{Number: 2693, HeadRef: "b", HeadOID: head, Body: stale + "\n"}
	exit, stdout, stderr = runReceipt(t, h, "receipt", "--repo", "o/n", "--pr", "2693")
	if exit != 1 || !strings.Contains(stderr, strings.Repeat("8", 40)) || stdout != "" {
		t.Fatalf("a body with only a stale receipt is refused naming its head; exit %d\nstdout: %s\nstderr: %s", exit, stdout, stderr)
	}
}
