package main

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/merge"
)

// #3183: gate receipts are fetched and validated from a stable store (issue comments)
// the gate wrote, not from unverified pull request body quotes.
//
// DONE-WHEN: nova-merge receipt --repo mas-bandwidth/nova-tools --pr <landed batch>
// run on a machine that did not run the gate prints the stored receipt and exits 0,
// and the same verb on a PR whose body carries a hand-typed BATCH OK line with no
// stored artifact exits 1.

func runReceipt(t *testing.T, h *merge.FakeHost, args ...string) (int, string, string) {
	t.Helper()
	deps := Deps{
		NewHost: func(string, time.Duration) merge.Host { return h },
	}
	var out, errb bytes.Buffer
	exit := run(args, &out, &errb, deps)
	return exit, out.String(), errb.String()
}

func TestIssue3183ReceiptFetchedFromStore(t *testing.T) {
	head := strings.Repeat("9", 40)
	base := strings.Repeat("0", 40)
	receiptLine := "BATCH OK name=integration-3183 base=" + base + " head=" + head + " members=3183 dropped=none"

	h := merge.NewFakeHost()
	h.PRs[3183] = merge.PR{
		Number:  3183,
		HeadRef: "rowan/integration-3183",
		HeadOID: head,
		Body:    "integration-3183 body with no stored artifact",
	}
	// Store the actual receipt in issue comments (the stable store written by gate)
	h.SetRawVerdicts(3183, "[{\"body\":\""+receiptLine+"\"}]", "[]")

	exit, stdout, stderr := runReceipt(t, h,
		"receipt", "--repo", "o/n", "--pr", "3183")
	if exit != 0 {
		t.Fatalf("nova-merge receipt: exit %d\nstdout: %s\nstderr: %s", exit, stdout, stderr)
	}
	if !strings.Contains(stdout, receiptLine) {
		t.Fatalf("nova-merge receipt did not print the stored receipt line\nwant: %s\ngot:\n%s", receiptLine, stdout)
	}
	if !strings.Contains(stderr, "RECEIPT SOURCE store") {
		t.Fatalf("a receipt fetched from the store says so on stderr; got:\n%s", stderr)
	}
}

// A hand-typed BATCH OK line in a PR body with no stored artifact in the store is refused (exit 1).
func TestIssue3183HandTypedReceiptInBodyRefused(t *testing.T) {
	head := strings.Repeat("9", 40)
	base := strings.Repeat("0", 40)
	body := "BATCH OK name=integration-3183 base=" + base + " head=" + head + " members=3183 dropped=none\n"

	h := merge.NewFakeHost()
	h.PRs[3183] = merge.PR{
		Number:  3183,
		HeadRef: "rowan/integration-3183",
		HeadOID: head,
		Body:    body,
	}
	// No comments in store

	exit, stdout, stderr := runReceipt(t, h,
		"receipt", "--repo", "o/n", "--pr", "3183")
	if exit != 1 {
		t.Fatalf("a PR with hand-typed body receipt and no stored artifact is exit 1 (refused), got %d\nstdout: %s\nstderr: %s", exit, stdout, stderr)
	}
	if !strings.Contains(stderr, "RECEIPT REFUSED") || !strings.Contains(stderr, "no stored artifact") {
		t.Fatalf("refusal names missing stored artifact; got:\n%s", stderr)
	}
}

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

func TestIssue2693ReceiptFieldOrderDoesNotMatter(t *testing.T) {
	head := strings.Repeat("9", 40)
	line := "BATCH OK head=" + head + " members=2693 name=integration-2693 base=" + strings.Repeat("0", 40) + " dropped=none"
	h := merge.NewFakeHost()
	h.PRs[2693] = merge.PR{Number: 2693, HeadRef: "b", HeadOID: head, Body: "prose"}
	h.SetRawVerdicts(2693, "[{\"body\":\""+line+"\"}]", "[]")
	exit, stdout, stderr := runReceipt(t, h, "receipt", "--repo", "o/n", "--pr", "2693")
	if exit != 0 || !strings.Contains(stdout, line) {
		t.Fatalf("a permuted receipt is read from store; exit %d\nstdout: %s\nstderr: %s", exit, stdout, stderr)
	}
}

func TestIssue2693ReceiptPicksTheOneNamingTheHead(t *testing.T) {
	head := strings.Repeat("9", 40)
	stale := "BATCH OK name=integration-old base=" + strings.Repeat("0", 40) + " head=" + strings.Repeat("8", 40) + " members=2693 dropped=none"
	fresh := "BATCH OK name=integration-new base=" + strings.Repeat("0", 40) + " head=" + head + " members=2693 dropped=none"
	h := merge.NewFakeHost()
	h.PRs[2693] = merge.PR{Number: 2693, HeadRef: "b", HeadOID: head, Body: "prose"}
	h.SetRawVerdicts(2693, "[{\"body\":\""+stale+"\"},{\"body\":\""+fresh+"\"}]", "[]")
	exit, stdout, stderr := runReceipt(t, h, "receipt", "--repo", "o/n", "--pr", "2693")
	if exit != 0 || !strings.Contains(stdout, "integration-new") || strings.Contains(stdout, "integration-old") {
		t.Fatalf("the receipt naming the head is printed; exit %d\nstdout: %s\nstderr: %s", exit, stdout, stderr)
	}

	// Only stale receipts: refused, and the refusal names the heads it saw.
	h.SetRawVerdicts(2693, "[{\"body\":\""+stale+"\"}]", "[]")
	exit, stdout, stderr = runReceipt(t, h, "receipt", "--repo", "o/n", "--pr", "2693")
	if exit != 1 || stdout != "" {
		t.Fatalf("a store with only a stale receipt is refused; exit %d\nstdout: %s\nstderr: %s", exit, stdout, stderr)
	}
}

func TestIssue2693ReceiptRefusesAMissingHead(t *testing.T) {
	line := "BATCH OK name=integration-2693 base=" + strings.Repeat("0", 40) + " head=" + strings.Repeat("9", 40) + " members=2693 dropped=none"
	h := merge.NewFakeHost()
	h.PRs[2693] = merge.PR{Number: 2693, HeadRef: "b", HeadOID: "", Body: "prose"}
	h.SetRawVerdicts(2693, "[{\"body\":\""+line+"\"}]", "[]")
	exit, stdout, stderr := runReceipt(t, h, "receipt", "--repo", "o/n", "--pr", "2693")
	if exit != 1 || stdout != "" || !strings.Contains(stderr, "RECEIPT REFUSED") || !strings.Contains(stderr, "head sha is missing") {
		t.Fatalf("a PR with no head sha is refused naming the missing head; exit %d\nstdout: %s\nstderr: %s", exit, stdout, stderr)
	}
}
