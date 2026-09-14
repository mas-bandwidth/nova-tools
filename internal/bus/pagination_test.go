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

func TestBodyPaginatorAdvancesToWholePrefixBeforeLaterGap(t *testing.T) {
	items := []BodyItem{
		{Commit: paginationOne, Path: "from-bo/a.md", Entry: OpenEntry{Path: "from-bo/a.md"}, Body: []byte("a")},
		{Commit: paginationTwo, Path: "from-bo/b.md", Entry: OpenEntry{Path: "from-bo/b.md"}, Body: []byte("oversize")},
	}
	request := paginationRequest("", paginationBase, 2, 1)
	request.Advance = true
	page, err := BodyPageFor(items, request)
	if err != nil {
		t.Fatal(err)
	}
	if page.SafeFrontier != paginationOne || len(page.Items) != 1 || len(page.Gaps) != 1 {
		t.Fatalf("did not retain the complete prefix before the gap: %+v", page)
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
	write(t, clone, path, "2026-09-09T12:34:56Z bo-aaaaaaaaaaaa\n2026-09-09T12:35:56Z bo-bbbbbbbbbbbb\n")
	commitByHand(t, clone, path, "append two receipts")
	head, err := HeadCommit(clone)
	if err != nil {
		t.Fatal(err)
	}
	// A real RECEIPTS source has multiple appended timestamp/target records in one path.
	// A path-keyed paginator would keep just one and let the cursor pass the other forever.
	records, err := BodyRecordsAtSnapshot(clone, BodySnapshot{Base: base, Head: head, Reader: "Ada", Selector: "inbox-new"}, []BodyItem{
		{Path: path, Offset: 0, Entry: OpenEntry{ID: "bo-aaaaaaaaaaaa", Kind: OpenReceipt, Path: path}},
		{Path: path, Offset: 1, Entry: OpenEntry{ID: "bo-bbbbbbbbbbbb", Kind: OpenReceipt, Path: path}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 2 || records[0].Offset != 0 || records[1].Offset != 1 || records[0].Commit != head || records[1].Commit != head {
		t.Fatalf("receipt offsets collapsed in snapshot: %+v", records)
	}
}

func TestBodySnapshotReadsFirstParentMergeDelta(t *testing.T) {
	hermetic(t)
	bare := bareBus(t)
	clone := cloneBus(t, bare)
	base, err := HeadCommit(clone)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := git(clone, "branch", "side", base); err != nil {
		t.Fatal(err)
	}
	write(t, clone, "from-bo/main.md", "From: Bo\nTo: Ada\nDate: Mon Sep  7 00:00:00 UTC 2026\nSubject: main\n\nmain")
	commitByHand(t, clone, "from-bo/main.md", "main body")
	if _, err := git(clone, "checkout", "-q", "side"); err != nil {
		t.Fatal(err)
	}
	write(t, clone, "from-bo/side.md", "From: Bo\nTo: Ada\nDate: Mon Sep  7 00:00:00 UTC 2026\nSubject: side\n\nside")
	commitByHand(t, clone, "from-bo/side.md", "side body")
	if _, err := git(clone, "checkout", "-q", "main"); err != nil {
		t.Fatal(err)
	}
	if _, err := git(clone, "-c", "user.name=Merge", "-c", "user.email=merge@example.com", "merge", "--no-ff", "--no-edit", "side"); err != nil {
		t.Fatal(err)
	}
	head, err := HeadCommit(clone)
	if err != nil {
		t.Fatal(err)
	}
	c, err := LoadConfig(clone)
	if err != nil {
		t.Fatal(err)
	}
	items, err := BodyNewItemsAtSnapshot(clone, BodySnapshot{Base: base, Head: head, Reader: "Ada", Selector: "inbox-new"}, c, mustParticipant(t, c, "Ada"), 40, LegacyLine{})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 || items[0].Path != "from-bo/main.md" || items[1].Path != "from-bo/side.md" {
		t.Fatalf("first-parent merge walk missed a note: %+v", items)
	}
}

func TestBodySnapshotUsesFinalRevisionAtHeadAndRetractedReplyDoesNotSettle(t *testing.T) {
	hermetic(t)
	bare := bareBus(t)
	clone := cloneBus(t, bare)
	base, err := HeadCommit(clone)
	if err != nil {
		t.Fatal(err)
	}
	path := "from-bo/edited.md"
	write(t, clone, path, "From: Bo\nTo: Ada\nDate: Mon Sep  7 00:00:00 UTC 2026\nId: bo-aaaaaaaaaaaa\nSubject: first\n\nfirst")
	commitByHand(t, clone, path, "first revision")
	write(t, clone, path, "From: Bo\nTo: Ada\nDate: Mon Sep  7 00:00:00 UTC 2026\nId: bo-aaaaaaaaaaaa\nSubject: final\n\nfinal")
	commitByHand(t, clone, path, "final revision")
	reply := "from-ada/retract.md"
	write(t, clone, reply, "From: Ada\nTo: Bo\nDate: Mon Sep  7 00:00:00 UTC 2026\nSubject: reply\nRe: bo-aaaaaaaaaaaa\n\nreply")
	commitByHand(t, clone, reply, "reply")
	write(t, clone, reply, "From: Ada\nTo: Bo\nDate: Mon Sep  7 00:00:00 UTC 2026\nSubject: retracted\n\nreply")
	commitByHand(t, clone, reply, "retract reply")
	head, err := HeadCommit(clone)
	if err != nil {
		t.Fatal(err)
	}
	c, err := LoadConfig(clone)
	if err != nil {
		t.Fatal(err)
	}
	items, err := BodyNewItemsAtSnapshot(clone, BodySnapshot{Base: base, Head: head, Reader: "Ada", Selector: "inbox-new"}, c, mustParticipant(t, c, "Ada"), 40, LegacyLine{})
	if err != nil || len(items) != 1 || items[0].Path != path || string(items[0].Body) != "final" {
		t.Fatalf("snapshot did not use H's final note/reply state: items=%+v err=%v", items, err)
	}
}

func TestBodySnapshotAllowsHardCeilingBodyAndNamesLargerBlobAsGap(t *testing.T) {
	hermetic(t)
	bare := bareBus(t)
	clone := cloneBus(t, bare)
	base, err := HeadCommit(clone)
	if err != nil {
		t.Fatal(err)
	}
	write(t, clone, "from-bo/exact.md", "From: Bo\nTo: Ada\nDate: Mon Sep  7 00:00:00 UTC 2026\nSubject: exact\n\n"+strings.Repeat("x", 1<<20))
	write(t, clone, "from-bo/crlf.md", "# exact with presentation\r\n \r\nFrom: Bo\r\nTo: Ada\r\nDate: Mon Sep  7 00:00:00 UTC 2026\r\nSubject: crlf\r\n \r\n"+strings.Repeat("x\r\n", 1<<19))
	const largeBytes = 3 << 20
	write(t, clone, "from-bo/too-large.md", "From: Bo\nTo: Ada\nDate: Mon Sep  7 00:00:00 UTC 2026\nSubject: large\n\n"+strings.Repeat("y", largeBytes))
	commitByHand(t, clone, "from-bo/exact.md", "exact and large bodies")
	if _, err := git(clone, "add", "--", "from-bo/crlf.md", "from-bo/too-large.md"); err != nil {
		t.Fatal(err)
	}
	if _, err := git(clone, "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "-q", "-m", "large body"); err != nil {
		t.Fatal(err)
	}
	head, err := HeadCommit(clone)
	if err != nil {
		t.Fatal(err)
	}
	c, err := LoadConfig(clone)
	if err != nil {
		t.Fatal(err)
	}
	items, err := BodyNewItemsAtSnapshot(clone, BodySnapshot{Base: base, Head: head, Reader: "Ada", Selector: "inbox-new"}, c, mustParticipant(t, c, "Ada"), 40, LegacyLine{})
	if err != nil {
		t.Fatal(err)
	}
	var exact, crlf, large BodyItem
	for _, item := range items {
		switch item.Path {
		case "from-bo/exact.md":
			exact = item
		case "from-bo/crlf.md":
			crlf = item
		case "from-bo/too-large.md":
			large = item
		}
	}
	if len(items) != 3 || bodyItemBytes(exact) != 1<<20 || bodyItemBytes(crlf) != 1<<20 || bodyItemBytes(large) != largeBytes {
		t.Fatalf("bounded source did not preserve exact and oversize body sizes: %+v", items)
	}
	crlfPage, err := BodyPageFor([]BodyItem{crlf}, paginationRequest("", base, 1, 1<<20))
	if err != nil || len(crlfPage.Items) != 1 || !crlfPage.Complete {
		t.Fatalf("CRLF body with heading and whitespace separator did not fit: page=%+v err=%v", crlfPage, err)
	}
	page, err := BodyPageFor([]BodyItem{large}, paginationRequest("", base, 1, 1<<20))
	if err != nil || len(page.Gaps) != 1 || page.Gaps[0].Bytes != largeBytes || !page.Drained || page.Complete {
		t.Fatalf("large body was not recorded as a terminal gap: page=%+v err=%v", page, err)
	}
}

func TestBodySnapshotRefusesOversizedHeaderInsteadOfCallingSmallBodyOversize(t *testing.T) {
	hermetic(t)
	bare := bareBus(t)
	clone := cloneBus(t, bare)
	base, err := HeadCommit(clone)
	if err != nil {
		t.Fatal(err)
	}
	write(t, clone, "from-bo/header.md", "From: Bo\nTo: Ada\nDate: Mon Sep  7 00:00:00 UTC 2026\nSubject: "+strings.Repeat("x", bodySnapshotHeaderLimit)+"\n\nsmall")
	commitByHand(t, clone, "from-bo/header.md", "oversized header")
	head, err := HeadCommit(clone)
	if err != nil {
		t.Fatal(err)
	}
	c, err := LoadConfig(clone)
	if err != nil {
		t.Fatal(err)
	}
	_, err = BodyNewItemsAtSnapshot(clone, BodySnapshot{Base: base, Head: head, Reader: "Ada", Selector: "inbox-new"}, c, mustParticipant(t, c, "Ada"), 40, LegacyLine{})
	if err == nil || !strings.Contains(err.Error(), "headers exceed") {
		t.Fatalf("oversized header was silently treated as a body gap: %v", err)
	}
}
