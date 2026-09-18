package main

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

// queryFriendsOK is a representative QUERY OK response for --ask friends
// that the fake session returns, matching the spec's grammar line at
// docs/SPEC-WORK.md: QUERY OK ask=<kind> ...
const queryFriendsOK = "QUERY OK ask=friends scope=17 membership=all branch=open unit=leaves source=9f2c1a7e freshest=2026-09-16T12:00:00Z done=3 done-unverified=0 unknown=1 deferred=0 cancelled=0 superseded=0 stale=0 required=8 since-baseline=0 private=0 open=5 closed=3 gap=0 leases=2 responsible=- pushed=15 rows=3 shown=2 pages=1 parses=0 replays=0 emitted=412"

// queryFriendsRow is a representative QUERY ROW for friends data.
const queryFriendsRow = "QUERY ROW ada kind=friend state=idle responsible=ada holder=unowned heartbeat=none deadline=- escalated-to=- blocked-by=-"

func TestQueryFriendsOverSocket(t *testing.T) {
	reply := queryFriendsOK
	socket, requests := fakeSession(t, reply)

	var stdout, stderr bytes.Buffer
	code := run([]string{"query", "--session", socket, "--ask", "friends", "--branch", "open"}, strings.NewReader(""), &stdout, &stderr, time.Now().UTC())
	if code != 0 {
		t.Fatalf("query friends exit = %d, stderr = %s", code, stderr.String())
	}
	if got, want := stdout.String(), queryFriendsOK+"\n"; got != want {
		t.Fatalf("query friends stdout = %q, want %q", got, want)
	}
	if stderr.Len() != 0 {
		t.Fatalf("query friends wrote stderr: %q", stderr.String())
	}
	if got, want := awaitRequest(t, requests), "query --ask friends --branch open --session "+socket; got != want {
		t.Fatalf("request line = %q, want %q", got, want)
	}
}

