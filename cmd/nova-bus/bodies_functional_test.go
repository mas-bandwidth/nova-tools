//go:build functional

package main

import (
	"encoding/base64"
	"errors"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/bus"
	"github.com/stretchr/testify/require"
)

func TestBodiesFullUsesCountedFramesAndHonoursByteBudget(t *testing.T) {
	t.Parallel()
	checkout, _ := busDir(t)
	r := invoke(t, "", "inbox", "--bus", checkout, "--as", "Ada", "--receipt-max-words", "40", "--full", "--bodies", "--max-notes", "1", "--max-bytes", "1000").mustCode(t, 0)
	require.Falsef(t, strings.Count(r.stdout, "INBOX BODY id=") != 1 || !strings.Contains(r.stdout, "bytes=44"), "bounded body frame missing or malformed:\n%s", r.stdout)
	require.Containsf(t, r.stdout, "INBOX BODIES printed=1 bytes=44 oversize=0 gaps=0 drained=false complete=false next=", "body receipt did not report bounded completion:\n%s", r.stdout)
}

func TestBodiesBrokenOutputCannotAdvanceCursor(t *testing.T) {
	t.Parallel()
	checkout, _ := busDir(t)
	invoke(t, "", "inbox", "--bus", checkout, "--as", "Ada", "--receipt-max-words", "40", "--full", "--advance", "--carry-history", "--remote", "origin", "--branch", "main").mustCode(t, 0)
	before, err := bus.ReadCursor(checkout, "from-ada")
	require.NoError(t, err)
	addBodyCommit(t, checkout, "from-bo/output.md", "bo-dddddddddddd", "output")
	var stderr strings.Builder
	code := run([]string{"inbox", "--bus", checkout, "--as", "Ada", "--receipt-max-words", "40", "--bodies", "--max-notes", "1", "--max-bytes", "100", "--advance", "--remote", "origin", "--branch", "main"}, strings.NewReader(""), refusingWriter{}, &stderr, now())
	require.Falsef(t, code != 1 || !strings.Contains(stderr.String(), "INBOX FAIL output"), "broken stdout did not refuse safely: code=%d stderr=%s", code, stderr.String())
	after, err := bus.ReadCursor(checkout, "from-ada")
	require.NoError(t, err)
	require.Falsef(t, after.Commit != before.Commit, "cursor advanced across failed body output: before=%s after=%s", before.Commit, after.Commit)
}

func TestBodiesPreservesReceiptAndHeardAsSummaryOnly(t *testing.T) {
	t.Parallel()
	checkout, _ := busDir(t)
	// The fixture's second incoming note is an actual receipt-shaped note. It remains a
	// listing entry, but must not be reframed as body prose.
	receipt := invoke(t, "", "inbox", "--bus", checkout, "--as", "Ada", "--receipt-max-words", "40", "--full", "--bodies", "--max-notes", "10", "--max-bytes", "1000").mustCode(t, 0)
	require.Falsef(t, !strings.Contains(receipt.stdout, "INBOX RECEIPT id=bo-111111111111") || strings.Contains(receipt.stdout, "INBOX BODY id=bo-111111111111"), "receipt did not remain summary-only:\n%s", receipt.stdout)

	invoke(t, "", "inbox", "--bus", checkout, "--as", "Ada", "--receipt-max-words", "40", "--full", "--advance", "--carry-history", "--remote", "origin", "--branch", "main").mustCode(t, 0)
	addBodyCommit(t, checkout, "from-bo/heard.md", "bo-121212121212", "heard body")
	addReceiptRecord(t, checkout, "bo-121212121212")
	heard := invoke(t, "", "inbox", "--bus", checkout, "--as", "Ada", "--receipt-max-words", "40", "--bodies", "--max-notes", "10", "--max-bytes", "1000").mustCode(t, 0)
	require.Falsef(t, !strings.Contains(heard.stdout, "INBOX HEARD id=bo-121212121212") || strings.Contains(heard.stdout, "INBOX BODY id=bo-121212121212"), "valid RECEIPTS record did not make the note heard/summary-only:\n%s", heard.stdout)
}

