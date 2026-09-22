package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// The `review` verb's tests. They dial NOTHING: gh is a script this test writes,
// and the provider is the recorded pass under internal/prereview/testdata (one
// Jev call costs money, so the 122-cell pass ran once and everything since
// replays it).

// cellData is where the recorded pull requests and answers live, relative to
// this package.
func cellData(t *testing.T, name string) string {
	t.Helper()
	p, err := filepath.Abs(filepath.Join("..", "..", "internal", "prereview", "testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// fakeGH writes a gh replacement that serves the recorded pull requests and
// records every argv it is called with, so a test can say what the verb did and
// -- more to the point -- what it did NOT do.
func fakeGH(t *testing.T) (path, argvLog string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fake gh is a shell script")
	}
	dir := t.TempDir()
	argvLog = filepath.Join(dir, "argv.log")
	path = filepath.Join(dir, "gh")
	script := fmt.Sprintf(`#!/bin/sh
printf '%%s\n' "$*" >> %q
case "$1" in
  pr)
    case "$2" in
      view) cat %q/"$3".json ;;
      diff) cat %q/"$3".diff ;;
      comment) exit 0 ;;
      *) echo "fake gh: unexpected $*" >&2; exit 2 ;;
    esac
    ;;
  api)
    sha=$(printf '%%s\n' "$2" | sed -n 's#.*commits/\([0-9a-fA-F][0-9a-fA-F]*\)/check-runs.*#\1#p')
    if [ -z "$sha" ]; then echo "fake gh: no sha in $*" >&2; exit 2; fi
    printf '%%s\n' "{\"total_count\":1,\"check_runs\":[{\"name\":\"ci-ok\",\"status\":\"completed\",\"conclusion\":\"success\",\"head_sha\":\"$sha\",\"started_at\":\"2026-09-22T00:00:00Z\",\"completed_at\":\"2026-09-22T00:00:01Z\"}]}"
    ;;
  *) echo "fake gh: unexpected $*" >&2; exit 2 ;;
esac
`, argvLog, cellData(t, "cells"), cellData(t, "cells"))
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path, argvLog
}

func argv(t *testing.T, log string) string {
	t.Helper()
	raw, err := os.ReadFile(log)
	if err != nil {
		if os.IsNotExist(err) {
			return ""
		}
		t.Fatal(err)
	}
	return string(raw)
}

// TestReviewDryRunPrintsTheLineAndPostsNothing. --dry-run is the default and it
// is the whole safety property today: Rowan turns posting on after Glenn has
// seen the 122-row result, and until then no verb run may write to a pull
// request.
func TestReviewDryRunPrintsTheLineAndPostsNothing(t *testing.T) {
	gh, log := fakeGH(t)
	ledger := filepath.Join(t.TempDir(), "ledger.jsonl")
	var out, errb bytes.Buffer
	code := run([]string{"review", "--repo", "mas-bandwidth/schema", "--pr", "1488",
		"--gh", gh, "--replay", cellData(t, "jev-2026-09-22"), "--ledger-path", ledger, "--table"}, &out, &errb)
	if code != 3 {
		t.Fatalf("exit=%d stderr=%s stdout=%s (a BOUNCE exits 3)", code, errb.String(), out.String())
	}
	line := out.String()
	if !strings.Contains(line, "JEV head=8d2213c7a6ea7ac0359e1020edaaa7914b8f8df3 verdict=BOUNCE score=6 checks=donewhen:ok,selfcheck:ok,paths:ok,claims:fail,ci:ok,score:6 model=jev-latest cost=$- explain=") {
		t.Fatalf("stdout = %q", line)
	}
	if !strings.Contains(line, "checks=donewhen:ok,selfcheck:ok,paths:ok,claims:fail,ci:ok,score:6") {
		t.Fatalf("the checks are not on the line: %q", line)
	}
	if strings.Contains(argv(t, log), "pr comment") {
		t.Fatalf("a dry run posted a comment: %s", argv(t, log))
	}
	raw, err := os.ReadFile(ledger)
	if err != nil {
		t.Fatalf("the verdict was not appended to the ledger: %v", err)
	}
	var d struct {
		PR       int     `json:"pr"`
		Verdict  string  `json:"verdict"`
		Score    int     `json:"score"`
		RawScore float64 `json:"raw_score"`
		Posted   bool    `json:"posted"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(raw), &d); err != nil {
		t.Fatal(err)
	}
	if d.PR != 1488 || d.Verdict != "BOUNCE" || d.Score != 6 || d.Posted {
		t.Fatalf("ledger row = %+v", d)
	}
	if d.RawScore == 0 {
		t.Error("the raw provider answer must travel into the ledger beside the 1-10")
	}
}

// TestReviewPostPostsExactlyOneComment. --post is the verb's only write, and it
// is one comment carrying the typed line.
func TestReviewPostPostsExactlyOneComment(t *testing.T) {
	gh, log := fakeGH(t)
	var out, errb bytes.Buffer
	run([]string{"review", "--repo", "mas-bandwidth/schema", "--pr", "1488", "--post",
		"--gh", gh, "--replay", cellData(t, "jev-2026-09-22"),
		"--ledger-path", filepath.Join(t.TempDir(), "l.jsonl")}, &out, &errb)
	calls := argv(t, log)
	if n := strings.Count(calls, "pr comment"); n != 1 {
		t.Fatalf("posted %d comments, want exactly 1: %s", n, calls)
	}
	if !strings.Contains(calls, "--body JEV head=") || strings.Contains(calls, "DISPOSITION") {
		t.Fatalf("the comment does not carry the typed line: %s", calls)
	}
	if !strings.Contains(calls, "lands nothing") {
		t.Fatalf("the comment does not say it lands nothing: %s", calls)
	}
}

// TestReviewBatchRunsEveryPullRequestOnce.
func TestReviewBatchRunsEveryPullRequestOnce(t *testing.T) {
	gh, _ := fakeGH(t)
	batch := filepath.Join(t.TempDir(), "batch.txt")
	if err := os.WriteFile(batch, []byte("# the two exemplars\n1488\n\n#1556\n1488\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	run([]string{"review", "--repo", "mas-bandwidth/schema", "--batch", batch, "--table",
		"--gh", gh, "--replay", cellData(t, "jev-2026-09-22"),
		"--ledger-path", filepath.Join(t.TempDir(), "l.jsonl")}, &out, &errb)
	rows := 0
	for _, l := range strings.Split(out.String(), "\n") {
		if strings.HasPrefix(l, "ROW\t") {
			rows++
		}
	}
	if rows != 2 {
		t.Fatalf("%d rows, want 2 (a repeated number is one pull request): %s", rows, out.String())
	}
}

// TestReviewRefusals. Every refusal names the remedy and spends nothing.
func TestReviewRefusals(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"no repo", []string{"review", "--pr", "1"}, "--repo"},
		{"neither pr nor batch", []string{"review", "--repo", "a/b"}, "exactly one"},
		{"both pr and batch", []string{"review", "--repo", "a/b", "--pr", "1", "--batch", "x"}, "exactly one"},
		{"post and dry-run", []string{"review", "--repo", "a/b", "--pr", "1", "--post", "--dry-run"}, "two halves"},
		{"card with batch", []string{"review", "--repo", "a/b", "--batch", "x", "--card", "c"}, "--card"},
		{"redis ledger", []string{"review", "--repo", "a/b", "--pr", "1", "--ledger", "redis"}, "#2563"},
		{"unknown ledger", []string{"review", "--repo", "a/b", "--pr", "1", "--ledger", "postgres"}, "file or redis"},
		{"stray argument", []string{"review", "--repo", "a/b", "--pr", "1", "extra"}, "unexpected argument"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out, errb bytes.Buffer
			if code := run(tc.args, &out, &errb); code != 2 {
				t.Fatalf("exit=%d, want 2; stderr=%s", code, errb.String())
			}
			if !strings.Contains(errb.String(), tc.want) {
				t.Fatalf("refusal %q does not name %q", errb.String(), tc.want)
			}
		})
	}
}

// TestReviewNoJevSpendsNothing. --no-jev runs the four mechanical checks alone:
// no key is read, no provider is dialled, and the line says the score is absent
// rather than printing a zero nobody gave.
func TestReviewNoJevSpendsNothing(t *testing.T) {
	gh, _ := fakeGH(t)
	var out, errb bytes.Buffer
	t.Setenv("JEV_API_KEY", "")
	t.Setenv("TYPESAFE_API_KEY", "")
	code := run([]string{"review", "--repo", "mas-bandwidth/schema", "--pr", "1556", "--no-jev",
		"--gh", gh, "--ledger-path", filepath.Join(t.TempDir(), "l.jsonl")}, &out, &errb)
	if code != 3 {
		t.Fatalf("exit=%d stderr=%s", code, errb.String())
	}
	if !strings.Contains(out.String(), "score=- ") {
		t.Fatalf("an unscored pass must not print a number: %q", out.String())
	}
	if !strings.Contains(out.String(), "verdict=UNSURE") {
		t.Fatalf("an unscored pass holds: %q", out.String())
	}
}

// TestReviewHelpNamesTheVerb. The usage is the door a stranger comes through.
func TestReviewHelpNamesTheVerb(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{"help"}, &out, &errb); code != 0 {
		t.Fatalf("exit=%d", code)
	}
	for _, want := range []string{"nova-decide review", "--batch", "NEVER lands anything"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("usage does not name %q", want)
		}
	}
}

// ciFixtureGH serves one recorded rollup for one head and a pull request whose
// head is that sha. The other checks are off in the caller, so the diff can be
// empty: only ci decides.
func ciFixtureGH(t *testing.T, pr int, sha, rollup string) string {
	t.Helper()
	dir := t.TempDir()
	view := filepath.Join(dir, "view.json")
	body := fmt.Sprintf("{\"number\":%d,\"headRefOid\":%q,\"title\":\"t\",\"body\":\"\",\"files\":[]}\n", pr, sha)
	if err := os.WriteFile(view, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "gh")
	script := fmt.Sprintf(`#!/bin/sh
case "$1" in
  pr)
    case "$2" in
      view) cat %q ;;
      diff) printf '' ;;
      comment) exit 0 ;;
      *) echo "unexpected pr $*" >&2; exit 2 ;;
    esac
    ;;
  api)
    case "$2" in
      *%s*) cat %q ;;
      *) echo "unexpected api $2" >&2; exit 2 ;;
    esac
    ;;
  *) echo "unexpected $*" >&2; exit 2 ;;
