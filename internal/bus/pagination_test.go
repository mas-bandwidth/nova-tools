package bus

import (
	"strings"
	"testing"
)

const (
	paginationBase = "1111111111111111111111111111111111111111"
	paginationOne  = "2222222222222222222222222222222222222222"
	paginationTwo  = "3333333333333333333333333333333333333333"
)

func paginationRequest(token string, cursor string, maxNotes int, maxBytes int64) BodyPageRequest {
	return BodyPageRequest{Snapshot: BodySnapshot{Base: paginationBase, Head: paginationTwo, Reader: "Ada", Selector: "inbox-new"}, ExpectedCursor: cursor, Token: token, MaxNotes: maxNotes, MaxBytes: maxBytes}
}

func TestBodyPaginatorEmitsAnExactFitInsteadOfDeferringItForever(t *testing.T) {
	items := []BodyItem{{Commit: paginationOne, Path: "from-bo/a.md", Entry: OpenEntry{Path: "from-bo/a.md"}, Body: []byte("exact")}}
	page, err := BodyPageFor(items, paginationRequest("", paginationBase, 1, 5))
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || page.PrintedBytes != 5 || !page.Drained || !page.Complete || page.Next != "" {
		t.Fatalf("exact-fit body was not terminally emitted: %+v", page)
	}
}

func TestBodyPaginatorCarriesEarlierGapAcrossChangedBudgetAndLaterPages(t *testing.T) {
	items := []BodyItem{
		{Commit: paginationOne, Path: "from-bo/a.md", Entry: OpenEntry{Path: "from-bo/a.md"}, Body: []byte("too-large")},
		{Commit: paginationTwo, Path: "from-bo/b.md", Entry: OpenEntry{Path: "from-bo/b.md"}, Body: []byte("ok")},
	}
	first, err := BodyPageFor(items, paginationRequest("", paginationBase, 1, 3))
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Gaps) != 1 || first.GapCount != 1 || first.Next == "" || first.SafeFrontier != "" {
		t.Fatalf("first page did not record the named gap: %+v", first)
	}
	// Raising a later page's budget never rewrites the historical gap.  The token's
	// last identity resumes after it; a fresh chain is the explicit way to retry it.
	second, err := BodyPageFor(items, paginationRequest(first.Next, paginationBase, 1, 100))
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Items) != 1 || second.Items[0].Path != "from-bo/b.md" || second.GapCount != 1 || !second.Drained || second.Complete || second.SafeFrontier != "" || second.Next != "" {
		t.Fatalf("later page forgot or crossed the earlier gap: %+v", second)
	}
}

func TestBodyPaginatorRefusesExternalCursorChange(t *testing.T) {
	items := []BodyItem{
		{Commit: paginationOne, Path: "from-bo/a.md", Entry: OpenEntry{Path: "from-bo/a.md"}, Body: []byte("a")},
		{Commit: paginationTwo, Path: "from-bo/b.md", Entry: OpenEntry{Path: "from-bo/b.md"}, Body: []byte("b")},
	}
	first, err := BodyPageFor(items, paginationRequest("", paginationBase, 1, 10))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := BodyPageFor(items, paginationRequest(first.Next, paginationTwo, 1, 10)); err == nil || !strings.Contains(err.Error(), "persisted cursor changed") {
		t.Fatalf("external cursor change was accepted: %v", err)
	}
}

func TestBodyPaginatorContinuationSurvivesItsOwnWholeCommitAdvance(t *testing.T) {
	items := []BodyItem{
		{Commit: paginationOne, Path: "from-bo/a.md", Entry: OpenEntry{Path: "from-bo/a.md"}, Body: []byte("a")},
		{Commit: paginationTwo, Path: "from-bo/b.md", Entry: OpenEntry{Path: "from-bo/b.md"}, Body: []byte("b")},
	}
	firstRequest := paginationRequest("", paginationBase, 1, 10)
	firstRequest.Advance = true
	first, err := BodyPageFor(items, firstRequest)
	if err != nil {
		t.Fatal(err)
	}
	if first.SafeFrontier != paginationOne || first.Next == "" {
		t.Fatalf("first page did not produce an advanceable frontier: %+v", first)
	}
	secondRequest := paginationRequest(first.Next, paginationOne, 1, 10)
	secondRequest.Advance = true
	second, err := BodyPageFor(items, secondRequest)
	if err != nil {
		t.Fatalf("ordinary cursor advance invalidated its token: %v", err)
	}
	if len(second.Items) != 1 || second.Items[0].Path != "from-bo/b.md" || !second.Complete {
		t.Fatalf("continuation did not deliver the second item: %+v", second)
	}
}

func TestBodyItemsAtSnapshotReadsPinnedCommitNotChangedWorktree(t *testing.T) {
	hermetic(t)
	bare := bareBus(t)
	clone := cloneBus(t, bare)
	base, err := HeadCommit(clone)
	if err != nil {
		t.Fatal(err)
	}
	path := "from-bo/pinned.md"
	write(t, clone, path, "From: Bo\nTo: Ada\nDate: Mon Sep  7 00:00:00 UTC 2026\nSubject: pinned\n\noriginal")
	commitByHand(t, clone, path, "add pinned note")
	head, err := HeadCommit(clone)
	if err != nil {
		t.Fatal(err)
	}
	// The body source must be git show <commit>:<path>.  Reading the worktree here
	// would return this mutation and make a continuation change under the reader.
	write(t, clone, path, "From: Bo\nTo: Ada\nDate: Mon Sep  7 00:00:00 UTC 2026\nSubject: pinned\n\nworktree mutation")
	items, err := BodyItemsAtSnapshot(clone, BodySnapshot{Base: base, Head: head, Reader: "Ada", Selector: "inbox-new"}, []OpenEntry{{Path: path}})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Commit != head || string(items[0].Body) != "original" {
		t.Fatalf("snapshot item read worktree or lost identity: %+v", items)
	}
}

func TestBodyRecordsAtSnapshotKeepsTwoReceiptOffsetsInOnePath(t *testing.T) {
	hermetic(t)
	bare := bareBus(t)
	clone := cloneBus(t, bare)
	base, err := HeadCommit(clone)
	if err != nil {
		t.Fatal(err)
	}
	path := "from-bo/RECEIPTS"
	write(t, clone, path, "From: Bo\nTo: Ada\nDate: Mon Sep  7 00:00:00 UTC 2026\nSubject: receipt source\n\nreceipt records are selected by the inbox layer")
	commitByHand(t, clone, path, "append two receipts")
	head, err := HeadCommit(clone)
	if err != nil {
		t.Fatal(err)
	}
	// A receipt source may have multiple appended records in one path.  A path-keyed
	// paginator would keep just one and let the cursor pass the other forever.
	records, err := BodyRecordsAtSnapshot(clone, BodySnapshot{Base: base, Head: head, Reader: "Ada", Selector: "inbox-new"}, []BodyItem{
		{Path: path, Offset: 0, Entry: OpenEntry{Path: path}},
		{Path: path, Offset: 1, Entry: OpenEntry{Path: path}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 2 || records[0].Offset != 0 || records[1].Offset != 1 || records[0].Commit != head || records[1].Commit != head {
		t.Fatalf("receipt offsets collapsed in snapshot: %+v", records)
	}
}
