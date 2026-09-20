package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
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

func TestCutKindFixCutsWhenMergedPRIsOutsideTheLookbackWindow(t *testing.T) {
	out, queue := cutFixDirs(t)
	old := time.Now().UTC().Add(-30 * 24 * time.Hour).Format(time.RFC3339)
	fakePRs(t, "[]",
		prListJSON(t, map[string]any{
			"number":      1952,
			"title":       "the widget",
			"body":        "Closes #1979",
			"headRefName": "rowan/the-widget",
			"mergedAt":    old,
		}),
	)

	code, stdout, stderr := runValidatedCut(t, cutFixArgs(t, 1979, "the widget", out, queue)...)
	if code != 0 {
		t.Fatalf("merged outside window: exit = %d, want 0; stderr=%q", code, stderr)
	}
	if !strings.Contains(stdout, "CUT CARD") {
		t.Fatalf("merged outside window: stdout=%q, want CUT CARD", stdout)
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

func fillerPRs(n, from int) []map[string]any {
	rows := make([]map[string]any, n)
	for i := 0; i < n; i++ {
		rows[i] = map[string]any{
			"number":      from + i,
			"title":       "unrelated",
			"body":        "no carry",
			"headRefName": "rowan/unrelated",
		}
	}
	return rows
}

// cut-kind-fix-refuses-when-the-matching-open-pr-is-beyond-the-first-hundred
// (#2041 HOLD): 218 open PRs were live; --limit 100 ended at #1736 and omitted
// #1730 Closes #1649, so cut wrote a duplicate. A fixture whose match is the
// 101st open row must refuse and write no card.
func TestCutKindFixRefusesWhenTheMatchingOpenPRIsBeyondTheFirstHundred(t *testing.T) {
	out, queue := cutFixDirs(t)
	page1 := fillerPRs(100, 3000)
	match := map[string]any{
		"number":      1730,
		"title":       "the repair",
		"body":        "Closes #1649",
		"headRefName": "rowan/closes-1649",
	}
	all := append(append([]map[string]any{}, page1...), match)
	specs := fakePATH(t)
	fakeTool(t, specs, "gh", fakeSpec{
		Rules: []fakeRule{
			{Arg: 6, Equals: "merged", Stdout: "[]"},
			{Arg: 8, Equals: "100", Stdout: prListJSON(t, page1...)},
		},
		Default: fakeRule{Stdout: prListJSON(t, all...)},
	})

	code, stdout, stderr := runValidatedCut(t, cutFixArgs(t, 1649, "the repair", out, queue)...)
	if code != 2 {
		t.Fatalf("match on page 2: exit = %d, want 2 (PR 1730 Closes #1649 is the 101st open row); stdout=%q stderr=%q",
			code, stdout, stderr)
	}
	if !strings.Contains(stderr, "already carries #1649") || !strings.Contains(stderr, "1730") {
		t.Fatalf("match on page 2: stderr=%q, want CUT REFUSED naming PR 1730 and #1649", stderr)
	}
	if names := mdFiles(t, out); len(names) != 0 {
		t.Fatalf("match on page 2: cards written: %v", names)
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