esac
`, view, sha, rollup)
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestReviewCIControlsReplayTheRecordedRollup is nova-tools #2704. --checks ci
// is checks_enabled with only that check on, so the verdict is the rollup's:
// #2519 at 907546af BOUNCEs and names the red jobs; #2522 at 8359db4f PASSes.
func TestReviewCIControlsReplayTheRecordedRollup(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake gh is a shell script")
	}
	for _, tc := range []struct {
		name string
		file string
		sha  string
		pr   int
		exit int
		want []string
		not  []string
	}{
		{
			name: "2519 bounce",
			file: "2519-907546af.json",
			sha:  "907546af45503600783997c21d90d55b489a1703",
			pr:   2519, exit: 3,
			want: []string{"verdict=BOUNCE", "ci:fail", "BOUNCE", "907546af",
				"jobs: ci-ok, test (1/4 space), test (1/4 studio), test (2/4 space), test (2/4 studio)"},
			not: []string{"verdict=PASS", "test (3/4", "lint"},
		},
		{
			name: "2522 pass",
			file: "2522-8359db4f.json",
			sha:  "8359db4fec24521b444d01bf6f90a9f981e9765e",
			pr:   2522, exit: 0,
			want: []string{"verdict=PASS", "ci:ok", "8359db4f"},
			not:  []string{"verdict=BOUNCE", "ci:fail"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gh := ciFixtureGH(t, tc.pr, tc.sha, cellData(t, filepath.Join("ci-rollup", tc.file)))
			var out, errb bytes.Buffer
			code := run([]string{"review", "--repo", "mas-bandwidth/nova-tools", "--pr", strconv.Itoa(tc.pr),
				"--checks", "ci", "--no-jev", "--gh", gh,
				"--ledger-path", filepath.Join(t.TempDir(), "l.jsonl")}, &out, &errb)
			if code != tc.exit {
				t.Fatalf("exit=%d want %d\nstderr=%s\nstdout=%s", code, tc.exit, errb.String(), out.String())
			}
			for _, w := range tc.want {
				if !strings.Contains(out.String(), w) {
					t.Errorf("stdout missing %q:\n%s", w, out.String())
				}
			}
			for _, w := range tc.not {
				if strings.Contains(out.String(), w) {
					t.Errorf("stdout contains %q:\n%s", w, out.String())
				}
			}
		})
	}
}
