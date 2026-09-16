package main

import (
	"bytes"
	"strings"
	"testing"
)

// The S6 slice is the clip slice over the socket: "the tree is in git, and
// others can read it". Its verbs are `snapshot --out <file>` (the session
// writes the published snapshot to a file a reader can point `--snapshot` at)
// and the clip verbs: `clip` itself, which acknowledges at once with the spec's
// OPERATION OK line, and the `operation status|list|wait|cancel` that carry the
// transport to its terminal CLIP OK / CLIP RACED / CLIP FAIL line.
//
// Every constant below instantiates a line of docs/SPEC-WORK.md's output
// grammar exactly once, so the byte-for-byte pass-through tests compare against
// the shape the spec prints and not a shape this package invented. The one
// exception is named: SNAPSHOT OK/FAIL, the snapshot verb's own answer, whose
// fields follow the grammar's laws (the verb's token uppercased, pushed= on the
// scope line, emitted= last) because the spec's verbs fence spells no snapshot
// verb — the S6 client card names it, and docs/CLI.md documents it.

// clipAckLine is the spec's OPERATION OK grammar line (docs/SPEC-WORK.md,
// Output grammar) as `clip` prints it at once: the operation id, op=clip, the
// state at acknowledgement, and the session's rev/pushed beside it.
const clipAckLine = "OPERATION OK id=op-17 op=clip state=queued started=2026-09-16T18:00:00Z updated=2026-09-16T18:00:00Z staged=0 rev=42 pushed=41 shown=0 emitted=186"

// clipOKLine is the spec's CLIP OK line: what `operation wait --id <id>` prints
// when the clip's transport settles, carrying the operation= id, the boundary
// request id, the commit and the clipped revision.
const clipOKLine = "CLIP OK session=/sessions/alpha.sock operation=op-17 boundary=req-8f14 events=3 base=8f14e45fceea commit=1a2b3c4d5e6f pushed=42 attempts=1 emitted=4096"

// clipRacedLine is the spec's CLIP RACED line: the base predicate refused the
// push (tip != base), exit 1, nothing pushed.
const clipRacedLine = "CLIP RACED session=/sessions/alpha.sock operation=op-17 boundary=req-8f14 generation=3 expected=8f14e45fceea found=9c87d2a1b6f3"

// clipFailLine is the spec's CLIP FAIL line: a refused push leaves accepted
// local work and the pending clip intact and reports *locally durable, not
// shared*.
const clipFailLine = "CLIP FAIL session=/sessions/alpha.sock operation=op-17 boundary=req-8f14 events=3 base=8f14e45fceea pushed=- attempts=1: locally durable, not shared"

// operationRunningLine is the OPERATION OK line with the operation mid-flight,
// the one `operation status --id <id>` answers: the same shape the clip
// acknowledged with, at the state the operation has reached.
const operationRunningLine = "OPERATION OK id=op-17 op=clip state=running started=2026-09-16T17:55:00Z updated=2026-09-16T17:59:12Z staged=2048 rev=42 pushed=41 shown=0 emitted=186"

// operationCancellingLine is the OPERATION OK line at state=cancelling: a
// cancellation is a request with its own acknowledgement, never an erasure.
const operationCancellingLine = "OPERATION OK id=op-17 op=clip state=cancelling started=2026-09-16T17:55:00Z updated=2026-09-16T18:00:31Z staged=2048 rev=42 pushed=41 shown=0 emitted=186"

// operationRowLine is the spec's OPERATION ROW line, one row of
// `operation list`.
const operationRowLine = "OPERATION ROW id=op-17 op=clip state=done started=2026-09-16T17:55:00Z updated=2026-09-16T18:00:00Z external=none"

// operationWaitingLine is the spec's OPERATION NOTE line: a wait that timed
// out leaves the operation running, and this line says so — an informational
// token, stdout, exit 0.
const operationWaitingLine = "OPERATION NOTE waiting id=op-17 timeout=30s after=-"

// snapshotOKLine is the snapshot verb's answer: the file written, the revision
// the snapshot carries, the base of the clip that published it and the clipped
// revision, the emitted bytes last.
const snapshotOKLine = "SNAPSHOT OK session=/sessions/alpha.sock out=./snapshots/alpha.sexp rev=42 base=8f14e45fceea pushed=41 emitted=8123"

// snapshotFailLine is the snapshot verb's refusal: a snapshot that would pass
// the session's own bound is refused whole and never truncated, with the spec's
// own remedy spelling from the clip's overflow refusal.
const snapshotFailLine = "SNAPSHOT FAIL session=/sessions/alpha.sock out=./snapshots/alpha.sexp: 9000000 bytes past --max-bytes=1048576, lower --retain or raise --max-bytes"

