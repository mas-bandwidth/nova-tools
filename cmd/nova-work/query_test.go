package main

import (
	"bytes"
	"strings"
	"testing"
)

const querySizeOKLine = "QUERY OK ask=size membership=required branch=open unit=leaves source=8f14e45f freshest=2026-09-16T18:00:00Z done=3 done-unverified=0 unknown=1 deferred=0 cancelled=0 superseded=0 stale=0 required=5 since-baseline=0 private=0 open=5 closed=0 gap=0 responsible=rowan rows=0 shown=5 parses=0 replays=0 emitted=612"

func TestQuerySizeSendsTheCorrectRequestLine(t *testing.T) {
	socket, requests := fakeSession(t, querySizeOKLine)
	var stdout, stderr bytes.Buffer
	code := run([]string{"query", "size", "--session", socket}, &stdout, &stderr, "")
	if code != 0 {
		t.Fatalf("query size exit = %d, stderr = %s", code, stderr.String())
	}
	if got, want := stdout.String(), querySizeOKLine+"\n"; got != want {
		t.Fatalf("query size stdout = %q, want %q", got, want)
	}
	if stderr.Len() != 0 {
		t.Fatalf("query size wrote stderr: %q", stderr.String())
	}
	if got, want := awaitRequest(t, requests), "query size --session "+socket; got != want {
		t.Fatalf("request line = %q, want %q", got, want)
	}
}

func TestQuerySizeWithNode(t *testing.T) {
	socket, requests := fakeSession(t, querySizeOKLine)
	var stdout, stderr bytes.Buffer
	code := run([]string{"query", "size", "--session", socket, "--node", "nova-tools/core"}, &stdout, &stderr, "")
	if code != 0 {
		t.Fatalf("query size --node exit = %d, stderr = %s", code, stderr.String())
	}
	if got, want := awaitRequest(t, requests), "query size --session "+socket+" --node nova-tools/core"; got != want {
		t.Fatalf("request line = %q, want %q", got, want)
	}
}

const queryRemainingOKLine = "QUERY OK ask=remaining membership=required branch=open unit=leaves source=8f14e45f freshest=2026-09-16T18:00:00Z done=0 done-unverified=0 unknown=2 deferred=1 cancelled=0 superseded=0 stale=0 required=5 since-baseline=0 private=0 open=5 closed=0 gap=0 responsible=rowan rows=0 shown=5 parses=0 replays=0 emitted=612"

func TestQueryRemainingSendsTheCorrectRequestLine(t *testing.T) {
	socket, requests := fakeSession(t, queryRemainingOKLine)
	var stdout, stderr bytes.Buffer
	code := run([]string{"query", "remaining", "--session", socket}, &stdout, &stderr, "")
	if code != 0 {
		t.Fatalf("query remaining exit = %d, stderr = %s", code, stderr.String())
	}
	if got, want := stdout.String(), queryRemainingOKLine+"\n"; got != want {
		t.Fatalf("query remaining stdout = %q, want %q", got, want)
	}
	if got, want := awaitRequest(t, requests), "query remaining --session "+socket; got != want {
		t.Fatalf("request line = %q, want %q", got, want)
	}
}

const queryReadyOKLine = "QUERY OK ask=ready membership=required branch=open unit=leaves source=8f14e45f freshest=2026-09-16T18:00:00Z done=0 done-unverified=0 unknown=1 deferred=0 cancelled=0 superseded=0 stale=0 required=3 since-baseline=0 private=0 open=3 closed=0 gap=0 responsible=rowan rows=0 shown=3 parses=0 replays=0 emitted=612"
const queryReadyRowLine = "QUERY ROW nova-tools/core/deploy branch=open disposition=pending repo=mas-bandwidth/nova-tools kind=task state=unknown blocker=nova-tools/core/auth reason=blocked-by-dependency resolver=emma"

func TestQueryReadySendsTheCorrectRequestLine(t *testing.T) {
	socket, requests := fakeSession(t, queryReadyOKLine)
	var stdout, stderr bytes.Buffer
	code := run([]string{"query", "ready", "--session", socket}, &stdout, &stderr, "")
	if code != 0 {
		t.Fatalf("query ready exit = %d, stderr = %s", code, stderr.String())
	}
	if got, want := stdout.String(), queryReadyOKLine+"\n"; got != want {
		t.Fatalf("query ready stdout = %q, want %q", got, want)
	}
	if got, want := awaitRequest(t, requests), "query ready --session "+socket; got != want {
		t.Fatalf("request line = %q, want %q", got, want)
	}
}

func TestQueryReadyWithNodeAndOrder(t *testing.T) {
	socket, requests := fakeSession(t, queryReadyOKLine)
	var stdout, stderr bytes.Buffer
	code := run([]string{"query", "ready", "--session", socket, "--node", "nova-tools/core", "--order", "priority"}, &stdout, &stderr, "")
	if code != 0 {
		t.Fatalf("query ready --node --order exit = %d, stderr = %s", code, stderr.String())
	}
	if got, want := awaitRequest(t, requests), "query ready --session "+socket+" --node nova-tools/core --order priority"; got != want {
		t.Fatalf("request line = %q, want %q", got, want)
	}
}

func TestQueryMissingSubverb(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"query"}, &stdout, &stderr, "")
	if code != 2 {
		t.Fatalf("query without subverb exit = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "size, remaining or ready") {
		t.Fatalf("refusal = %q, want it naming size, remaining or ready", stderr.String())
	}
}

func TestQueryUnknownSubverb(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"query", "bogus"}, &stdout, &stderr, "")
	if code != 2 {
		t.Fatalf("query bogus exit = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), `unknown query verb "bogus"`) {
		t.Fatalf("refusal = %q, want it naming unknown query verb", stderr.String())
	}
}

func TestQueryMissingSession(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"query", "size"}, &stdout, &stderr, "")
	if code != 2 {
		t.Fatalf("query size without --session exit = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "refusing to guess") {
		t.Fatalf("refusal = %q, want it naming refusing to guess", stderr.String())
	}
}

func TestQueryHelpListsQueryVerbs(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"help"}, &stdout, &stderr, "")
	if code != 0 {
		t.Fatalf("help exit = %d, stderr = %s", code, stderr.String())
	}
	help := stdout.String()
	for _, v := range []string{"query size", "query remaining", "query ready"} {
		if !strings.Contains(help, "nova-work "+v) {
			t.Errorf("help does not list %q", v)
		}
	}
}
