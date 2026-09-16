package pulse

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTestFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// ghFixture puts a fake `gh` on PATH that serves issue list JSON and records every argv it
// was called with, and returns the path of that record. The record is the proof: a real gh
// writes nothing to it, so a test that asserts the argv cannot pass on a bench that merely
// happens to be logged in.
func ghFixture(t *testing.T, dir, jsonBody string) string {
	t.Helper()
	specs := fakePATH(t)
	log := filepath.Join(dir, "gh-argv.log")
	fakeTool(t, specs, "gh", fakeSpec{Log: log, Default: fakeRule{StdoutFile: jsonBody}})
	return log
}

// ghCalls is the fake gh's argv record, one line per invocation.
func ghCalls(t *testing.T, log string) []string {
	t.Helper()
	raw, err := os.ReadFile(log)
	if err != nil {
		return nil
	}
	return nonempty(string(raw))
}

func TestPoolReadsIssueLabel(t *testing.T) {
	dir := t.TempDir()
	jsonBody := writeTestFile(t, dir, "issues.json", `[
  {"number": 1, "title": "one", "labels": [{"name": "card"}], "body": ""},
  {"number": 2, "title": "two", "labels": [{"name": "card"}], "body": ""},
  {"number": 3, "title": "three", "labels": [], "body": "tool: x\ncommand: y\nverbatim output: z\nexpected: a\nsmallest fix: b"},
  {"number": 4, "title": "four", "labels": [], "body": "not a card"}
]`)
	ghLog := ghFixture(t, dir, jsonBody)

	root := filepath.Join(dir, "root")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	sources := writeTestFile(t, dir, "sources.tsv", "issues\towner/repo\tfix\n")

	var out, errb bytes.Buffer
	code := Pool(PoolInput{Sources: sources, Root: root, Stdout: &out, Stderr: &errb})
	if code != 0 {
		t.Fatalf("Pool exit = %d, stderr=%s", code, errb.String())
	}
	// The fake answered, not the real gh: the argv it recorded is the only thing that can
	// have produced the rows below.
	calls := ghCalls(t, ghLog)
	if len(calls) != 1 || !strings.Contains(calls[0], "gh issue list --repo owner/repo") {
		t.Fatalf("the fake gh recorded %v; want one `gh issue list --repo owner/repo`", calls)
	}
	if !strings.Contains(out.String(), "candidates=3") || !strings.Contains(out.String(), "issues=3") {
		t.Fatalf("POOL OK line wrong: %q", out.String())
	}
	raw, err := os.ReadFile(filepath.Join(root, "pool.tsv"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("want 3 pool rows, got %d: %q", len(lines), string(raw))
	}
	for i, wantID := range []string{"1", "2", "3"} {
		if got := strings.Split(lines[i], "\t")[1]; got != wantID {
			t.Fatalf("row %d id = %q, want %q", i, got, wantID)
		}
	}
}

func TestPoolRefusesUnreadableSource(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "root")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}

	// A gh that exits 1: the issues source is unreadable.
	specs := fakePATH(t)
	ghLog := filepath.Join(dir, "gh-argv.log")
	fakeTool(t, specs, "gh", fakeSpec{Log: ghLog, Default: fakeRule{Exit: 1}})

	sources := writeTestFile(t, dir, "sources.tsv", "issues\towner/repo\tfix\n")
	var out, errb bytes.Buffer
	code := Pool(PoolInput{Sources: sources, Root: root, Stdout: &out, Stderr: &errb})
	if code != 2 {
		t.Fatalf("exit = %d, want 2; stdout=%q stderr=%q", code, out.String(), errb.String())
	}
	if !strings.Contains(errb.String(), "POOL REFUSED source=issues:owner/repo") {
		t.Fatalf("refusal line wrong: %q", errb.String())
	}
	if _, err := os.Stat(filepath.Join(root, "pool.tsv")); !os.IsNotExist(err) {
		t.Fatalf("pool.tsv was written despite the refusal")
	}
	if calls := ghCalls(t, ghLog); len(calls) != 1 {
		t.Fatalf("the fake gh recorded %v; the refusal must come from the fake, not a real gh", calls)
	}
}