func TestSnapshotPrintsTheSessionsLineByteForByte(t *testing.T) {
	socket, requests := fakeSession(t, snapshotOKLine)
	var stdout, stderr bytes.Buffer
	code := run([]string{"snapshot", "--session", socket, "--out", "./snapshots/alpha.sexp"}, &stdout, &stderr, "")
	if code != 0 {
		t.Fatalf("snapshot exit = %d, stderr = %s", code, stderr.String())
	}
	if got, want := stdout.String(), snapshotOKLine+"\n"; got != want {
		t.Fatalf("snapshot stdout = %q, want byte for byte %q", got, want)
	}
	if stderr.Len() != 0 {
		t.Fatalf("snapshot wrote stderr: %q", stderr.String())
	}
	if got, want := awaitRequest(t, requests), "snapshot --session "+socket+" --out ./snapshots/alpha.sexp"; got != want {
		t.Fatalf("request line = %q, want %q", got, want)
	}
}

func TestSnapshotRefusalExitsOne(t *testing.T) {
	socket, _ := fakeSession(t, snapshotFailLine)
	var stdout, stderr bytes.Buffer
	code := run([]string{"snapshot", "--session", socket, "--out", "./snapshots/alpha.sexp"}, &stdout, &stderr, "")
	if code != 1 {
		t.Fatalf("snapshot refusal exit = %d, want 1", code)
	}
	if stdout.Len() != 0 {
		t.Fatalf("snapshot refusal wrote stdout: %q", stdout.String())
	}
	if got, want := stderr.String(), snapshotFailLine+"\n"; got != want {
		t.Fatalf("snapshot refusal stderr = %q, want byte for byte %q", got, want)
	}
}

func TestSnapshotRequiresOutAndRefusesToGuess(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"snapshot", "--session", "session.sock"}, &stdout, &stderr, "")
	if code != 2 {
		t.Fatalf("snapshot without --out exit = %d, want 2", code)
	}
	if stdout.Len() != 0 {
		t.Fatalf("snapshot without --out wrote stdout: %q", stdout.String())
	}
	lines := strings.Split(strings.TrimSuffix(stderr.String(), "\n"), "\n")
	if len(lines) != 1 || !strings.HasSuffix(lines[0], "run: nova-work help") {
		t.Fatalf("snapshot without --out refusal = %q, want one line ending \"run: nova-work help\"", stderr.String())
	}
	if !strings.Contains(lines[0], "--out is required") || !strings.Contains(lines[0], "refusing to guess") {
		t.Fatalf("snapshot without --out refusal = %q, want it naming --out and refusing to guess", lines[0])
	}
}

func TestClipPrintsTheOperationAckAtOnceAndExits(t *testing.T) {
	socket, requests := fakeSession(t, clipAckLine)
	var stdout, stderr bytes.Buffer
	code := run([]string{"clip", "--session", socket, "--as", "rowan", "--git-timeout", "30"}, &stdout, &stderr, "")
	if code != 0 {
		t.Fatalf("clip exit = %d, stderr = %s", code, stderr.String())
	}
	if got, want := stdout.String(), clipAckLine+"\n"; got != want {
		t.Fatalf("clip stdout = %q, want byte for byte %q", got, want)
	}
	if stderr.Len() != 0 {
		t.Fatalf("clip wrote stderr: %q", stderr.String())
	}
	if got, want := awaitRequest(t, requests), "clip --session "+socket+" --as rowan --git-timeout 30"; got != want {
		t.Fatalf("request line = %q, want %q", got, want)
	}
}

func TestClipSpellsItsWholeFlagSurface(t *testing.T) {
	socket, requests := fakeSession(t, clipAckLine)
	var stdout, stderr bytes.Buffer
	code := run([]string{"clip", "--session", socket, "--as", "Rowan Jr", "--git-timeout", "45", "--attempts", "3", "--max", "20", "--now", "2026-09-16T18:00:00Z"}, &stdout, &stderr, "")
	if code != 0 || stderr.Len() != 0 {
		t.Fatalf("clip exit=%d stderr=%q", code, stderr.String())
	}
	want := "clip --session " + socket + " --as Rowan\\x20Jr --git-timeout 45 --attempts 3 --max 20 --now 2026-09-16T18:00:00Z"
	if got := awaitRequest(t, requests); got != want {
		t.Fatalf("clip request line = %q, want %q", got, want)
	}
}

