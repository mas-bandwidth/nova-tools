package workissue

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestMappingFidelity(t *testing.T) {
	issue := CapturedIssue{
		Provider: "github",
		Repo:     "mas-bandwidth/nova-tools",
		Number:   2081,
		Revision: "rev-3",
		Title:    "nova-work E09-F02/F03: Map captured issues to tree",
		Body:     "Description of issue mapping and two-sided back pointers.",
		State:    "open",
		Author:   "emma",
		Labels:   []string{"work", "import", "p1"},
		Comments: []CapturedComment{
			{
				ID:        101,
				Author:    "glenn",
				Body:      "Ensure idempotency on second pass.",
				CreatedAt: time.Date(2026, 9, 20, 10, 0, 0, 0, time.UTC),
			},
		},
	}

	node, attrs, err := MapCapturedIssue(issue)
	if err != nil {
		t.Fatalf("unexpected error mapping issue: %v", err)
	}

	if node.ID != "imported/mas-bandwidth/nova-tools/2081" {
		t.Errorf("unexpected node ID: got %q, want %q", node.ID, "imported/mas-bandwidth/nova-tools/2081")
	}
	if node.Title != issue.Title {
		t.Errorf("unexpected title: got %q, want %q", node.Title, issue.Title)
	}
	if node.State != "open" || node.Branch != "o" || node.OpenCount != 1 {
		t.Errorf("unexpected open state: state=%q branch=%q openCount=%d", node.State, node.Branch, node.OpenCount)
	}
	if !IsValidUID(node.UID) {
		t.Errorf("node UID is invalid: %q", node.UID)
	}
	if node.UID != attrs.UID {
		t.Errorf("node UID and attrs UID mismatch: %q vs %q", node.UID, attrs.UID)
	}
	if attrs.Author != "emma" {
		t.Errorf("unexpected author: got %q, want emma", attrs.Author)
	}
	if attrs.Body != issue.Body {
		t.Errorf("unexpected body: got %q, want %q", attrs.Body, issue.Body)
	}
	if attrs.Correspondence.Number != 2081 || attrs.Correspondence.Repo != "mas-bandwidth/nova-tools" {
		t.Errorf("unexpected correspondence: %+v", attrs.Correspondence)
	}

	// Test closed issue mapping
	closedIssue := issue
	closedIssue.State = "closed"
	closedNode, _, err := MapCapturedIssue(closedIssue)
	if err != nil {
		t.Fatalf("unexpected error mapping closed issue: %v", err)
	}
	if closedNode.State != "settled" || closedNode.Branch != "c" || closedNode.OpenCount != 0 {
		t.Errorf("unexpected closed state: state=%q branch=%q openCount=%d", closedNode.State, closedNode.Branch, closedNode.OpenCount)
	}
}

func TestCommentPreservation(t *testing.T) {
	t1 := time.Date(2026, 9, 20, 10, 15, 0, 0, time.UTC)
	t2 := time.Date(2026, 9, 20, 10, 30, 0, 0, time.UTC)
	comments := []CapturedComment{
		{ID: 1, Author: "rowan", Body: "Reviewing plan.", CreatedAt: t1},
		{ID: 2, Author: "emma", Body: "Plan affirmed.", CreatedAt: t2},
	}

	issue := CapturedIssue{
		Number:   2081,
		Title:    "Preserve comments test",
		Comments: comments,
	}

	_, attrs, err := MapCapturedIssue(issue)
	if err != nil {
		t.Fatalf("mapping failed: %v", err)
	}

	if len(attrs.Comments) != 2 {
		t.Fatalf("expected 2 comments, got %d", len(attrs.Comments))
	}
	if attrs.Comments[0].Author != "rowan" || attrs.Comments[0].Body != "Reviewing plan." || !attrs.Comments[0].CreatedAt.Equal(t1) {
		t.Errorf("comment 0 corrupted: %+v", attrs.Comments[0])
	}
	if attrs.Comments[1].Author != "emma" || attrs.Comments[1].Body != "Plan affirmed." || !attrs.Comments[1].CreatedAt.Equal(t2) {
		t.Errorf("comment 1 corrupted: %+v", attrs.Comments[1])
	}
}

