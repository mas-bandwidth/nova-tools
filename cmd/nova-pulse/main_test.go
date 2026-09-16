package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeMainFile(t *testing.T, dir, name, content string) string {
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

// mainGhFixture puts a fake `gh` on PATH that serves issue list JSON and records every
// argv it was called with, and returns the path of that record.
func mainGhFixture(t *testing.T, dir, jsonBody string) string {
	t.Helper()
	specs := fakePATH(t)
	log := filepath.Join(dir, "gh-argv.log")
	fakeTool(t, specs, "gh", fakeSpec{Log: log, Default: fakeRule{StdoutFile: jsonBody}})
	return log
}

func TestHelpListsOnlyBuiltVerbs(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{"help"}, &out, &errb, time.Now().UTC()); code != 0 {
		t.Fatalf("help exit = %d, stderr=%s", code, errb.String())
	}
	usage := out.String()
	shipped := map[string]bool{"pool": true, "launch": true, "harvest": true, "manager": true, "cut": true}
	unshipped := map[string]bool{"width": true}
	for _, line := range strings.Split(usage, "\n") {
		verb := strings.Fields(line)
		if len(verb) < 2 || verb[0] != "nova-pulse" {
			continue
		}
		name := verb[1]
		marked := strings.Contains(line, "not yet implemented")
		switch {
		case unshipped[name] && !marked:
			t.Errorf("help lists %q as available; it is unshipped and must read \"not yet implemented\": %q", name, line)
		case shipped[name] && marked:
			t.Errorf("help marks shipped verb %q as \"not yet implemented\": %q", name, line)
		}
	}
}

func TestHelpDropsNotYetImplementedForHarvest(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{"help"}, &out, &errb, time.Now().UTC()); code != 0 {
		t.Fatalf("help exit = %d, stderr=%s", code, errb.String())
	}
	for _, line := range strings.Split(out.String(), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[0] == "nova-pulse" && fields[1] == "harvest" {
			if strings.Contains(line, "(not yet implemented)") {
				t.Errorf("harvest help still reads \"not yet implemented\": %q", line)
			}
		}
	}
	if code := run([]string{"harvest", "--root", "/tmp/root"}, &out, &errb, time.Now().UTC()); code != 2 {
		t.Fatalf("harvest without --id exit = %d, want 2", code)
	}
	if !strings.Contains(errb.String(), "--id is required") {
		t.Errorf("harvest without --id did not name the remedy: %q", errb.String())
	}
}

// issue #515, the half that outlived the landings: docs/SPEC-PULSE.md's "The verbs"
// block claims to be the string `nova-pulse help` prints, byte for byte, so it is
// a line a stranger pastes. A verb the binary refuses with "not implemented in
// this card" must read "(not yet implemented)" there — a first run following the
// block otherwise hits a dead end on a verb the block called available
// (docs/ONBOARDING.md point 1) — and a marker left on a verb that runs is the same
// lie the other way round, so it must go the day the verb lands.
func TestSpecVerbsBlockMarksUnshippedVerbs(t *testing.T) {
	doc, err := os.ReadFile(filepath.Join("..", "..", "docs", "SPEC-PULSE.md"))
	if err != nil {
		t.Fatal(err)
	}
	_, after, ok := strings.Cut(string(doc), "\n## The verbs\n")
	if !ok {
		t.Fatal("the spec has no verbs section")
	}
	_, after, ok = strings.Cut(after, "```\n")
	if !ok {
		t.Fatal("the verbs section has no block")
	}
	block, _, ok := strings.Cut(after, "```")
	if !ok {
		t.Fatal("the verbs block does not close")
	}
	for _, line := range strings.Split(block, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 || fields[0] != "nova-pulse" || fields[1] == "version" || fields[1] == "help" {
			continue
		}
		var out, errb bytes.Buffer
		run([]string{fields[1]}, &out, &errb, time.Now().UTC())
		refused := strings.Contains(errb.String(), "not implemented in this card")
		marked := strings.Contains(line, "(not yet implemented)")
		switch {
		case refused && !marked:
			t.Errorf("the spec's verbs block lists %q as available; the verb refuses \"not implemented in this card\" and the line must read \"(not yet implemented)\": %q", fields[1], line)
		case marked && !refused:
			t.Errorf("the spec's verbs block marks %q \"(not yet implemented)\" and the verb runs; the marker must go when the verb lands: %q", fields[1], line)
		}
	}
}