func TestBodiesKeepInboxDisplayGroupsWhenCanonicalOrderStartsReceipt(t *testing.T) {
	t.Parallel()
	checkout, _ := busDir(t)
	invoke(t, "", "inbox", "--bus", checkout, "--as", "Ada", "--receipt-max-words", "40", "--full", "--advance", "--carry-history", "--remote", "origin", "--branch", "main").mustCode(t, 0)
	addGroupedBodyCommit(t, checkout)
	r := invoke(t, "", "inbox", "--bus", checkout, "--as", "Ada", "--receipt-max-words", "40", "--bodies", "--max-notes", "3", "--max-bytes", "1000").mustCode(t, 0)
	note := strings.Index(r.stdout, "INBOX NOTE id=bo-ccccccccccce")
	heard := strings.Index(r.stdout, "INBOX HEARD id=bo-bbbbbbbbbbbe")
	receipt := strings.Index(r.stdout, "INBOX RECEIPT id=bo-aaaaaaaaaaae")
	require.Falsef(t, note < 0 || heard < 0 || receipt < 0 || !(note < heard && heard < receipt), "bodies did not retain NOTE, HEARD, RECEIPT display groups:\n%s", r.stdout)
	require.Falsef(t, strings.Contains(r.stdout, "INBOX BODY id=bo-bbbbbbbbbbbe") || strings.Contains(r.stdout, "INBOX BODY id=bo-aaaaaaaaaaae"), "grouped summary entries were reframed as bodies:\n%s", r.stdout)
}

type refusingWriter struct{}

func (refusingWriter) Write([]byte) (int, error) { return 0, errors.New("broken pipe") }

func TestBodiesContinuationKeepsOriginalSnapshotAfterAdvanceAndNewTip(t *testing.T) {
	t.Parallel()
	checkout, _ := busDir(t)
	// Establish C0 so the body chain is an incremental C0..H snapshot rather than
	// first-run adoption history.
	invoke(t, "", "inbox", "--bus", checkout, "--as", "Ada", "--receipt-max-words", "40", "--full", "--advance", "--carry-history", "--remote", "origin", "--branch", "main").mustCode(t, 0)
	addBodyCommit(t, checkout, "from-bo/a.md", "bo-aaaaaaaaaaaa", "A")
	addBodyCommit(t, checkout, "from-bo/b.md", "bo-bbbbbbbbbbbb", "B")
	first := invoke(t, "", "inbox", "--bus", checkout, "--as", "Ada", "--receipt-max-words", "40", "--bodies", "--max-notes", "1", "--max-bytes", "100", "--advance", "--remote", "origin", "--branch", "main").mustCode(t, 0)
	token := bodyNext(t, first.stdout)
	require.Falsef(t, !strings.Contains(first.stdout, "\nA\n") || strings.Contains(first.stdout, "\nB\n"), "first page was not the first immutable item:\n%s", first.stdout)
	open, err := bus.ReadOpen(checkout, "from-ada")
	require.NoError(t, err)
	for _, entry := range open {
		require.NotEqualf(t, "bo-bbbbbbbbbbbb", entry.ID, "unemitted B was persisted in OPEN: %+v", open)
	}
	ordinary := invoke(t, "", "inbox", "--bus", checkout, "--as", "Ada", "--receipt-max-words", "40").mustCode(t, 0)
	require.Containsf(t, ordinary.stdout, "path=from-bo/b.md", "ordinary inbox did not retain B as NEW after page A advance:\n%s", ordinary.stdout)
	// This reply reaches HEAD after H and closes B in today's OPEN.  The continuation
	// must rebuild C0..H from its token, rather than let that later state erase B.
	addBodyReply(t, checkout, "from-ada/re-b.md", "bo-bbbbbbbbbbbb")
	second := invoke(t, "", "inbox", "--bus", checkout, "--as", "Ada", "--receipt-max-words", "40", "--bodies", "--max-notes", "1", "--max-bytes", "100", "--after", token, "--advance", "--remote", "origin", "--branch", "main").mustCode(t, 0)
	require.Falsef(t, !strings.Contains(second.stdout, "\nB\n") || strings.Contains(second.stdout, "re-b") || !strings.Contains(second.stdout, "drained=true complete=true next=-"), "continuation did not stay on its original snapshot:\n%s", second.stdout)
}