func TestOperationWaitPrintsTheClipOKLineByteForByte(t *testing.T) {
	socket, requests := fakeSession(t, clipOKLine)
	var stdout, stderr bytes.Buffer
	code := run([]string{"operation", "wait", "--session", socket, "--id", "op-17", "--timeout", "30s", "--after", "cursor-1"}, &stdout, &stderr, "")
	if code != 0 {
		t.Fatalf("operation wait exit = %d, stderr = %s", code, stderr.String())
	}
	if got, want := stdout.String(), clipOKLine+"\n"; got != want {
		t.Fatalf("operation wait stdout = %q, want byte for byte %q", got, want)
	}
	if stderr.Len() != 0 {
		t.Fatalf("operation wait wrote stderr: %q", stderr.String())
	}
	want := "operation wait --session " + socket + " --id op-17 --timeout 30s --after cursor-1"
	if got := awaitRequest(t, requests); got != want {
		t.Fatalf("request line = %q, want %q", got, want)
	}
}

func TestOperationWaitRacedExitsOne(t *testing.T) {
	socket, _ := fakeSession(t, clipRacedLine)
	var stdout, stderr bytes.Buffer
	code := run([]string{"operation", "wait", "--session", socket, "--id", "op-17", "--timeout", "30s"}, &stdout, &stderr, "")
	if code != 1 {
		t.Fatalf("raced wait exit = %d, want 1", code)
	}
	if stdout.Len() != 0 {
		t.Fatalf("raced wait wrote stdout: %q", stdout.String())
	}
	if got, want := stderr.String(), clipRacedLine+"\n"; got != want {
		t.Fatalf("raced wait stderr = %q, want byte for byte %q", got, want)
	}
}

func TestOperationWaitFailExitsOne(t *testing.T) {
	socket, _ := fakeSession(t, clipFailLine)
	var stdout, stderr bytes.Buffer
	code := run([]string{"operation", "wait", "--session", socket, "--id", "op-17", "--timeout", "30s"}, &stdout, &stderr, "")
	if code != 1 {
		t.Fatalf("failed wait exit = %d, want 1", code)
	}
	if stdout.Len() != 0 {
		t.Fatalf("failed wait wrote stdout: %q", stdout.String())
	}
	if got, want := stderr.String(), clipFailLine+"\n"; got != want {
		t.Fatalf("failed wait stderr = %q, want byte for byte %q", got, want)
	}
}

func TestOperationWaitTimeoutPrintsTheNoteLineAndExitsZero(t *testing.T) {
	// A wait that times out leaves the operation running: the NOTE line is the
	// session saying so, an informational token to stdout at exit 0, never a
	// failure the caller did not have.
	socket, _ := fakeSession(t, operationWaitingLine)
	var stdout, stderr bytes.Buffer
	code := run([]string{"operation", "wait", "--session", socket, "--id", "op-17", "--timeout", "30s"}, &stdout, &stderr, "")
	if code != 0 {
		t.Fatalf("timed-out wait exit = %d, want 0 (stderr %q)", code, stderr.String())
	}
	if got, want := stdout.String(), operationWaitingLine+"\n"; got != want {
		t.Fatalf("timed-out wait stdout = %q, want byte for byte %q", got, want)
	}
	if stderr.Len() != 0 {
		t.Fatalf("timed-out wait wrote stderr: %q", stderr.String())
	}
}

func TestOperationStatusPrintsTheStateLineByteForByte(t *testing.T) {
	socket, requests := fakeSession(t, operationRunningLine)
	var stdout, stderr bytes.Buffer
	code := run([]string{"operation", "status", "--session", socket, "--id", "op-17"}, &stdout, &stderr, "")
	if code != 0 {
		t.Fatalf("operation status exit = %d, stderr = %s", code, stderr.String())
	}
	if got, want := stdout.String(), operationRunningLine+"\n"; got != want {
		t.Fatalf("operation status stdout = %q, want byte for byte %q", got, want)
	}
	if stderr.Len() != 0 {
		t.Fatalf("operation status wrote stderr: %q", stderr.String())
	}
	if got, want := awaitRequest(t, requests), "operation status --session "+socket+" --id op-17"; got != want {
		t.Fatalf("request line = %q, want %q", got, want)
	}
}

func TestOperationListPrintsTheRowLineByteForByte(t *testing.T) {
	socket, requests := fakeSession(t, operationRowLine)
	var stdout, stderr bytes.Buffer
	code := run([]string{"operation", "list", "--session", socket, "--max", "20"}, &stdout, &stderr, "")
	if code != 0 {
		t.Fatalf("operation list exit = %d, stderr = %s", code, stderr.String())
	}
	if got, want := stdout.String(), operationRowLine+"\n"; got != want {
		t.Fatalf("operation list stdout = %q, want byte for byte %q", got, want)
	}
	if stderr.Len() != 0 {
		t.Fatalf("operation list wrote stderr: %q", stderr.String())
	}
	if got, want := awaitRequest(t, requests), "operation list --session "+socket+" --max 20"; got != want {
		t.Fatalf("request line = %q, want %q", got, want)
	}
}

