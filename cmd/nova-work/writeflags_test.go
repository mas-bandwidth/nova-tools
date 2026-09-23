package main

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

// The write flags' two promises the client can break, driven through run()
// against a session on a real Unix socket (fakeSession), so the request line,
// the number of requests and the verdict are the ones a caller gets. The
// engine's own zero-event bookkeeping lives in the session and is the Lisp
// suite's to prove; what these tests pin is the boundary: the flag reaches
// the session spelled as the wire spells it, exactly one request crosses the
// socket, the client writes nothing of its own, and the session's answer is
// passed through byte for byte with the spec's exit (docs/SPEC-WORK.md,
// "The verbs": <write flags>, --dry-run, --expect and the replay path).

// sessionRequests runs one client verb against a fake session answering
// reply, and returns the exit, stdout, stderr and every request line that
// reached the session. run returns only after the reply is read, and the fake
// records a request before it answers, so the channel holds every request the
// run sent by the time run returns: a second write would be counted here.
func sessionRequests(t *testing.T, reply string, args ...string) (code int, stdout, stderr string, requests []string) {
	t.Helper()
	socket, ch := fakeSession(t, reply)
	argv := make([]string, len(args))
	for i, a := range args {
		if a == "S" {
			a = socket
		}
		argv[i] = a
	}
	var out, errb bytes.Buffer
	code = run(argv, &out, &errb, "")
	for len(ch) > 0 {
		requests = append(requests, <-ch)
	}
	return code, out.String(), errb.String(), requests
}

// onlyTheSocket fails if the client left anything in its working directory
// beyond the session's own socket: a dry run writes nothing, locally either.
func onlyTheSocket(t *testing.T) {
	t.Helper()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() != "session.sock" {
			t.Fatalf("the client wrote %q in its working directory", e.Name())
		}
	}
}

func hasFlag(request, flag, value string) bool {
	fields := strings.Fields(request)
	for i := 0; i+1 < len(fields); i++ {
		if fields[i] == flag && fields[i+1] == value {
			return true
		}
	}
	return false
}

// A dry run crosses the socket once, marked --dry-run true beside the
// --expect it validates against, prints the session's projected receipt at
// exit 0, and sends no second (applying) request. The control: the same verb
// without --dry-run carries no dry-run token, so a client that dropped the
// switch would send a real write and fail the first assertion.
func TestDryRunValidatesButWritesNothing(t *testing.T) {
	receipt := "STATE OK request=r-1 node=E01.11 rev=41 pushed=4 changed=0 dry-run=true emitted=0"
	code, stdout, stderr, requests := sessionRequests(t, receipt,
		"state", "--session", "S", "--as", "Rowan", "--request", "r-1", "--expect", "41",
		"--dry-run", "--node", "E01.11", "--to", "doing", "--reason", "preview")
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr %q)", code, stderr)
	}
	if len(requests) != 1 {
		t.Fatalf("%d requests reached the session, want exactly 1 (a dry run never follows itself with an apply): %q", len(requests), requests)
	}
	if !hasFlag(requests[0], "--dry-run", "true") {
		t.Fatalf("request = %q, want it carrying --dry-run true", requests[0])
	}
	if !hasFlag(requests[0], "--expect", "41") {
		t.Fatalf("request = %q, want the dry run validated against --expect 41", requests[0])
	}
	if stdout != receipt+"\n" || stderr != "" {
		t.Fatalf("stdout = %q stderr = %q, want the receipt byte for byte on stdout alone", stdout, stderr)
	}
	onlyTheSocket(t)

	_, _, _, applied := sessionRequests(t, "STATE OK request=r-1 node=E01.11 rev=42 pushed=4 changed=1",
		"state", "--session", "S", "--as", "Rowan", "--request", "r-1", "--expect", "41",
		"--node", "E01.11", "--to", "doing", "--reason", "apply")
	if len(applied) != 1 || strings.Contains(applied[0], "--dry-run") {
		t.Fatalf("apply requests = %q, want one request with no --dry-run token", applied)
	}
}

// A stale --expect is the session's refusal and the client's exit 1: the
// request carried the caller's revision, the FAIL line naming expect= and
// current= goes to stderr byte for byte, nothing goes to stdout, and the
// client does not retry with the current revision on its own -- the
// requester re-reads and resubmits.
func TestExpectStaleRefusal(t *testing.T) {
	refusal := "STATE FAIL node=E01.11 expect=3 current=5: stale"
	code, stdout, stderr, requests := sessionRequests(t, refusal,
		"state", "--session", "S", "--as", "Rowan", "--expect", "3",
		"--node", "E01.11", "--to", "doing", "--reason", "late")
	if code != 1 {
		t.Fatalf("exit = %d, want 1 (stderr %q)", code, stderr)
	}
	if len(requests) != 1 {
		t.Fatalf("%d requests reached the session, want exactly 1 (no blind retry at current=): %q", len(requests), requests)
	}
	if !hasFlag(requests[0], "--expect", "3") {
		t.Fatalf("request = %q, want it carrying --expect 3", requests[0])
	}
	if stdout != "" {
		t.Fatalf("a stale refusal wrote stdout: %q", stdout)
	}
	if stderr != refusal+"\n" {
		t.Fatalf("stderr = %q, want %q byte for byte", stderr, refusal+"\n")
	}
}

// On the replay path the expectation is checked per node: `session replay`
// sends the bundle as one request, and a node that moved since the bundle's
// clipped revision refuses it `<MUTATION> FAIL node=<id> expect=<rev>
// current=<rev>: stale`. The client passes the per-node refusal through at
// exit 1 and sends nothing after it.
func TestReplayPerNodeStaleCheck(t *testing.T) {
	refusal := "STATE FAIL node=E01.11 expect=4 current=6: stale"
	code, stdout, stderr, requests := sessionRequests(t, refusal,
		"session", "replay", "--session", "S", "--from", "bundle.sexp", "--as", "Rowan")
	if code != 1 {
		t.Fatalf("exit = %d, want 1 (stderr %q)", code, stderr)
	}
	if len(requests) != 1 {
		t.Fatalf("%d requests reached the session, want exactly 1: %q", len(requests), requests)
	}
	if !strings.HasPrefix(requests[0], "session replay ") || !hasFlag(requests[0], "--from", "bundle.sexp") {
		t.Fatalf("request = %q, want a session replay naming --from bundle.sexp", requests[0])
	}
	if stdout != "" {
		t.Fatalf("a per-node stale refusal wrote stdout: %q", stdout)
	}
	if stderr != refusal+"\n" {
		t.Fatalf("stderr = %q, want %q byte for byte", stderr, refusal+"\n")
	}
}