// cut-refusal-names-models-shape: a cut whose templates dir has no models.tsv is refused,
// and the refusal names the two-line shape flash <model id> / pro <model id> (issue #633).
func TestCutRefusalNamesModelsShape(t *testing.T) {
	dir := t.TempDir()
	pool := writeMainFile(t, dir, "pool.tsv", "mas-bandwidth/nova-tools\t1\tread\tTitle\tread\n")
	templates := filepath.Join(dir, "templates")
	if err := os.MkdirAll(templates, 0o755); err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	code := run([]string{"cut", "--pool", pool, "--templates", templates, "--out", filepath.Join(dir, "out"), "--root", filepath.Join(dir, "root")}, &out, &errb, time.Now().UTC())
	if code != 2 {
		t.Fatalf("cut missing models.tsv exit = %d, want 2; stderr=%s", code, errb.String())
	}
	if !strings.Contains(errb.String(), "flash <model id>") || !strings.Contains(errb.String(), "pro <model id>") {
		t.Fatalf("stderr=%q, want the two-line shape flash <model id> / pro <model id>", errb.String())
	}
}

// pool-reads-open-non-draft-prs (issue #638): a `prs` source kind pools open, non-draft
// pull requests as read candidates -- one candidate per PR, the read half of the loop
// harvest itself describes ("cuts a read card per PR"). A draft PR is nowhere, and the
// fake gh's argv record is the proof the rows came from the fake, never the network.
func TestPoolReadsOpenNonDraftPRs(t *testing.T) {
	dir := t.TempDir()
	jsonBody := writeMainFile(t, dir, "prs.json", `[
  {"number": 11, "title": "one", "isDraft": false},
  {"number": 12, "title": "two", "isDraft": true},
  {"number": 13, "title": "three", "isDraft": false}
]`)
	ghLog := mainGhFixture(t, dir, jsonBody)

	root := filepath.Join(dir, "root")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	sources := writeMainFile(t, dir, "sources.tsv", "prs\tmas-bandwidth/nova-tools\tread\n")

	var out, errb bytes.Buffer
	code := run([]string{"pool", "--sources", sources, "--root", root}, &out, &errb, time.Now().UTC())
	if code != 0 {
		t.Fatalf("pool exit = %d, want 0; stderr=%q", code, errb.String())
	}
	// The fake answered, not the real gh: the argv it recorded is the only thing that can
	// have produced the rows below.
	calls, err := os.ReadFile(ghLog)
	if err != nil || !strings.Contains(string(calls), "gh pr list --repo mas-bandwidth/nova-tools") {
		t.Fatalf("the fake gh recorded %q (err=%v); want one `gh pr list --repo mas-bandwidth/nova-tools`", calls, err)
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

func TestPoolMaxFlagBoundsCandidates(t *testing.T) {
	dir := t.TempDir()
	jsonBody := writeMainFile(t, dir, "issues.json", `[
  {"number": 1, "title": "one", "labels": [{"name": "card"}], "body": ""},
  {"number": 2, "title": "two", "labels": [{"name": "card"}], "body": ""},
  {"number": 3, "title": "three", "labels": [{"name": "card"}], "body": ""}
]`)
	ghLog := mainGhFixture(t, dir, jsonBody)

	root := filepath.Join(dir, "root")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	sources := writeMainFile(t, dir, "sources.tsv", "issues\towner/repo\tfix\n")

	var out, errb bytes.Buffer
	code := run([]string{"pool", "--sources", sources, "--root", root, "--max", "1"}, &out, &errb, time.Now().UTC())
	if code != 0 {
		t.Fatalf("pool exit = %d, stderr=%s", code, errb.String())
	}
	// The fake answered, not the real gh: the argv it recorded is the only thing that can
	// have produced the row below.
	calls, err := os.ReadFile(ghLog)
	if err != nil || !strings.Contains(string(calls), "gh issue list --repo owner/repo") {
		t.Fatalf("the fake gh recorded %q (err=%v); want one `gh issue list --repo owner/repo`", calls, err)
	}
	if !strings.Contains(out.String(), "candidates=1") || !strings.Contains(out.String(), "issues=1") {
		t.Fatalf("POOL OK line bounds not honored: %q", out.String())
	}
	raw, err := os.ReadFile(filepath.Join(root, "pool.tsv"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
	if len(lines) != 1 {
		t.Fatalf("want 1 pool row, got %d: %q", len(lines), string(raw))
	}
}
