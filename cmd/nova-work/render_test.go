package main

import (
	"bytes"
	"strings"
	"testing"
)

// renderOKLine and renderFailLine are the spec's own RENDER grammar lines
// (docs/SPEC-WORK.md, Output grammar) instantiated once: --file and --check
// keep their status lines, and a check that finds drift is a RENDER FAIL, exit
// 1, the target untouched.
const renderOKLine = "RENDER OK view=roadmap-1 projection=proj-1 scope=42 cells=5 private=1 bytes=2048 target=mas-bandwidth/nova-tools:docs/roadmap.md was=e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855 now=e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855 pushed=41 emitted=934"

const renderFailLine = "RENDER FAIL view=roadmap-1 projection=proj-1 cells=5 private=1 drifted=3 target=mas-bandwidth/nova-tools:docs/roadmap.md: the region moved since it was rendered"

// queryPercentOKLine and queryPercentFailLine are the spec's own QUERY lines
// for the percent ask: green over applicable, rows and baseline-rows beside it,
// and a refusal that names no axis member.
const queryPercentOKLine = "QUERY OK ask=percent scope=412 membership=axis branch=open unit=features source=9f2c1a7e freshest=2026-09-13T18:22:41Z done=6 done-unverified=1 unknown=0 deferred=0 cancelled=0 superseded=0 stale=0 required=12 since-baseline=0 private=0 open=0 closed=0 gap=0 green=7 applicable=10 baseline-rows=10 row-kind=feature pushed=410 rows=10 shown=0 pages=1 parses=0 replays=0 emitted=512"

const queryPercentFailLine = "QUERY FAIL ask=percent rows=10 shown=0: no such axis member"

func TestRenderCheckPrintsTheSessionsLineByteForByte(t *testing.T) {
	socket, requests := fakeSession(t, renderOKLine)
	var stdout, stderr bytes.Buffer
	code := run([]string{"render", "--session", socket, "--view", "roadmap-1", "--projection", "proj-1", "--check"}, &stdout, &stderr, "")
	if code != 0 {
		t.Fatalf("render --check exit = %d, stderr = %s", code, stderr.String())
	}
	if got, want := stdout.String(), renderOKLine+"\n"; got != want {
		t.Fatalf("render --check stdout = %q, want byte for byte %q", got, want)
	}
	if stderr.Len() != 0 {
		t.Fatalf("render --check wrote stderr: %q", stderr.String())
	}
	if got, want := awaitRequest(t, requests), "render --session "+socket+" --view roadmap-1 --projection proj-1 --check true"; got != want {
		t.Fatalf("request line = %q, want %q", got, want)
	}
}

func TestRenderCheckDriftRefusalExitsOne(t *testing.T) {
	socket, _ := fakeSession(t, renderFailLine)
	var stdout, stderr bytes.Buffer
	code := run([]string{"render", "--session", socket, "--view", "roadmap-1", "--projection", "proj-1", "--check"}, &stdout, &stderr, "")
	if code != 1 {
		t.Fatalf("drift refusal exit = %d, want 1", code)
	}
	if stdout.Len() != 0 {
		t.Fatalf("drift refusal wrote stdout: %q", stdout.String())
	}
	if got, want := stderr.String(), renderFailLine+"\n"; got != want {
		t.Fatalf("drift refusal stderr = %q, want byte for byte %q", got, want)
	}
}

func TestRenderSpellsItsRequestLine(t *testing.T) {
	socket, requests := fakeSession(t, renderOKLine)

	var stdout, stderr bytes.Buffer
	code := run([]string{"render", "--session", socket, "--view", "roadmap-1", "--projection", "proj-1", "--file", "--fixed", "os=linux", "--fixed", "arch=arm64", "--at", "42"}, &stdout, &stderr, "")
	if code != 0 || stderr.Len() != 0 {
		t.Fatalf("render --file exit=%d stderr=%q", code, stderr.String())
	}
	want := "render --session " + socket + " --view roadmap-1 --projection proj-1 --fixed os\\x3dlinux --fixed arch\\x3darm64 --file true --at 42"
	if got := awaitRequest(t, requests); got != want {
		t.Fatalf("--file request line = %q, want %q", got, want)
	}

	stdout.Reset()
	stderr.Reset()
	code = run([]string{"render", "--session", socket, "--view", "roadmap-1", "--chat", "--row-axis", "os", "--column-axis", "arch", "--fixed", "os=linux"}, &stdout, &stderr, "")
	if code != 0 || stderr.Len() != 0 {
		t.Fatalf("render --chat exit=%d stderr=%q", code, stderr.String())
	}
	want = "render --session " + socket + " --view roadmap-1 --chat true --row-axis os --column-axis arch --fixed os\\x3dlinux"
	if got := awaitRequest(t, requests); got != want {
		t.Fatalf("--chat request line = %q, want %q", got, want)
	}
}

func TestQueryPercentPrintsTheSessionsLineByteForByte(t *testing.T) {
	socket, requests := fakeSession(t, queryPercentOKLine)
	var stdout, stderr bytes.Buffer
	code := run([]string{"query", "--session", socket, "--ask", "percent", "--axis", "linux", "--node", "roadmap-1", "--max", "20"}, &stdout, &stderr, "")
	if code != 0 {
		t.Fatalf("query percent exit = %d, stderr = %s", code, stderr.String())
	}
	if got, want := stdout.String(), queryPercentOKLine+"\n"; got != want {
		t.Fatalf("query percent stdout = %q, want byte for byte %q", got, want)
	}
	if stderr.Len() != 0 {
		t.Fatalf("query percent wrote stderr: %q", stderr.String())
	}
	if got, want := awaitRequest(t, requests), "query --session "+socket+" --ask percent --node roadmap-1 --axis linux --max 20"; got != want {
		t.Fatalf("request line = %q, want %q", got, want)
	}
}

func TestQueryPercentRefusalExitsOne(t *testing.T) {
	socket, _ := fakeSession(t, queryPercentFailLine)
	var stdout, stderr bytes.Buffer
	code := run([]string{"query", "--session", socket, "--ask", "percent", "--axis", "linux"}, &stdout, &stderr, "")
	if code != 1 {
		t.Fatalf("query percent refusal exit = %d, want 1", code)
	}
	if stdout.Len() != 0 {
		t.Fatalf("query percent refusal wrote stdout: %q", stdout.String())
	}
	if got, want := stderr.String(), queryPercentFailLine+"\n"; got != want {
		t.Fatalf("query percent refusal stderr = %q, want byte for byte %q", got, want)
	}
}

func TestRenderAndQueryRefuseWithoutSession(t *testing.T) {
	for _, args := range [][]string{
		{"render", "--view", "roadmap-1"},
		{"query", "--ask", "percent", "--axis", "linux"},
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