func TestBodiesReadOnlyContinuationNeedsNoPersistedOpenSnapshot(t *testing.T) {
	t.Parallel()
	checkout, _ := busDir(t)
	invoke(t, "", "inbox", "--bus", checkout, "--as", "Ada", "--receipt-max-words", "40", "--full", "--advance", "--carry-history", "--remote", "origin", "--branch", "main").mustCode(t, 0)
	addBodyCommit(t, checkout, "from-bo/read-only-a.md", "bo-eeeeeeeeeeee", "read-only A")
	addBodyCommit(t, checkout, "from-bo/read-only-b.md", "bo-ffffffffffff", "read-only B")
	first := invoke(t, "", "inbox", "--bus", checkout, "--as", "Ada", "--receipt-max-words", "40", "--bodies", "--max-notes", "1", "--max-bytes", "100").mustCode(t, 0)
	token := bodyNext(t, first.stdout)
	addBodyReply(t, checkout, "from-ada/read-only-re-b.md", "bo-ffffffffffff")
	second := invoke(t, "", "inbox", "--bus", checkout, "--as", "Ada", "--receipt-max-words", "40", "--bodies", "--max-notes", "1", "--max-bytes", "100", "--after", token).mustCode(t, 0)
	require.Falsef(t, !strings.Contains(second.stdout, "\nread-only B\n") || !strings.Contains(second.stdout, "drained=true complete=true next=-"), "read-only continuation consulted current OPEN instead of C0..H:\n%s", second.stdout)
}

func TestBodiesSameCommitPersistsWholeEmittedPrefix(t *testing.T) {
	t.Parallel()
	checkout, _ := busDir(t)
	invoke(t, "", "inbox", "--bus", checkout, "--as", "Ada", "--receipt-max-words", "40", "--full", "--advance", "--carry-history", "--remote", "origin", "--branch", "main").mustCode(t, 0)
	addTwoBodyNotesOneCommit(t, checkout)
	first := invoke(t, "", "inbox", "--bus", checkout, "--as", "Ada", "--receipt-max-words", "40", "--bodies", "--max-notes", "1", "--max-bytes", "100", "--advance", "--remote", "origin", "--branch", "main").mustCode(t, 0)
	token := bodyNext(t, first.stdout)
	{
		open, err := bus.ReadOpen(checkout, "from-ada")
		require.Falsef(t, err != nil || hasOpenID(open, "bo-cccccccccccc") || hasOpenID(open, "bo-dddddddddddd"), "partial commit wrote either new body into OPEN: open=%+v err=%v", open, err)
	}
	invoke(t, "", "inbox", "--bus", checkout, "--as", "Ada", "--receipt-max-words", "40", "--bodies", "--max-notes", "1", "--max-bytes", "100", "--after", token, "--advance", "--remote", "origin", "--branch", "main").mustCode(t, 0)
	open, err := bus.ReadOpen(checkout, "from-ada")
	require.Falsef(t, err != nil || !hasOpenID(open, "bo-cccccccccccc") || !hasOpenID(open, "bo-dddddddddddd"), "whole same-commit prefix did not persist A and B: open=%+v err=%v", open, err)
}