func TestLabelSetMapping(t *testing.T) {
	labels := []string{"bug", "critical", "area/work", "sync:verified"}
	issue := CapturedIssue{
		Number: 2081,
		Title:  "Labels test",
		Labels: labels,
	}

	_, attrs, err := MapCapturedIssue(issue)
	if err != nil {
		t.Fatalf("mapping failed: %v", err)
	}

	if len(attrs.Labels) != len(labels) {
		t.Fatalf("expected %d labels, got %d", len(labels), len(attrs.Labels))
	}
	for i, l := range labels {
		if attrs.Labels[i] != l {
			t.Errorf("label %d: got %q, want %q", i, attrs.Labels[i], l)
		}
	}
}

func Test128BitGenericUID(t *testing.T) {
	// 1. Valid minting
	uid, err := MintUID()
	if err != nil {
		t.Fatalf("MintUID failed: %v", err)
	}
	if len(uid) != 32 {
		t.Errorf("expected 32 characters, got %d (%q)", len(uid), uid)
	}
	if !IsValidUID(uid) {
		t.Errorf("expected valid UID, got %q", uid)
	}

	// 2. Collision resistance check across 1000 mints
	seen := make(map[string]struct{}, 1000)
	for i := 0; i < 1000; i++ {
		u, err := MintUID()
		if err != nil {
			t.Fatalf("MintUID failed at iteration %d: %v", i, err)
		}
		if _, exists := seen[u]; exists {
			t.Fatalf("collision detected on UID: %q", u)
		}
		seen[u] = struct{}{}
	}

	// 3. Rejection of invalid UIDs
	invalidCases := []string{
		"",
		"abc",
		"0123456789abcdef0123456789abcde",   // 31 chars
		"0123456789abcdef0123456789abcdef0",  // 33 chars
		"0123456789ABCDEF0123456789abcdef",  // uppercase hex
		"0123456789abcdef0123456789abcdeg",  // invalid char 'g'
	}
	for _, bad := range invalidCases {
		if IsValidUID(bad) {
			t.Errorf("expected IsValidUID(%q) = false, got true", bad)
		}
	}

	// 4. Source refusal on short bytes
	shortBuf := bytes.NewReader([]byte{1, 2, 3, 4, 5})
	_, err = MintUIDFromSource(shortBuf)
	if !errors.Is(err, ErrShortByteSource) {
		t.Errorf("expected ErrShortByteSource on short buffer, got %v", err)
	}

	// 5. Deterministic source
	exactBytes := []byte{
		0x01, 0x23, 0x45, 0x67, 0x89, 0xab, 0xcd, 0xef,
		0xfe, 0xdc, 0xba, 0x98, 0x76, 0x54, 0x32, 0x10,
	}
	deterministicUID, err := MintUIDFromSource(bytes.NewReader(exactBytes))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := "0123456789abcdeffedcba9876543210"
	if deterministicUID != expected {
		t.Errorf("got %q, want %q", deterministicUID, expected)
	}
}