// pool-reads-open-non-draft-prs (issue #638): a `prs` source kind pools open, non-draft
// pull requests as read candidates -- one candidate per PR, the read half of the loop
// harvest itself describes ("cuts a read card per PR"). A draft PR is nowhere, and the
// fake gh's argv record is the proof the rows came from the fake, never the network.
func TestPoolPRsSourcePoolsOpenNonDraftPRs(t *testing.T) {
	dir := t.TempDir()
	jsonBody := writeTestFile(t, dir, "prs.json", `[
  {"number": 11, "title": "one", "isDraft": false},
  {"number": 12, "title": "two", "isDraft": true},
  {"number": 13, "title": "three", "isDraft": false}
]`)
	ghLog := ghFixture(t, dir, jsonBody)

	root := filepath.Join(dir, "root")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	sources := writeTestFile(t, dir, "sources.tsv", "prs\tmas-bandwidth/nova-tools\tread\n")

	var out, errb bytes.Buffer
	code := Pool(PoolInput{Sources: sources, Root: root, Stdout: &out, Stderr: &errb})
	if code != 0 {
		t.Fatalf("pool exit = %d, want 0; stderr=%q", code, errb.String())
	}
	// The fake answered, not the real gh: the argv it recorded is the only thing that can
	// have produced the rows below.
	calls := ghCalls(t, ghLog)
	if len(calls) != 1 || !strings.Contains(calls[0], "gh pr list --repo mas-bandwidth/nova-tools") {
		t.Fatalf("the fake gh recorded %v; want one `gh pr list --repo mas-bandwidth/nova-tools`", calls)
	}
	if !strings.Contains(out.String(), "candidates=2") || !strings.Contains(out.String(), "prs=2") {
		t.Fatalf("POOL OK line wrong: %q", out.String())
	}
	raw, err := os.ReadFile(filepath.Join(root, "pool.tsv"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
	want := []string{
		"prs\t11\tread\tone\tread",
		"prs\t13\tread\tthree\tread",
	}
	if len(lines) != len(want) {
		t.Fatalf("want %d pool rows, got %d: %q", len(want), len(lines), string(raw))
	}
	for i, w := range want {
		if lines[i] != w {
			t.Fatalf("row %d = %q, want %q", i, lines[i], w)
		}
	}
}

// pool-roadmap-kind-is-card-kind: a roadmap source enumerates one row per cell whose :card
// names a template; the kind column of pool.tsv is the card kind (the template name), not
// the source kind, because cut keys rule 6 (and rule 7's routing) on row.Kind, and a
// "roadmap" kind drives every read candidate into the writing-template branch and rule 6
// RED on the no-build text card.
func TestPoolRoadmapKindIsCardKind(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "root")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	roadmap := writeTestFile(t, dir, "roadmap.sexp",
		"((:id \"pulse-read\" :title \"read a pool\" :card \"read\")\n"+
			" (:id \"pulse-fix\" :title \"fix the pool\" :card \"fix\")\n"+
			" (:id \"pulse-plan\" :title \"plan the work\" :state \"missing\"))\n")
	sources := writeTestFile(t, dir, "sources.tsv", "roadmap\t"+roadmap+"\tfix\n")

	var out, errb bytes.Buffer
	code := Pool(PoolInput{Sources: sources, Root: root, Stdout: &out, Stderr: &errb})
	if code != 0 {
		t.Fatalf("pool exit = %d, want 0; stdout=%q stderr=%q", code, out.String(), errb.String())
	}
	if !strings.Contains(out.String(), "candidates=2") || !strings.Contains(out.String(), "roadmap=2") {
		t.Fatalf("POOL OK line wrong: %q", out.String())
	}
	raw, err := os.ReadFile(filepath.Join(root, "pool.tsv"))
	if err != nil {
		t.Fatal(err)
	}
	wantKinds := map[string]string{"pulse-read": "read", "pulse-fix": "fix"}
	for _, line := range strings.Split(strings.TrimRight(string(raw), "\n"), "\n") {
		parts := strings.Split(line, "\t")
		if len(parts) != 5 {
			t.Fatalf("row %q does not have 5 fields", line)
		}
		if parts[0] != "roadmap" {
			t.Fatalf("row %q source = %q, want %q", line, parts[0], "roadmap")
		}
		want, ok := wantKinds[parts[1]]
		if !ok {
			t.Fatalf("row %q id = %q, want one of pulse-read|pulse-fix", line, parts[1])
		}
		if parts[2] != want {
			t.Fatalf("row %q kind = %q, want %q (card kind, not source kind)", line, parts[2], want)
		}
	}
}
