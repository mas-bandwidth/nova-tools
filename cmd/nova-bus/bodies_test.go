package main

import (
	"errors"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/bus"
)

func TestBodiesFullUsesCountedFramesAndHonoursByteBudget(t *testing.T) {
	t.Parallel()
	checkout, _ := busDir(t)
	r := invoke(t, "", "inbox", "--bus", checkout, "--as", "Ada", "--receipt-max-words", "40", "--full", "--bodies", "--max-notes", "1", "--max-bytes", "1000").mustCode(t, 0)
	if strings.Count(r.stdout, "INBOX BODY id=") != 1 || !strings.Contains(r.stdout, "bytes=44") {
		t.Fatalf("bounded body frame missing or malformed:\n%s", r.stdout)
	}
	if !strings.Contains(r.stdout, "INBOX BODIES printed=1 bytes=44 oversize=0 gaps=0 drained=false complete=false next=") {
		t.Fatalf("body receipt did not report bounded completion:\n%s", r.stdout)
	}
}

func TestBodiesBrokenOutputCannotAdvanceCursor(t *testing.T) {
	checkout, _ := busDir(t)
	invoke(t, "", "inbox", "--bus", checkout, "--as", "Ada", "--receipt-max-words", "40", "--full", "--advance", "--carry-history", "--remote", "origin", "--branch", "main").mustCode(t, 0)
	before, err := bus.ReadCursor(checkout, "from-ada")
	if err != nil {
		t.Fatal(err)
	}
	addBodyCommit(t, checkout, "from-bo/output.md", "bo-dddddddddddd", "output")
	var stderr strings.Builder
	code := run([]string{"inbox", "--bus", checkout, "--as", "Ada", "--receipt-max-words", "40", "--bodies", "--max-notes", "1", "--max-bytes", "100", "--advance", "--remote", "origin", "--branch", "main"}, strings.NewReader(""), refusingWriter{}, &stderr, now())
	if code != 1 || !strings.Contains(stderr.String(), "INBOX FAIL output") {
		t.Fatalf("broken stdout did not refuse safely: code=%d stderr=%s", code, stderr.String())
	}
	after, err := bus.ReadCursor(checkout, "from-ada")
	if err != nil {
		t.Fatal(err)
	}
	if after.Commit != before.Commit {
		t.Fatalf("cursor advanced across failed body output: before=%s after=%s", before.Commit, after.Commit)
	}
}

type refusingWriter struct{}

func (refusingWriter) Write([]byte) (int, error) { return 0, errors.New("broken pipe") }

func TestBodiesContinuationKeepsOriginalSnapshotAfterAdvanceAndNewTip(t *testing.T) {
	checkout, _ := busDir(t)
	// Establish C0 so the body chain is an incremental C0..H snapshot rather than
	// first-run adoption history.
	invoke(t, "", "inbox", "--bus", checkout, "--as", "Ada", "--receipt-max-words", "40", "--full", "--advance", "--carry-history", "--remote", "origin", "--branch", "main").mustCode(t, 0)
	addBodyCommit(t, checkout, "from-bo/a.md", "bo-aaaaaaaaaaaa", "A")
	addBodyCommit(t, checkout, "from-bo/b.md", "bo-bbbbbbbbbbbb", "B")
	first := invoke(t, "", "inbox", "--bus", checkout, "--as", "Ada", "--receipt-max-words", "40", "--bodies", "--max-notes", "1", "--max-bytes", "100", "--advance", "--remote", "origin", "--branch", "main").mustCode(t, 0)
	token := bodyNext(t, first.stdout)
	if !strings.Contains(first.stdout, "\nA\n") || strings.Contains(first.stdout, "\nB\n") {
		t.Fatalf("first page was not the first immutable item:\n%s", first.stdout)
	}
	// This reply reaches HEAD after H and closes B in today's OPEN.  The continuation
	// must rebuild C0..H from its token, rather than let that later state erase B.
	addBodyReply(t, checkout, "from-ada/re-b.md", "bo-bbbbbbbbbbbb")
	second := invoke(t, "", "inbox", "--bus", checkout, "--as", "Ada", "--receipt-max-words", "40", "--bodies", "--max-notes", "1", "--max-bytes", "100", "--after", token, "--advance", "--remote", "origin", "--branch", "main").mustCode(t, 0)
	if !strings.Contains(second.stdout, "\nB\n") || strings.Contains(second.stdout, "re-b") || !strings.Contains(second.stdout, "drained=true complete=true next=-") {
		t.Fatalf("continuation did not stay on its original snapshot:\n%s", second.stdout)
	}
}

func TestBodiesReadOnlyContinuationNeedsNoPersistedOpenSnapshot(t *testing.T) {
	checkout, _ := busDir(t)
	invoke(t, "", "inbox", "--bus", checkout, "--as", "Ada", "--receipt-max-words", "40", "--full", "--advance", "--carry-history", "--remote", "origin", "--branch", "main").mustCode(t, 0)
	addBodyCommit(t, checkout, "from-bo/read-only-a.md", "bo-eeeeeeeeeeee", "read-only A")
	addBodyCommit(t, checkout, "from-bo/read-only-b.md", "bo-ffffffffffff", "read-only B")
	first := invoke(t, "", "inbox", "--bus", checkout, "--as", "Ada", "--receipt-max-words", "40", "--bodies", "--max-notes", "1", "--max-bytes", "100").mustCode(t, 0)
	token := bodyNext(t, first.stdout)
	addBodyReply(t, checkout, "from-ada/read-only-re-b.md", "bo-ffffffffffff")
	second := invoke(t, "", "inbox", "--bus", checkout, "--as", "Ada", "--receipt-max-words", "40", "--bodies", "--max-notes", "1", "--max-bytes", "100", "--after", token).mustCode(t, 0)
	if !strings.Contains(second.stdout, "\nread-only B\n") || !strings.Contains(second.stdout, "drained=true complete=true next=-") {
		t.Fatalf("read-only continuation consulted current OPEN instead of C0..H:\n%s", second.stdout)
	}
}

func addBodyCommit(t *testing.T, checkout, path, id, body string) {
	t.Helper()
	writeFile(t, checkout, path, "From: Bo\nTo: Ada\nDate: Mon Sep  7 00:00:00 UTC 2026\nId: "+id+"\nSubject: "+body+"\n\n"+body)
	gitIn(t, checkout, "add", "--", path)
	gitIn(t, checkout, "-c", "user.name=Bo", "-c", "user.email=bo@example.com", "commit", "-q", "-m", "body "+body)
	gitIn(t, checkout, "push", "-q", "origin", "main")
}

func addBodyReply(t *testing.T, checkout, path, target string) {
	t.Helper()
	writeFile(t, checkout, path, "From: Ada\nTo: Bo\nDate: Mon Sep  7 00:00:00 UTC 2026\nSubject: later reply\nRe: "+target+"\n\nlater")
	gitIn(t, checkout, "add", "--", path)
	gitIn(t, checkout, "-c", "user.name=Ada", "-c", "user.email=ada@example.com", "commit", "-q", "-m", "later reply")
	gitIn(t, checkout, "push", "-q", "origin", "main")
}

func bodyNext(t *testing.T, stdout string) string {
	t.Helper()
	for _, field := range strings.Fields(stdout) {
		if token, ok := strings.CutPrefix(field, "next="); ok && token != "-" {
			return token
		}
	}
	t.Fatalf("bodies receipt had no continuation token:\n%s", stdout)
	return ""
}