func TestQueryFriendsWithOwner(t *testing.T) {
	reply := queryFriendsOK
	socket, requests := fakeSession(t, reply)

	var stdout, stderr bytes.Buffer
	code := run([]string{"query", "--session", socket, "--ask", "friends", "--branch", "open", "--owner", "Rowan Jr"}, strings.NewReader(""), &stdout, &stderr, time.Now().UTC())
	if code != 0 {
		t.Fatalf("query friends --owner exit = %d, stderr = %s", code, stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("query friends --owner wrote stderr: %q", stderr.String())
	}
	if got, want := awaitRequest(t, requests), "query --ask friends --branch open --owner Rowan\\x20Jr --session "+socket; got != want {
		t.Fatalf("request line = %q, want %q", got, want)
	}
}

func TestQueryFriendsWithRepo(t *testing.T) {
	reply := queryFriendsOK
	socket, requests := fakeSession(t, reply)

	var stdout, stderr bytes.Buffer
	code := run([]string{"query", "--session", socket, "--ask", "friends", "--branch", "open", "--repo", "mas-bandwidth/nova-tools"}, strings.NewReader(""), &stdout, &stderr, time.Now().UTC())
	if code != 0 {
		t.Fatalf("query friends --repo exit = %d, stderr = %s", code, stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("query friends --repo wrote stderr: %q", stderr.String())
	}
	if got, want := awaitRequest(t, requests), "query --ask friends --branch open --repo mas-bandwidth/nova-tools --session "+socket; got != want {
		t.Fatalf("request line = %q, want %q", got, want)
	}
}

func TestQueryFriendsWithMax(t *testing.T) {
	reply := queryFriendsOK
	socket, requests := fakeSession(t, reply)

	var stdout, stderr bytes.Buffer
	code := run([]string{"query", "--session", socket, "--ask", "friends", "--branch", "open", "--max", "50"}, strings.NewReader(""), &stdout, &stderr, time.Now().UTC())
	if code != 0 {
		t.Fatalf("query friends --max exit = %d, stderr = %s", code, stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("query friends --max wrote stderr: %q", stderr.String())
	}
	if got, want := awaitRequest(t, requests), "query --ask friends --branch open --max 50 --session "+socket; got != want {
		t.Fatalf("request line = %q, want %q", got, want)
	}
}

func TestQueryRefusesWithoutAsk(t *testing.T) {
	socket, _ := fakeSession(t, queryFriendsOK)
	var stdout, stderr bytes.Buffer
	code := run([]string{"query", "--session", socket, "--branch", "open"}, strings.NewReader(""), &stdout, &stderr, time.Now().UTC())
	if code != 2 {
		t.Fatalf("query without --ask exit = %d, want 2, stderr = %s", code, stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("query without --ask wrote stdout: %q", stdout.String())
	}
	if !strings.Contains(stderr.String(), "--ask is required") {
		t.Fatalf("refusal = %q, want it naming --ask is required", stderr.String())
	}
}

func TestQueryRefusesWithoutBranch(t *testing.T) {
	socket, _ := fakeSession(t, queryFriendsOK)
	var stdout, stderr bytes.Buffer
	code := run([]string{"query", "--session", socket, "--ask", "friends"}, strings.NewReader(""), &stdout, &stderr, time.Now().UTC())
	if code != 2 {
		t.Fatalf("query without --branch exit = %d, want 2, stderr = %s", code, stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("query without --branch wrote stdout: %q", stdout.String())
	}
	if !strings.Contains(stderr.String(), "--branch is required") {
		t.Fatalf("refusal = %q, want it naming --branch is required", stderr.String())
	}
}

func TestQueryRefusesInvalidAskKind(t *testing.T) {
	socket, _ := fakeSession(t, queryFriendsOK)
	var stdout, stderr bytes.Buffer
	code := run([]string{"query", "--session", socket, "--ask", "bogus", "--branch", "open"}, strings.NewReader(""), &stdout, &stderr, time.Now().UTC())
	if code != 2 {
		t.Fatalf("query invalid --ask exit = %d, want 2, stderr = %s", code, stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("query invalid --ask wrote stdout: %q", stdout.String())
	}
	if !strings.Contains(stderr.String(), "not one of the known kinds") {
		t.Fatalf("refusal = %q, want it naming not one of the known kinds", stderr.String())
	}
}

func TestQueryRefusesInvalidBranch(t *testing.T) {
	socket, _ := fakeSession(t, queryFriendsOK)
	var stdout, stderr bytes.Buffer
	code := run([]string{"query", "--session", socket, "--ask", "friends", "--branch", "bogus"}, strings.NewReader(""), &stdout, &stderr, time.Now().UTC())
	if code != 2 {
		t.Fatalf("query invalid --branch exit = %d, want 2, stderr = %s", code, stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("query invalid --branch wrote stdout: %q", stdout.String())
	}
	if !strings.Contains(stderr.String(), "is not one of open, closed, root") {
		t.Fatalf("refusal = %q, want it naming the valid branches", stderr.String())
	}
}

func TestQueryRefusesSessionAndSnapshotTogether(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"query", "--session", "a.sock", "--snapshot", "b.sexp", "--max-bytes", "1000", "--max-depth", "10", "--max-nodes", "100", "--cache", "c.cache", "--ask", "friends", "--branch", "open"}, strings.NewReader(""), &stdout, &stderr, time.Now().UTC())
	if code != 2 {
		t.Fatalf("query --session and --snapshot exit = %d, want 2, stderr = %s", code, stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("query --session and --snapshot wrote stdout: %q", stdout.String())
	}
	if !strings.Contains(stderr.String(), "mutually exclusive") {
		t.Fatalf("refusal = %q, want it naming mutually exclusive", stderr.String())
	}
}

func TestQueryRefusesSnapshotWithoutBounds(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"query", "--snapshot", "snap.sexp", "--ask", "friends", "--branch", "open", "--cache", "c.cache"}, strings.NewReader(""), &stdout, &stderr, time.Now().UTC())
	if code != 2 {
		t.Fatalf("query snapshot without bounds exit = %d, want 2, stderr = %s", code, stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("query snapshot without bounds wrote stdout: %q", stdout.String())
	}
	if !strings.Contains(stderr.String(), "--snapshot requires --max-bytes") {
		t.Fatalf("refusal = %q, want it naming --snapshot requires --max-bytes", stderr.String())
	}
}

func TestQueryRefusesBranchOpenWithFromTo(t *testing.T) {
	socket, _ := fakeSession(t, queryFriendsOK)
	var stdout, stderr bytes.Buffer
	code := run([]string{"query", "--session", socket, "--ask", "friends", "--branch", "open", "--from", "2026-09-01T00:00:00Z", "--to", "2026-09-14T00:00:00Z"}, strings.NewReader(""), &stdout, &stderr, time.Now().UTC())
	if code != 2 {
		t.Fatalf("query --branch open with --from/--to exit = %d, want 2, stderr = %s", code, stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("query --branch open with --from/--to wrote stdout: %q", stdout.String())
	}
	if !strings.Contains(stderr.String(), "refused under --branch open") {
		t.Fatalf("refusal = %q, want it naming refused under --branch open", stderr.String())
	}
}

func TestQueryRefusesBranchClosedWithoutFromTo(t *testing.T) {
	socket, _ := fakeSession(t, queryFriendsOK)
	var stdout, stderr bytes.Buffer
	code := run([]string{"query", "--session", socket, "--ask", "done", "--branch", "closed"}, strings.NewReader(""), &stdout, &stderr, time.Now().UTC())
	if code != 2 {
		t.Fatalf("query --branch closed without --from/--to exit = %d, want 2, stderr = %s", code, stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("query --branch closed without --from/--to wrote stdout: %q", stdout.String())
	}
	if !strings.Contains(stderr.String(), "requires --from and --to") {
		t.Fatalf("refusal = %q, want it naming requires --from and --to", stderr.String())
	}
}

func TestQueryRefusesWhoWithoutWindow(t *testing.T) {
	socket, _ := fakeSession(t, queryFriendsOK)
	var stdout, stderr bytes.Buffer
	code := run([]string{"query", "--session", socket, "--ask", "who", "--branch", "open"}, strings.NewReader(""), &stdout, &stderr, time.Now().UTC())
	if code != 2 {
		t.Fatalf("query who without --window exit = %d, want 2, stderr = %s", code, stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("query who without --window wrote stdout: %q", stdout.String())
	}
	if !strings.Contains(stderr.String(), "--ask who requires --window") {
		t.Fatalf("refusal = %q, want it naming --ask who requires --window", stderr.String())
	}
}

func TestQueryRefusesStaleWithoutWindow(t *testing.T) {
	socket, _ := fakeSession(t, queryFriendsOK)
	var stdout, stderr bytes.Buffer
	code := run([]string{"query", "--session", socket, "--ask", "stale", "--branch", "open"}, strings.NewReader(""), &stdout, &stderr, time.Now().UTC())
	if code != 2 {
		t.Fatalf("query stale without --window exit = %d, want 2, stderr = %s", code, stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("query stale without --window wrote stdout: %q", stdout.String())
	}
	if !strings.Contains(stderr.String(), "--ask stale requires --window") {
		t.Fatalf("refusal = %q, want it naming --ask stale requires --window", stderr.String())
	}
}

func TestQueryRefusesOrderPriorityOnNonReady(t *testing.T) {
	socket, _ := fakeSession(t, queryFriendsOK)
	var stdout, stderr bytes.Buffer
	code := run([]string{"query", "--session", socket, "--ask", "friends", "--branch", "open", "--order", "priority"}, strings.NewReader(""), &stdout, &stderr, time.Now().UTC())
	if code != 2 {
		t.Fatalf("query --order priority on friends exit = %d, want 2, stderr = %s", code, stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("query --order priority on friends wrote stdout: %q", stdout.String())
	}
	if !strings.Contains(stderr.String(), "only --ask ready admits it") {
		t.Fatalf("refusal = %q, want it naming only --ask ready admits it", stderr.String())
	}
}

func TestQueryWhoBranchOpenOnly(t *testing.T) {
	socket, _ := fakeSession(t, queryFriendsOK)
	var stdout, stderr bytes.Buffer
	code := run([]string{"query", "--session", socket, "--ask", "who", "--branch", "closed", "--window", "5m", "--from", "2026-09-01T00:00:00Z", "--to", "2026-09-14T00:00:00Z"}, strings.NewReader(""), &stdout, &stderr, time.Now().UTC())
	if code != 2 {
		t.Fatalf("query who --branch closed exit = %d, want 2, stderr = %s", code, stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("query who --branch closed wrote stdout: %q", stdout.String())
	}
	if !strings.Contains(stderr.String(), "--branch closed is refused") {
		t.Fatalf("refusal = %q, want it naming --branch open only", stderr.String())
	}
}

func TestQueryOrderPriorityOnReady(t *testing.T) {
	reply := queryFriendsOK
	socket, requests := fakeSession(t, reply)

	var stdout, stderr bytes.Buffer
	code := run([]string{"query", "--session", socket, "--ask", "ready", "--branch", "open", "--order", "priority"}, strings.NewReader(""), &stdout, &stderr, time.Now().UTC())
	if code != 0 {
		t.Fatalf("query ready --order priority exit = %d, stderr = %s", code, stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("query ready --order priority wrote stderr: %q", stderr.String())
	}
	if got := awaitRequest(t, requests); !strings.Contains(got, "--order priority") {
		t.Fatalf("request line = %q, want it containing --order priority", got)
	}
}

func TestQueryAxisRefusedOnNonPercent(t *testing.T) {
	socket, _ := fakeSession(t, queryFriendsOK)
	var stdout, stderr bytes.Buffer
	code := run([]string{"query", "--session", socket, "--ask", "friends", "--branch", "open", "--axis", "rowan"}, strings.NewReader(""), &stdout, &stderr, time.Now().UTC())
	if code != 2 {
		t.Fatalf("query --axis on friends exit = %d, want 2, stderr = %s", code, stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("query --axis on friends wrote stdout: %q", stdout.String())
	}
	if !strings.Contains(stderr.String(), "--axis is refused on --ask friends") {
		t.Fatalf("refusal = %q, want it naming --axis is refused", stderr.String())
	}
}

func TestQueryHandoffsBranchOpenOnly(t *testing.T) {
	socket, _ := fakeSession(t, queryFriendsOK)
	var stdout, stderr bytes.Buffer
	code := run([]string{"query", "--session", socket, "--ask", "handoffs", "--branch", "root", "--from", "2026-09-01T00:00:00Z", "--to", "2026-09-14T00:00:00Z"}, strings.NewReader(""), &stdout, &stderr, time.Now().UTC())
	if code != 2 {
		t.Fatalf("query handoffs --branch root exit = %d, want 2, stderr = %s", code, stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("query handoffs --branch root wrote stdout: %q", stdout.String())
	}
	if !strings.Contains(stderr.String(), "is --branch open only") {
		t.Fatalf("refusal = %q, want it naming --branch open only", stderr.String())
	}
}

func TestQueryWithoutSessionOrSnapshot(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"query", "--ask", "friends", "--branch", "open"}, strings.NewReader(""), &stdout, &stderr, time.Now().UTC())
	if code != 2 {
		t.Fatalf("query without --session or --snapshot exit = %d, want 2, stderr = %s", code, stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("query without --session or --snapshot wrote stdout: %q", stdout.String())
	}
	if !strings.Contains(stderr.String(), "--session or --snapshot is required") {
		t.Fatalf("refusal = %q, want it naming --session or --snapshot is required", stderr.String())
	}
}

func TestHelpListsQuery(t *testing.T) {
	verbs := switchVerbs(t)
	found := false
	for _, v := range verbs {
		if v == "query" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("help does not list the switch's verb %q", "query")
	}
}

func TestQueryEscalationsRefusesAsk(t *testing.T) {
	// "escalations" is not a valid --ask kind for nova-work query; it
	// belongs to nova-pulse. The client refuses it at exit 2.
	socket, _ := fakeSession(t, queryFriendsOK)
	var stdout, stderr bytes.Buffer
	code := run([]string{"query", "--session", socket, "--ask", "escalations", "--branch", "open"}, strings.NewReader(""), &stdout, &stderr, time.Now().UTC())
	if code != 2 {
		t.Fatalf("query escalations exit = %d, want 2, stderr = %s", code, stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("query escalations wrote stdout: %q", stdout.String())
	}
	if !strings.Contains(stderr.String(), "not one of the known kinds") {
		t.Fatalf("refusal = %q, want it naming not one of the known kinds", stderr.String())
	}
}