func TestBodiesFullAdvancePersistsOnlyEmittedItems(t *testing.T) {
	t.Parallel()
	checkout, _ := busDir(t)
	addBodyCommit(t, checkout, "from-bo/unprinted.md", "bo-eeeeeeeeeeef", "unprinted")
	r := invoke(t, "", "inbox", "--bus", checkout, "--as", "Ada", "--receipt-max-words", "40", "--full", "--bodies", "--max-notes", "2", "--max-bytes", "1000", "--advance", "--carry-history", "--remote", "origin", "--branch", "main").mustCode(t, 0)
	open, err := bus.ReadOpen(checkout, "from-ada")
	require.NoError(t, err)
	require.Falsef(t, len(open) != 2 || hasOpenID(open, "bo-eeeeeeeeeeef"), "full bodies page persisted unprinted OPEN rows: %d\n%s", len(open), r.stdout)
	require.Containsf(t, r.stdout, "id="+open[0].ID, "persisted row was not emitted by the bounded page: open=%+v\n%s", open, r.stdout)
}

func TestBodiesFullAdvancePreservesExistingOpenCarry(t *testing.T) {
	t.Parallel()
	checkout, _ := busDir(t)
	invoke(t, "", "inbox", "--bus", checkout, "--as", "Ada", "--receipt-max-words", "40", "--full", "--advance", "--carry-history", "--remote", "origin", "--branch", "main").mustCode(t, 0)
	before, err := bus.ReadOpen(checkout, "from-ada")
	require.Falsef(t, err != nil || len(before) == 0, "fixture did not establish carried OPEN: open=%+v err=%v", before, err)
	addBodyCommit(t, checkout, "from-bo/new-full.md", "bo-eeeeeeeeeeea", "new full")
	invoke(t, "", "inbox", "--bus", checkout, "--as", "Ada", "--receipt-max-words", "40", "--full", "--bodies", "--max-notes", "1", "--max-bytes", "100", "--advance", "--remote", "origin", "--branch", "main").mustCode(t, 0)
	after, err := bus.ReadOpen(checkout, "from-ada")
	require.Falsef(t, err != nil || !hasOpenID(after, "bo-eeeeeeeeeeea"), "full bodies advance did not record its emitted NEW item: open=%+v err=%v", after, err)
	for _, entry := range before {
		require.Truef(t, hasOpenID(after, entry.ID), "full bodies advance dropped prior carry %q: before=%+v after=%+v", entry.ID, before, after)
	}
}

func hasOpenID(entries []bus.OpenEntry, id string) bool {
	for _, entry := range entries {
		if entry.ID == id {
			return true
		}
	}
	return false
}

func TestBodiesContinuationRefusesOtherReaderAndSelector(t *testing.T) {
	t.Parallel()
	checkout, _ := busDir(t)
	first := invoke(t, "", "inbox", "--bus", checkout, "--as", "Ada", "--receipt-max-words", "40", "--full", "--bodies", "--max-notes", "1", "--max-bytes", "1000").mustCode(t, 0)
	token := bodyNext(t, first.stdout)
	other := invoke(t, "", "inbox", "--bus", checkout, "--as", "Bo", "--receipt-max-words", "40", "--full", "--bodies", "--max-notes", "1", "--max-bytes", "1000", "--after", token)
	require.Falsef(t, other.code != 2 || !strings.Contains(other.stderr, "another reader or selector"), "other reader accepted Ada token: code=%d stderr=%s", other.code, other.stderr)
	raw, err := base64.RawURLEncoding.DecodeString(token)
	require.NoError(t, err)
	bad := base64.RawURLEncoding.EncodeToString([]byte(strings.Replace(string(raw), `"s":"inbox-new"`, `"s":"other"`, 1)))
	wrongSelector := invoke(t, "", "inbox", "--bus", checkout, "--as", "Ada", "--receipt-max-words", "40", "--full", "--bodies", "--max-notes", "1", "--max-bytes", "1000", "--after", bad)
	require.Falsef(t, wrongSelector.code != 2 || !strings.Contains(wrongSelector.stderr, "another reader or selector"), "other selector accepted token: code=%d stderr=%s", wrongSelector.code, wrongSelector.stderr)
}

func addBodyCommit(t *testing.T, checkout, path, id, body string) {
	t.Helper()
	writeFile(t, checkout, path, "From: Bo\nTo: Ada\nDate: Mon Sep  7 00:00:00 UTC 2026\nId: "+id+"\nSubject: "+body+"\n\n"+body)
	gitIn(t, checkout, "add", "--", path)
	gitIn(t, checkout, "-c", "user.name=Bo", "-c", "user.email=bo@example.com", "commit", "-q", "-m", "body "+body)
	gitIn(t, checkout, "push", "-q", "origin", "main")
}

