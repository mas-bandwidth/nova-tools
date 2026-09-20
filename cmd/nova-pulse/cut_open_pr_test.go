package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// cut-kind-fix-refuses-when-an-open-pr-already-carries-the-issue (#2041): a
// generator that cuts a fix card for an issue an open (or just-landed) PR
// already names is how the 2026-09-20 fix wave re-cut 27 of 27. The cutter
// itself must refuse, so every generator that calls it inherits the floor.

func prListJSON(t *testing.T, rows ...map[string]any) string {
	t.Helper()
	if rows == nil {
		return "[]"
	}
	raw, err := json.Marshal(rows)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func cutFixArgs(t *testing.T, issue int, title, out, queue string) []string {
	t.Helper()
	return []string{
		"--kind", "fix",
		"--repo", "mas-bandwidth/nova-tools",
		"--issue", strconv.Itoa(issue),
		"--title", title,
		"--out", out,
		"--queue", queue,
	}
}

func cutFixDirs(t *testing.T) (out, queue string) {
	t.Helper()
	dir := t.TempDir()
	queue = filepath.Join(dir, "queue")
	out = filepath.Join(queue, "pending")
	if err := os.MkdirAll(queue, 0o755); err != nil {
		t.Fatal(err)
	}
	return out, queue
}

func fakePRs(t *testing.T, open, merged string) {
	t.Helper()
	specs := fakePATH(t)
	log := filepath.Join(t.TempDir(), "gh.log")
	fakeTool(t, specs, "gh", fakeSpec{
		Log: log,
		Rules: []fakeRule{
			{Arg: 6, Equals: "open", Stdout: open},
			{Arg: 6, Equals: "merged", Stdout: merged},
		},
		Default: fakeRule{Stdout: "[]"},
	})
}

func TestCutKindFixRefusesWhenAnOpenPRAlreadyCarriesTheIssue(t *testing.T) {
	out, queue := cutFixDirs(t)
	fakePRs(t,
		prListJSON(t, map[string]any{
			"number":      1960,
			"title":       "the widget",
			"body":        "Fixes #1979\n\nthe repair.",
			"headRefName": "rowan/the-widget",
		}),
		"[]",
	)

	code, stdout, stderr := runValidatedCut(t, cutFixArgs(t, 1979, "the widget", out, queue)...)
	if code != 2 {
		t.Fatalf("exit = %d, want 2 (refuse a card an open PR already carries); stdout=%q stderr=%q", code, stdout, stderr)
	}
	if !strings.Contains(stderr, "CUT REFUSED") || !strings.Contains(stderr, "already carries #1979") || !strings.Contains(stderr, "1960") {
		t.Fatalf("stderr=%q, want CUT REFUSED naming PR 1960 and #1979", stderr)
	}
	if !strings.Contains(stderr, "(") {
		t.Fatalf("stderr=%q, want a remedy in parentheses", stderr)
	}
	if names := mdFiles(t, out); len(names) != 0 {
		t.Fatalf("cards written despite the refusal: %v", names)
	}
}

func TestCutKindFixRefusesWhenThePRTitleNamesTheIssue(t *testing.T) {
	out, queue := cutFixDirs(t)
	fakePRs(t,
		prListJSON(t, map[string]any{
			"number":      1630,
			"title":       "cut: skip a card already in flight (#1979)",
			"body":        "the repair",
			"headRefName": "rowan/cut-skip",
		}),
		"[]",
	)

	code, _, stderr := runValidatedCut(t, cutFixArgs(t, 1979, "cut skip", out, queue)...)
	if code != 2 {
		t.Fatalf("title match: exit = %d, want 2; stderr=%q", code, stderr)
	}
	if !strings.Contains(stderr, "already carries #1979") || !strings.Contains(stderr, "1630") {
		t.Fatalf("title match: stderr=%q, want PR 1630 carrying #1979", stderr)
	}
	if names := mdFiles(t, out); len(names) != 0 {
		t.Fatalf("title match: cards written: %v", names)
	}
}

func TestCutKindFixRefusesWhenThePRBranchNamesTheIssue(t *testing.T) {
	out, queue := cutFixDirs(t)
	fakePRs(t,
		prListJSON(t, map[string]any{
			"number":      1873,
			"title":       "the widget",
			"body":        "no issue ref here",
			"headRefName": "johnny/1979-the-widget",
		}),
		"[]",
	)

	code, _, stderr := runValidatedCut(t, cutFixArgs(t, 1979, "the widget", out, queue)...)
	if code != 2 {
		t.Fatalf("branch match: exit = %d, want 2; stderr=%q", code, stderr)
	}
	if !strings.Contains(stderr, "already carries #1979") || !strings.Contains(stderr, "1873") {
		t.Fatalf("branch match: stderr=%q, want PR 1873 carrying #1979", stderr)
	}
	if !strings.Contains(stderr, "johnny/1979-the-widget") {
		t.Fatalf("branch match: stderr=%q, want the branch named", stderr)
	}
	if names := mdFiles(t, out); len(names) != 0 {
		t.Fatalf("branch match: cards written: %v", names)
	}
}

func TestCutKindFixRefusesWhenARecentlyMergedPRAlreadyCarriesTheIssue(t *testing.T) {
	out, queue := cutFixDirs(t)
	fakePRs(t, "[]",
		prListJSON(t, map[string]any{
			"number":      1952,
			"title":       "the widget",
			"body":        "Closes #1979",
			"headRefName": "rowan/the-widget",
		}),
	)

	code, _, stderr := runValidatedCut(t, cutFixArgs(t, 1979, "the widget", out, queue)...)
	if code != 2 {
		t.Fatalf("merged: exit = %d, want 2; stderr=%q", code, stderr)
	}
	if !strings.Contains(stderr, "already carries #1979") || !strings.Contains(stderr, "1952") {
		t.Fatalf("merged: stderr=%q, want merged PR 1952 carrying #1979", stderr)
	}
	if names := mdFiles(t, out); len(names) != 0 {
		t.Fatalf("merged: cards written: %v", names)
	}
}

func TestCutKindFixCutsWhenNoPRCarriesTheIssue(t *testing.T) {
	out, queue := cutFixDirs(t)
	fakePRs(t,
		prListJSON(t, map[string]any{
			"number":      1,
			"title":       "unrelated",
			"body":        "Fixes #2",
			"headRefName": "rowan/unrelated",
		}),
		"[]",
	)

	code, stdout, stderr := runValidatedCut(t, cutFixArgs(t, 1979, "the widget", out, queue)...)
	if code != 0 {
		t.Fatalf("no match: exit = %d, want 0; stderr=%q", code, stderr)
	}
	if !strings.Contains(stdout, "CUT CARD") || !strings.Contains(stdout, "kind=fix") {
		t.Fatalf("no match: stdout=%q, want CUT CARD kind=fix", stdout)
	}
	if names := mdFiles(t, out); len(names) != 1 {
		t.Fatalf("no match: cards = %v, want one", names)
	}
}

func TestCutKindFixRefusesWhenTheForgeCannotBeRead(t *testing.T) {
	out, queue := cutFixDirs(t)
	specs := fakePATH(t)
	fakeTool(t, specs, "gh", fakeSpec{Default: fakeRule{Exit: 1, Stderr: "gh: HTTP 401"}})

	code, _, stderr := runValidatedCut(t, cutFixArgs(t, 1979, "the widget", out, queue)...)
	if code != 2 {
		t.Fatalf("unread forge: exit = %d, want 2 (do not cut while the forge is unread); stderr=%q", code, stderr)
	}
	if !strings.Contains(stderr, "CUT REFUSED") {
		t.Fatalf("unread forge: stderr=%q, want CUT REFUSED", stderr)
	}
	if names := mdFiles(t, out); len(names) != 0 {
		t.Fatalf("unread forge: cards written: %v", names)
	}
}