func TestOperationCancelSpellsItsRequestLine(t *testing.T) {
	// A cancellation is a request with its own acknowledgement: it takes the
	// write flags like every other request, and the session answers the
	// OPERATION OK line at state=cancelling.
	socket, requests := fakeSession(t, operationCancellingLine)
	var stdout, stderr bytes.Buffer
	code := run([]string{"operation", "cancel", "--session", socket, "--as", "rowan", "--request", "req-9", "--id", "op-17", "--reason", "raced by hand edit"}, &stdout, &stderr, "")
	if code != 0 {
		t.Fatalf("operation cancel exit = %d, stderr = %s", code, stderr.String())
	}
	if got, want := stdout.String(), operationCancellingLine+"\n"; got != want {
		t.Fatalf("operation cancel stdout = %q, want byte for byte %q", got, want)
	}
	want := "operation cancel --session " + socket + " --as rowan --request req-9 --id op-17 --reason raced\\x20by\\x20hand\\x20edit"
	if got := awaitRequest(t, requests); got != want {
		t.Fatalf("operation cancel request line = %q, want %q", got, want)
	}

	// The write flags' optional spellings travel as given, --dry-run as a bool.
	stdout.Reset()
	stderr.Reset()
	code = run([]string{"operation", "cancel", "--session", socket, "--as", "rowan", "--expect", "42", "--now", "2026-09-16T18:00:00Z", "--deadline", "2026-09-16T18:01:00Z", "--dry-run", "--id", "op-17", "--reason", "raced by hand edit"}, &stdout, &stderr, "")
	if code != 0 || stderr.Len() != 0 {
		t.Fatalf("operation cancel --dry-run exit=%d stderr=%q", code, stderr.String())
	}
	want = "operation cancel --session " + socket + " --as rowan --expect 42 --now 2026-09-16T18:00:00Z --deadline 2026-09-16T18:01:00Z --dry-run true --id op-17 --reason raced\\x20by\\x20hand\\x20edit"
	if got := awaitRequest(t, requests); got != want {
		t.Fatalf("operation cancel --dry-run request line = %q, want %q", got, want)
	}
}

func TestS6VerbsRefuseWithoutSession(t *testing.T) {
	for _, args := range [][]string{
		{"snapshot", "--out", "./snapshots/alpha.sexp"},
		{"clip", "--as", "rowan"},
		{"operation", "wait", "--id", "op-17"},
	} {
		t.Run(args[0], func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := run(args, &stdout, &stderr, "")
			if code != 2 {
				t.Fatalf("%s without --session exit = %d, want 2", args[0], code)
			}
			if stdout.Len() != 0 {
				t.Fatalf("%s without --session wrote stdout: %q", args[0], stdout.String())
			}
			lines := strings.Split(strings.TrimSuffix(stderr.String(), "\n"), "\n")
			if len(lines) != 1 || !strings.HasSuffix(lines[0], "run: nova-work help") {
				t.Fatalf("%s without --session refusal = %q, want one line ending \"run: nova-work help\"", args[0], stderr.String())
			}
			if !strings.Contains(lines[0], "refusing to guess") {
				t.Fatalf("%s without --session refusal = %q, want it naming the missing --session", args[0], lines[0])
			}
		})
	}
}

func TestOperationRefusesUnknownSubverb(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"operation without a subverb", []string{"operation"}, "status, list, wait or cancel"},
		{"unknown operation verb", []string{"operation", "bogus"}, "unknown operation verb"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := run(c.args, &stdout, &stderr, "")
			if code != 2 {
				t.Fatalf("exit = %d, want 2 (stderr %q)", code, stderr.String())
			}
			if stdout.Len() != 0 {
				t.Fatalf("refusal wrote stdout: %q", stdout.String())
			}
			if !strings.Contains(stderr.String(), c.want) {
				t.Fatalf("refusal %q does not name %q", stderr.String(), c.want)
			}
		})
	}
}

func TestHelpListsTheS6Verbs(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"help"}, &stdout, &stderr, "")
	if code != 0 {
		t.Fatalf("help exit = %d, stderr = %s", code, stderr.String())
	}
	help := stdout.String()
	for _, v := range []string{"snapshot", "clip", "operation status", "operation list", "operation wait", "operation cancel"} {
		if !strings.Contains(help, "nova-work "+v) {
			t.Errorf("help does not list %q", v)
		}
	}
}