func addGroupedBodyCommit(t *testing.T, checkout string) {
	t.Helper()
	writeFile(t, checkout, "from-bo/a-receipt.md", "From: Bo\nTo: Ada\nDate: Mon Sep  7 00:00:00 UTC 2026\nId: bo-aaaaaaaaaaae\nSubject: receipt\nKind: receipt\n\nreceived")
	writeFile(t, checkout, "from-bo/b-heard.md", "From: Bo\nTo: Ada\nDate: Mon Sep  7 00:00:00 UTC 2026\nId: bo-bbbbbbbbbbbe\nSubject: heard\n\nheard body")
	writeFile(t, checkout, "from-bo/c-note.md", "From: Bo\nTo: Ada\nDate: Mon Sep  7 00:00:00 UTC 2026\nId: bo-ccccccccccce\nSubject: note\n\nnote body")
	writeFile(t, checkout, "from-ada/RECEIPTS", "2026-09-09T12:34:56Z bo-bbbbbbbbbbbe\n")
	gitIn(t, checkout, "add", "--", "from-bo/a-receipt.md", "from-bo/b-heard.md", "from-bo/c-note.md", "from-ada/RECEIPTS")
	gitIn(t, checkout, "-c", "user.name=Bo", "-c", "user.email=bo@example.com", "commit", "-q", "-m", "receipt heard note")
	gitIn(t, checkout, "push", "-q", "origin", "main")
}

func addTwoBodyNotesOneCommit(t *testing.T, checkout string) {
	t.Helper()
	writeFile(t, checkout, "from-bo/a.md", "From: Bo\nTo: Ada\nDate: Mon Sep  7 00:00:00 UTC 2026\nId: bo-cccccccccccc\nSubject: A\n\nA")
	writeFile(t, checkout, "from-bo/b.md", "From: Bo\nTo: Ada\nDate: Mon Sep  7 00:00:00 UTC 2026\nId: bo-dddddddddddd\nSubject: B\n\nB")
	gitIn(t, checkout, "add", "--", "from-bo/a.md", "from-bo/b.md")
	gitIn(t, checkout, "-c", "user.name=Bo", "-c", "user.email=bo@example.com", "commit", "-q", "-m", "two bodies")
	gitIn(t, checkout, "push", "-q", "origin", "main")
}

func addBodyReply(t *testing.T, checkout, path, target string) {
	t.Helper()
	writeFile(t, checkout, path, "From: Ada\nTo: Bo\nDate: Mon Sep  7 00:00:00 UTC 2026\nSubject: later reply\nRe: "+target+"\n\nlater")
	gitIn(t, checkout, "add", "--", path)
	gitIn(t, checkout, "-c", "user.name=Ada", "-c", "user.email=ada@example.com", "commit", "-q", "-m", "later reply")
	gitIn(t, checkout, "push", "-q", "origin", "main")
}

func addReceiptRecord(t *testing.T, checkout, target string) {
	t.Helper()
	writeFile(t, checkout, "from-ada/RECEIPTS", "2026-09-09T12:34:56Z "+target+"\n")
	gitIn(t, checkout, "add", "--", "from-ada/RECEIPTS")
	gitIn(t, checkout, "-c", "user.name=Ada", "-c", "user.email=ada@example.com", "commit", "-q", "-m", "receipt "+target)
	gitIn(t, checkout, "push", "-q", "origin", "main")
}

func bodyNext(t *testing.T, stdout string) string {
	t.Helper()
	for _, field := range strings.Fields(stdout) {
		if token, ok := strings.CutPrefix(field, "next="); ok && token != "-" {
			return token
		}
	}
	require.FailNowf(t, "assertion failed", "bodies receipt had no continuation token:\n%s", stdout)
	return ""
}
