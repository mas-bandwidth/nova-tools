package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/merge"
	"github.com/mas-bandwidth/nova-tools/internal/testbin"
)

// TestIssue2063 is nova-tools #2063's control: the reads ledger. Who must read
// which PR at which exact head, and what each reader has said at THAT head, is
// a record; before this it lived in bus prose and the coordinator's memory.
// The lab is offline: a bare repository stands in for github.com/test/repo
// through the prRemoteURL seam, the GitHub reviews arrive as --reviews snapshot
// files, and the bus is a directory of notes carrying typed READ/ASK lines.
func TestIssue2063(t *testing.T) {
	// SLEEPS: this test waits on the wall clock (measured over 5 s on the 2026-09-25 PR run). Skipped 2026-09-25
	// by Glenn's rule ("unit tests must not have real sleeps or waits"): it becomes a
	// mocked-clock unit test or a functional program (nova-tools #4221).
	t.Skip("SLEEPS: needs a mocked clock or a functional test (nova-tools #4221)")
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not on this machine; the reads ledger drives a real git against a bare fixture remote")
	}
	dir := t.TempDir()
	gh := filepath.Join(dir, "gh.git")
	seed := filepath.Join(dir, "seed")
	gitAt := func(d string, args ...string) string {
		t.Helper()
		c := exec.Command("git", args...)
		c.Dir = d
		c.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=fixture", "GIT_AUTHOR_EMAIL=fixture@example.invalid",
			"GIT_COMMITTER_NAME=fixture", "GIT_COMMITTER_EMAIL=fixture@example.invalid")
		b, e := c.CombinedOutput()
		if e != nil {
			t.Fatalf("git %v: %v %s", args, e, b)
		}
		return strings.TrimSpace(string(b))
	}
	gitAt(dir, "init", "-q", "--bare", gh)
	gitAt(gh, "symbolic-ref", "HEAD", "refs/heads/main")
	must := func(e error) {
		t.Helper()
		if e != nil {
			t.Fatal(e)
		}
	}
	must(os.MkdirAll(seed, 0o755))
	gitAt(seed, "init", "-q", "-b", "main")
	gitAt(seed, "config", "user.email", "fixture@example.invalid")
	gitAt(seed, "config", "user.name", "fixture")
	must(os.MkdirAll(filepath.Join(seed, "docs"), 0o755))
	must(os.MkdirAll(filepath.Join(seed, "internal", "auth"), 0o755))
	must(os.MkdirAll(filepath.Join(seed, "internal", "plain"), 0o755))
	must(os.WriteFile(filepath.Join(seed, "docs", "SPEC-THING.md"), []byte("# Spec\n\n1. Be kind.\n"), 0o644))
	must(os.WriteFile(filepath.Join(seed, "internal", "auth", "acl.go"), []byte("package acl\n\nfunc Check() bool { return true }\n"), 0o644))
	must(os.WriteFile(filepath.Join(seed, "internal", "plain", "util.go"), []byte("package plain\n"), 0o644))
	gitAt(seed, "add", ".")
	gitAt(seed, "commit", "-qm", "base")
	base := gitAt(seed, "rev-parse", "HEAD")
	gitAt(seed, "push", "-q", gh, "main:refs/heads/main")

	// PR 1945 touches internal/auth/acl.go: the code read and the security read
	// are both derived from the paths touched.
	gitAt(seed, "checkout", "-qb", "pr-1945")
	must(os.WriteFile(filepath.Join(seed, "internal", "auth", "acl.go"), []byte("package acl\n\nfunc Check() bool { return false }\n"), 0o644))
	gitAt(seed, "add", "-u")
	gitAt(seed, "commit", "-qm", "the pull request")
	head1 := gitAt(seed, "rev-parse", "HEAD")
	gitAt(seed, "push", "-q", gh, "pr-1945:refs/pull/1945/head")

	// PR 1901 touches the spec document alone: the contract read is derived, and
	// the security read is an explicit ask, not a path.
	gitAt(seed, "checkout", "-q", "main")
	gitAt(seed, "checkout", "-qb", "pr-1901")
	must(os.WriteFile(filepath.Join(seed, "docs", "SPEC-THING.md"), []byte("# Spec\n\n1. Be kind, precisely.\n"), 0o644))
	gitAt(seed, "add", "-u")
	gitAt(seed, "commit", "-qm", "the spec change")
	head1901 := gitAt(seed, "rev-parse", "HEAD")
	gitAt(seed, "push", "-q", gh, "pr-1901:refs/pull/1901/head")

	lane := filepath.Join(dir, "lane")
	repo := filepath.Join(lane, merge.RepoDir)
	gitAt(dir, "clone", "-q", gh, repo)
	// The lane's forge remote is the local bare repository: the endpoint mocked
	// with a local fake, so no real host stands on the CI path (#2863).
	prevRemote := prRemoteURL
	prRemoteURL = func(string) string { return gh }
	t.Cleanup(func() { prRemoteURL = prevRemote })
	st := &merge.State{
		Version: merge.Version, Repo: "test/repo", Base: base, LaneBranch: "lane",
		PRs: []*merge.Entry{{PR: 1945, NeedsRead: "yes"}, {PR: 1901, NeedsRead: "yes"}},
	}
	must(st.SaveTo(lane))

	bus := filepath.Join(dir, "bus")
	must(os.MkdirAll(bus, 0o755))
	note := func(name, from, date, body string) {
		t.Helper()
		must(os.WriteFile(filepath.Join(bus, name), []byte("From: "+from+"\nTo: Rowan\nSubject: r\nDate: "+date+"\n\n"+body+"\n"), 0o644))
	}
	note("01-ask.md", "Rowan", "Wed Sep 23 00:59:00 UTC 2026", "ASK #1901 security Stella")
	note("02-stella.md", "Stella", "Wed Sep 23 01:02:03 UTC 2026", "READ #1945 "+head1+" APPROVE scope=security")
	note("03-rowan.md", "rowan", "Wed Sep 23 01:04:05 UTC 2026", "READ #1945 "+head1+" APPROVE")
	// The bus is shared: prose that starts with "Read", a malformed line naming
	// no pull request, and typed lines about a PR this lane does not hold are
	// stepped over, never a refusal of this lane's ledger (Emma's HOLD, #2863).
	// The stranger's note has no Date line, which only a line that is ours needs.
	must(os.WriteFile(filepath.Join(bus, "00-stranger.md"), []byte("From: Pax\nTo: Rowan\nSubject: s\n\n"+
		"Read the spec first, then the diff.\nREAD #9999 deadbeef APPROVE\nASK #9999 vibes Pax\nREAD it later\n"), 0o644))

	snapshot := func(name string, v any) string {
		t.Helper()
		p := filepath.Join(dir, name)
		b, e := json.Marshal(v)
		if e != nil {
			t.Fatal(e)
		}
		must(os.WriteFile(p, b, 0o644))
		return p
	}
	reviews1945 := snapshot("reviews-1945.json", map[string]any{
		"author": map[string]string{"login": "rowan"},
		"reviews": []any{map[string]any{
			"author":       map[string]string{"login": "emma"},
			"state":        "APPROVED",
			"commit_id":    head1,
			"submitted_at": "2026-09-23T01:01:00Z",
		}},
	})
	reviews1901 := snapshot("reviews-1901.json", map[string]any{
		"author":  map[string]string{"login": "glenn"},
		"reviews": []any{},
	})
	readArgs := func(extra ...string) []string {
		return append([]string{"reads", "--lane", lane, "--bus", bus,
			"--reviews", "1945:" + reviews1945, "--reviews", "1901:" + reviews1901}, extra...)
	}
	runReads := func(extra ...string) string {
		t.Helper()
		var out, errb bytes.Buffer
		code := run(readArgs(extra...), &out, &errb)
		if code != 0 {
			t.Fatalf("reads %v: exit %d\nstdout: %s\nstderr: %s", extra, code, out.String(), errb.String())
		}
		return out.String()
	}

	var out, errb bytes.Buffer
	if code := run([]string{"reads"}, &out, &errb); code != 2 || !strings.Contains(errb.String(), "READS REFUSED") {
		t.Fatalf("reads with no --lane: exit %d stderr %s", code, errb.String())
	}

	// The ledger at head1: what each reader has said at THAT head.
	ledger := runReads()
	short1 := merge.Short(head1)
	for _, want := range []string{
		"READS ENTRY kind=pr id=1945 head=" + head1 + " roles=code,security author=rowan",
		"READ kind=pr id=1945 who=emma verdict=approve scope=- sha=" + head1 + " at=2026-09-23T01:01:00Z source=github",
		"READ kind=pr id=1945 who=Stella verdict=approve scope=security sha=" + head1 + " at=2026-09-23T01:02:03Z source=bus",
		"READ kind=pr id=1945 who=rowan verdict=approve scope=- sha=" + head1 + " at=2026-09-23T01:04:05Z source=bus self=yes",
		"READS ENTRY kind=pr id=1901 head=" + head1901 + " roles=contract,security author=glenn",
		"READS OWED kind=pr id=1901 role=contract who=-",
		"READS OWED kind=pr id=1901 role=security who=Stella",
	} {
		if !strings.Contains(ledger, want) {
			t.Errorf("the reads ledger at head1 lacks %q:\n%s", want, ledger)
		}
	}
	if strings.Contains(ledger, "READS STALE") {
		t.Errorf("nothing is stale at head1:\n%s", ledger)
	}

	// --ready: every required read an approve at the live head. 1945 is; 1901
	// owes contract and security.
	ready := runReads("--ready")
	if !strings.Contains(ready, "READS READY kind=pr id=1945 head="+head1+" roles=code,security") {
		t.Errorf("reads --ready does not list 1945 at head1:\n%s", ready)
	}
	if strings.Contains(ready, "id=1901") {
		t.Errorf("reads --ready lists 1901, whose contract and security reads are owed:\n%s", ready)
	}

	// --waiting-on Stella: the ask, and only the ask, before the push.
	waiting := runReads("--waiting-on", "Stella")
	if !strings.Contains(waiting, "READS WAITING who=Stella kind=pr id=1901 role=security why=ask at=2026-09-23T00:59:00Z") {
		t.Errorf("Stella's queue does not hold the security ask on 1901:\n%s", waiting)
	}
	if strings.Contains(waiting, "id=1945") {
		t.Errorf("Stella's queue holds 1945, whose security read she approved at head1:\n%s", waiting)
	}

	// The author pushes a new head.
	gitAt(seed, "checkout", "-q", "pr-1945")
	must(os.WriteFile(filepath.Join(seed, "internal", "auth", "acl.go"), []byte("package acl\n\nfunc Check() bool { return false || true }\n"), 0o644))
	gitAt(seed, "add", "-u")
	gitAt(seed, "commit", "-qm", "the push that moves the head")
	head2 := gitAt(seed, "rev-parse", "HEAD")
	gitAt(seed, "push", "-q", gh, "pr-1945:refs/pull/1945/head")
	short2 := merge.Short(head2)
	delta := short1 + ".." + short2

	// The ledger at head2: the reads at head1 are records still, marked stale,
	// each naming who must re-read what delta.
	ledger = runReads()
	for _, want := range []string{
		"READS ENTRY kind=pr id=1945 head=" + head2 + " roles=code,security author=rowan",
		"READ kind=pr id=1945 who=Stella verdict=approve scope=security sha=" + head1 + " at=2026-09-23T01:02:03Z source=bus",
		"READS STALE kind=pr id=1945 who=emma read=" + short1 + " live=" + short2 + " delta=" + delta,
		"READS STALE kind=pr id=1945 who=Stella read=" + short1 + " live=" + short2 + " delta=" + delta,
		"READS STALE kind=pr id=1945 who=rowan read=" + short1 + " live=" + short2 + " delta=" + delta,
		"READS OWED kind=pr id=1945 role=code who=emma",
		"READS OWED kind=pr id=1945 role=security who=Stella",
	} {
		if !strings.Contains(ledger, want) {
			t.Errorf("the reads ledger at head2 lacks %q:\n%s", want, ledger)
		}
	}

	// An approval at an old head no longer counts (#1762 'stale approve').
	ready = runReads("--ready")
	if !strings.Contains(ready, "READS READY none") || strings.Contains(ready, "id=1945") {
		t.Errorf("reads --ready after the push must list nothing; emma approved head1, not head2:\n%s", ready)
	}

	// Stella's queue in order: the ask first (00:59:00), the stale re-read of
	// 1945's security read second (01:02:03), with the delta to re-read.
	waiting = runReads("--waiting-on", "Stella")
	askLine := "READS WAITING who=Stella kind=pr id=1901 role=security why=ask at=2026-09-23T00:59:00Z"
	staleLine := "READS WAITING who=Stella kind=pr id=1945 role=security why=stale at=2026-09-23T01:02:03Z delta=" + delta
	if !strings.Contains(waiting, askLine) || !strings.Contains(waiting, staleLine) {
		t.Errorf("Stella's queue after the push must hold the ask and the stale re-read:\n%s", waiting)
	}
	if strings.Index(waiting, askLine) > strings.Index(waiting, staleLine) {
		t.Errorf("Stella's queue is not in order; the ask (00:59:00) precedes the stale read (01:02:03):\n%s", waiting)
	}

	// Nobody reads their own work: the author's approve is a record and never a
	// queue item.
	waiting = runReads("--waiting-on", "rowan")
	if !strings.Contains(waiting, "READS WAITING who=rowan none") || strings.Contains(waiting, "id=1945") {
		t.Errorf("the author's own read never enters the author's queue:\n%s", waiting)
	}

	// A mistyped line about a PR the lane holds is still a refusal: silently
	// dropping a friend's verdict on this lane is what the ledger closes.
	note("04-typo.md", "Stella", "Wed Sep 23 01:06:00 UTC 2026", "READ #1945 "+short1+" APPROVE")
	out.Reset()
	errb.Reset()
	if code := run(readArgs(), &out, &errb); code != 2 || !strings.Contains(errb.String(), "READS REFUSED") || !strings.Contains(errb.String(), "04-typo.md body line 1") {
		t.Errorf("a mistyped READ on held PR 1945 must refuse naming the note and line: exit %d stderr %s", code, errb.String())
	}
	must(os.Remove(filepath.Join(bus, "04-typo.md")))

	// A failed GitHub read is a refusal, never an empty review list (Stella's
	// hold, #2863): with no --reviews snapshot for 1945, the real seam runs gh;
	// a gh that exits non-zero must not leave a ledger that sees no GitHub hold
	// and cannot tell the author's own bus approve from a friend's.
	fakeBin := filepath.Join(dir, "fakebin")
	must(os.MkdirAll(fakeBin, 0o755))
	must(testbin.WriteExecutable(filepath.Join(fakeBin, "gh"), []byte("#!/bin/sh\necho 'fake gh: HTTP 502 Bad Gateway' >&2\nexit 1\n"), 0o755))
	t.Setenv("PATH", fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"))
	prevView := viewPRReviews
	viewPRReviews = ghPRReviews
	t.Cleanup(func() { viewPRReviews = prevView })
	for _, extra := range [][]string{nil, {"--ready"}} {
		out.Reset()
		errb.Reset()
		args := append([]string{"reads", "--lane", lane, "--bus", bus, "--reviews", "1901:" + reviews1901}, extra...)
		code := run(args, &out, &errb)
		if code != 2 || !strings.Contains(errb.String(), "READS REFUSED") ||
			!strings.Contains(errb.String(), "GitHub reviews of pr 1945") || !strings.Contains(errb.String(), "fake gh: HTTP 502") {
			t.Errorf("reads %v with gh failing must refuse naming the GitHub read of 1945 and gh's reason: exit %d\nstdout: %s\nstderr: %s", extra, code, out.String(), errb.String())
		}
		if strings.Contains(out.String(), "READS READY") || strings.Contains(out.String(), "id=1945") {
			t.Errorf("reads %v with gh failing printed a ledger or readiness anyway:\n%s", extra, out.String())
		}
	}

	// A gh that prints more than the 4 MiB bound is refused promptly (Stella's
	// hold, #2863): the reader stops at the bound, and a child still writing
	// must not block Wait until the caller's timeout. --timeout is a day, so
	// a reader that waits on the blocked child hangs past the test binary's
	// deadline instead of returning: the event asserted is that run returns.
	must(testbin.WriteExecutable(filepath.Join(fakeBin, "gh"), []byte("#!/bin/sh\nhead -c 6000000 /dev/zero\nexit 0\n"), 0o755))
	out.Reset()
	errb.Reset()
	code := run([]string{"reads", "--lane", lane, "--bus", bus, "--reviews", "1901:" + reviews1901, "--ready", "--timeout", "86400"}, &out, &errb)
	if code != 2 || !strings.Contains(errb.String(), "READS REFUSED") || !strings.Contains(errb.String(), "gh output over 4194304 bytes") ||
		strings.Contains(out.String(), "READS READY") {
		t.Errorf("reads with an oversized gh reply must refuse naming the bound: exit %d\nstdout: %s\nstderr: %s", code, out.String(), errb.String())
	}

	// A snapshot that names no author is refused the same way: the author's
	// own reads could not be told from a friend's.
	noAuthor := snapshot("reviews-1945-noauthor.json", map[string]any{"reviews": []any{}})
	out.Reset()
	errb.Reset()
	if code := run([]string{"reads", "--lane", lane, "--bus", bus, "--reviews", "1945:" + noAuthor, "--reviews", "1901:" + reviews1901, "--ready"}, &out, &errb); code != 2 ||
		!strings.Contains(errb.String(), "pr 1945 name no author") || strings.Contains(out.String(), "READS READY") {
		t.Errorf("a reviews snapshot with no author must refuse: exit %d\nstdout: %s\nstderr: %s", code, out.String(), errb.String())
	}
}

// TestIssue2063ReviewProseIsNotTyped: a GitHub review whose line merely starts
// with the word "Read" is prose, not a typed READ line, and must not refuse the
// ledger (Johnny's nit on #2863); a mistyped READ #<n> line still refuses.
func TestIssue2063ReviewProseIsNotTyped(t *testing.T) {
	t.Parallel()

	head := strings.Repeat("a", 40)
	revs := []prReview{{Author: "johnny", State: "APPROVED", CommitID: head, SubmittedAt: "2026-09-23T14:21:21Z",
		Body: "Read the spec first; the seam is fine.\nread carefully"}}
	got, err := ingestPRReviews(2863, revs)
	if err != nil || len(got) != 1 || got[0].Verdict != "approve" || got[0].Head != head {
		t.Fatalf("prose review must fold as a plain APPROVED at commit_id: got %+v err %v", got, err)
	}
	revs[0].Body = "READ #2863 abc APPROVE"
	if _, err := ingestPRReviews(2863, revs); err == nil {
		t.Fatalf("a mistyped READ #<n> line must still refuse")
	}
}