func TestBackPointerFormatAndParse(t *testing.T) {
	uid := "0123456789abcdef0123456789abcdef"

	// With store
	formatted := FormatBackPointer(uid, "s-primary")
	expected := "nova-work: uid=0123456789abcdef0123456789abcdef store=s-primary"
	if formatted != expected {
		t.Errorf("FormatBackPointer: got %q, want %q", formatted, expected)
	}

	parsedUID, parsedStore, ok := ParseBackPointer(formatted)
	if !ok || parsedUID != uid || parsedStore != "s-primary" {
		t.Errorf("ParseBackPointer: got uid=%q store=%q ok=%v", parsedUID, parsedStore, ok)
	}

	// Without store
	formattedBare := FormatBackPointer(uid, "")
	expectedBare := "nova-work: uid=0123456789abcdef0123456789abcdef"
	if formattedBare != expectedBare {
		t.Errorf("FormatBackPointer bare: got %q, want %q", formattedBare, expectedBare)
	}

	parsedBareUID, parsedBareStore, ok := ParseBackPointer(formattedBare)
	if !ok || parsedBareUID != uid || parsedBareStore != "" {
		t.Errorf("ParseBackPointer bare: got uid=%q store=%q ok=%v", parsedBareUID, parsedBareStore, ok)
	}

	// Inside longer text comment
	commentText := "Automated import complete.\nnova-work: uid=0123456789abcdef0123456789abcdef store=s-primary\nTracked."
	parsedEmbeddedUID, parsedEmbeddedStore, ok := ParseBackPointer(commentText)
	if !ok || parsedEmbeddedUID != uid || parsedEmbeddedStore != "s-primary" {
		t.Errorf("ParseBackPointer embedded: got uid=%q store=%q ok=%v", parsedEmbeddedUID, parsedEmbeddedStore, ok)
	}
}

func TestBackPointerIdempotency(t *testing.T) {
	uid := "aabbccddeeff00112233445566778899"
	issue := CapturedIssue{
		Repo:   "mas-bandwidth/nova-tools",
		Number: 2081,
		Title:  "Idempotency proving run",
	}

	// First pass: generate receipt
	receipt, err := GenerateBackPointerReceipt(issue, uid, "store-1")
	if err != nil {
		t.Fatalf("first pass failed: %v", err)
	}
	if receipt == nil || !receipt.Applied {
		t.Fatalf("expected applied receipt, got %+v", receipt)
	}
	if !strings.Contains(receipt.BackPointer, uid) {
		t.Errorf("receipt missing UID: %q", receipt.BackPointer)
	}

	// Apply receipt
	applied := ApplyBackPointerReceipt(&issue, receipt, "nova-work[bot]")
	if !applied {
		t.Fatal("failed to apply receipt to issue")
	}
	if len(issue.Comments) != 1 {
		t.Fatalf("expected 1 comment, got %d", len(issue.Comments))
	}
	if !strings.Contains(issue.Comments[0].Body, "nova-work: uid="+uid) {
		t.Errorf("comment body did not contain back-pointer: %q", issue.Comments[0].Body)
	}

	// Second pass: must detect existing back-pointer and return ErrAlreadyPresent
	secondReceipt, secondErr := GenerateBackPointerReceipt(issue, uid, "store-1")
	if !errors.Is(secondErr, ErrAlreadyPresent) {
		t.Errorf("expected ErrAlreadyPresent on second pass, got error: %v", secondErr)
	}
	if secondReceipt != nil {
		t.Errorf("expected nil receipt on second pass, got %+v", secondReceipt)
	}

	// Applying on issue that already carries it must fail and not add another comment
	appliedSecond := ApplyBackPointerReceipt(&issue, receipt, "nova-work[bot]")
	if appliedSecond {
		t.Error("ApplyBackPointerReceipt should return false when back-pointer already present")
	}
	if len(issue.Comments) != 1 {
		t.Errorf("expected comment count to remain 1, got %d", len(issue.Comments))
	}

	// Check body detection: if body carries back-pointer, second pass also writes nothing
	bodyIssue := CapturedIssue{
		Repo:   "mas-bandwidth/nova-tools",
		Number: 2082,
		Body:   "Issue body already contains nova-work: uid=" + uid,
	}
	_, bodyErr := GenerateBackPointerReceipt(bodyIssue, uid, "")
	if !errors.Is(bodyErr, ErrAlreadyPresent) {
		t.Errorf("expected body back-pointer to trigger ErrAlreadyPresent, got %v", bodyErr)
	}
}
