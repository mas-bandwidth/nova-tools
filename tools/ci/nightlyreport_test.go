package main

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func nightlyEnv() (env, *bytes.Buffer, *bytes.Buffer) {
	var out, errb bytes.Buffer
	vars := map[string]string{"GITHUB_REPOSITORY": "o/r", "GITHUB_SERVER_URL": "https://example.test", "GITHUB_RUN_ID": "42"}
	return env{stdout: &out, stderr: &errb, getenv: func(k string) string { return vars[k] }}, &out, &errb
}

var nightlyDay = func() time.Time { return time.Date(2026, 9, 30, 23, 59, 0, 0, time.UTC) }

func TestNightlyReportCommentsOnTodaysOpenIssue(t *testing.T) {
	t.Parallel()
	e, out, _ := nightlyEnv()
	r := &fakeCmdRunner{answer: func(c cmdSpec) (string, int, error) {
		if c.Args[0] == "issue" && c.Args[1] == "list" {
			return `[{"number":7,"title":"nightly tagged suite red 2026-09-29"},{"number":9,"title":"nightly tagged suite red 2026-09-30"},{"number":11,"title":"nightly tagged suite red 2026-09-30"}]`, 0, nil
		}
		return "", 0, nil
	}}
	if code := nightlyReport(e, r, nightlyDay, nil); code != 0 {
		t.Fatalf("exit %d", code)
	}
	want := []string{
		"gh issue list --state open --limit 200 --json number,title --repo o/r",
		"gh issue comment 9 --body A nightly tagged suite is still red: https://example.test/o/r/actions/runs/42 --repo o/r",
	}
	if got := r.lines(); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("commands\n got %q\nwant %q", got, want)
	}
	if !strings.Contains(out.String(), "issue #9 already open for today; adding a comment") {
		t.Fatalf("stdout %q", out)
	}
}

func TestNightlyReportCreatesTheIssueWhenNoneIsOpenForToday(t *testing.T) {
	t.Parallel()
	e, _, _ := nightlyEnv()
	r := &fakeCmdRunner{answer: func(c cmdSpec) (string, int, error) {
		if c.Args[1] == "list" {
			return `[{"number":7,"title":"nightly tagged suite red 2026-09-29"},{"number":8,"title":"something else"}]`, 0, nil
		}
		return "", 0, nil
	}}
	if code := nightlyReport(e, r, nightlyDay, nil); code != 0 {
		t.Fatalf("exit %d", code)
	}
	var create cmdSpec
	for _, c := range r.calls {
		if c.Args[1] == "create" {
			create = c
		}
	}
	if create.Name == "" {
		t.Fatalf("nothing created: %q", r.lines())
	}
	got := strings.Join(create.Args, "\x00")
	for _, want := range []string{"--title\x00nightly tagged suite red 2026-09-30", "--body\x00A leg of the nightly tagged suites failed.\n\nRun: https://example.test/o/r/actions/runs/42"} {
		if !strings.Contains(got, want) {
			t.Errorf("issue create lacks %q:\n%q", want, create.Args)
		}
	}
	if r.ran("gh issue comment") {
		t.Fatal("commented when there was no issue for today")
	}
}

func TestNightlyReportTheDayIsTheUTCDay(t *testing.T) {
	t.Parallel()
	e, _, _ := nightlyEnv()
	r := &fakeCmdRunner{answer: func(c cmdSpec) (string, int, error) {
		if c.Args[1] == "list" {
			return `[]`, 0, nil
		}
		return "", 0, nil
	}}
	zone := time.FixedZone("west", -8*3600)
	late := func() time.Time { return time.Date(2026, 9, 30, 20, 0, 0, 0, zone) } // 04:00 UTC the next day
	if code := nightlyReport(e, r, late, nil); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(strings.Join(r.lines(), "\n"), "nightly tagged suite red 2026-10-01") {
		t.Fatalf("commands %q", r.lines())
	}
}

func TestNightlyReportGhFailuresAreFailures(t *testing.T) {
	t.Parallel()
	for _, sub := range []string{"list", "comment", "create"} {
		e, _, errb := nightlyEnv()
		r := &fakeCmdRunner{answer: func(c cmdSpec) (string, int, error) {
			if c.Args[1] == sub {
				return "", 1, nil
			}
			if c.Args[1] == "list" {
				if sub == "comment" {
					return `[{"number":9,"title":"nightly tagged suite red 2026-09-30"}]`, 0, nil
				}
				return `[]`, 0, nil
			}
			return "", 0, nil
		}}
		if code := nightlyReport(e, r, nightlyDay, nil); code != 1 || !strings.Contains(errb.String(), "nightly-report:") {
			t.Errorf("gh issue %s failing: exit %d stderr %q", sub, code, errb)
		}
	}
}

func TestNightlyReportUnreadableListIsAFailureNotACreate(t *testing.T) {
	t.Parallel()
	e, _, errb := nightlyEnv()
	r := &fakeCmdRunner{answer: func(c cmdSpec) (string, int, error) { return "not json", 0, nil }}
	if code := nightlyReport(e, r, nightlyDay, nil); code != 1 || r.ran("gh issue create") {
		t.Fatalf("exit %d, commands %q stderr %q: an unreadable list must not open a duplicate", code, r.lines(), errb)
	}
}

func TestNightlyReportNeedsTheRunsContext(t *testing.T) {
	t.Parallel()
	var out, errb bytes.Buffer
	e := env{stdout: &out, stderr: &errb, getenv: func(string) string { return "" }}
	if code := nightlyReport(e, &fakeCmdRunner{}, nightlyDay, nil); code != 2 {
		t.Fatalf("exit %d, want 2", code)
	}
	e, _, _ = nightlyEnv()
	if code := nightlyReport(e, &fakeCmdRunner{}, nightlyDay, []string{"x"}); code != 2 {
		t.Fatalf("an argument: exit %d, want 2", code)
	}
}
