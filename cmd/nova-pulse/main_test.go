package main

import (
	"bytes"
	"fmt"
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
	shipped := map[string]bool{"pool": true, "launch": true, "harvest": true, "manager": true, "cut": true, "progress": true}
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

// cut-refusal-names-cost-table-shape: a cut whose templates dir has no benches.tsv (or
// routes.tsv) is refused, and the refusal names the shape: a `model` row of names, a
// `cost` row of `zero|flat|metered` with a usd per Mtok, and a `capability` row
// (SPEC-PULSE rule 7).
func TestCutRefusalNamesCostTableShape(t *testing.T) {
	dir := t.TempDir()
	pool := writeMainFile(t, dir, "pool.tsv", "mas-bandwidth/nova-tools\t1\tread\tTitle\tread\n")
	templates := filepath.Join(dir, "templates")
	if err := os.MkdirAll(templates, 0o755); err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	code := run([]string{"cut", "--pool", pool, "--templates", templates, "--out", filepath.Join(dir, "out"), "--root", filepath.Join(dir, "root")}, &out, &errb, time.Now().UTC())
	if code != 2 {
		t.Fatalf("cut missing cost table exit = %d, want 2; stderr=%s", code, errb.String())
	}
	if !strings.Contains(errb.String(), "benches.tsv") || !strings.Contains(errb.String(), "cost") || !strings.Contains(errb.String(), "capability") {
		t.Fatalf("stderr=%q, want the cost table shape named (benches.tsv, cost, capability)", errb.String())
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
	fields := strings.Fields(strings.TrimSpace(out.String()))
	wantFields := []string{
		"POOL", "OK",
		"sources=1", "candidates=2",
		"issues=0", "audits=0", "slices=0", "roadmap=0", "prs=2", "work=0",
		"next=0", "plan=0", "seen=0",
		"took=", "out=" + filepath.Join(root, "pool.tsv"),
	}
	if len(fields) != len(wantFields) {
		t.Fatalf("POOL OK line wrong: %q", out.String())
	}
	for i, w := range wantFields {
		if w == "took=" {
			if !strings.HasPrefix(fields[i], "took=") || fields[i] == "took=" {
				t.Fatalf("POOL OK field %d = %q, want took=<duration>: %q", i, fields[i], out.String())
			}
			continue
		}
		if fields[i] != w {
			t.Fatalf("POOL OK field %d = %q, want %q: %q", i, fields[i], w, out.String())
		}
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

// cut-max-bounds-the-cards-cut (#634): `nova-pulse cut --max 6` cuts six cards, never the
// whole pool. The dogfood probe ran
// `nova-pulse cut --pool root/pool.tsv --templates ./templates --out ./cards --root ./root
// --max 6` against an 18-candidate pool and got `CUT OK cards=18 skipped=0 flash=0 pro=18
// out=./cards` -- --max was a print cap only and cut everything. A cap a caller names on cut
// must bound the cards, the way pool --max bounds candidates.
func TestCutMaxBoundsCardsCut(t *testing.T) {
	dir := t.TempDir()
	var pool strings.Builder
	for i := 1; i <= 18; i++ {
		fmt.Fprintf(&pool, "root\t%d\tfix\tFix %d\tfix\n", i, i)
	}
	poolPath := writeMainFile(t, dir, "pool.tsv", pool.String())
	templates := filepath.Join("testdata", "templates")
	out := filepath.Join(dir, "cards")
	root := filepath.Join(dir, "root")

	var stdout, errb bytes.Buffer
	code := run([]string{"cut", "--pool", poolPath, "--templates", templates, "--out", out, "--root", root, "--max", "6"}, &stdout, &errb, time.Now().UTC())
	if code != 0 {
		t.Fatalf("cut exit = %d, want 0; stderr=%s", code, errb.String())
	}
	want := "CUT OK cards=6 skipped=0 zero=0 flat=6 metered=0 out=" + out
	if !strings.Contains(stdout.String(), want) {
		t.Fatalf("stdout=%q, want it to carry %q", stdout.String(), want)
	}
	md := 0
	entries, err := os.ReadDir(out)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".md") {
			md++
		}
	}
	if md != 6 {
		t.Fatalf("cards dir holds %d .md cards, want 6", md)
	}
}

// launch-passes-benches-through (issue #637): a launch with --benches <file> and
// --bench <names> hands both through to nova-swarm batch, so one pulse fills the Studio
// and Space as SPEC-SWARM's Benches section allows. Today the second bench is only
// reachable by calling nova-swarm batch yourself: launch does not know the flags.
func TestLaunchPassesBenchesThrough(t *testing.T) {
	dir := t.TempDir()
	specs := fakePATH(t)
	argvLog := filepath.Join(dir, "argv.log")
	fakeTool(t, specs, "nova-swarm", fakeSpec{Log: argvLog})

	root := filepath.Join(dir, "root")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	card := writeMainFile(t, dir, "card-a.md", "RESULT card-a sha=000000000000\nbody card-a\n")
	cards := writeMainFile(t, dir, "cards.tsv", "card-a\t-\tpro\t"+card+"\n")
	benches := writeMainFile(t, dir, "benches.tsv", "name\thost\troot\tcores\tharness\tauth\twall\n")

	var out, errb bytes.Buffer
	code := run([]string{"launch", "--cards", cards, "--root", root, "--slots", "6", "--deadline", "600", "--benches", benches, "--bench", "studio,space"}, &out, &errb, time.Now().UTC())
	if code != 0 {
		t.Fatalf("launch exit = %d, want 0; stderr=%q", code, errb.String())
	}
	raw, err := os.ReadFile(argvLog)
	if err != nil {
		t.Fatal(err)
	}
	log := string(raw)
	if !strings.Contains(log, "--benches "+benches) {
		t.Fatalf("nova-swarm batch argv lacks --benches: %q", log)
	}
	if !strings.Contains(log, "--bench studio,space") {
		t.Fatalf("nova-swarm batch argv lacks --bench: %q", log)
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

// cut-source-is-locator-not-kind (issue #631): the card's STEP 1 clone URL renders the
// locator the sources line declares (mas-bandwidth/nova-tools), never the source kind.
// Today pool writes the kind into pool.tsv field 1, so every issues card clones
// github.com/issues.git (the dogfood probe's quoted line); pool must write the locator,
// and cut renders <source> from it. This runs the pool then the cut command from the issue
// and asserts the expected STEP 1 line.
func TestCutSourceIsLocatorNotKind(t *testing.T) {
	dir := t.TempDir()
	jsonBody := writeMainFile(t, dir, "issues.json", `[
  {"number": 417, "title": "cut renders the kind into the clone URL", "labels": [{"name": "card"}], "body": ""}
]`)
	ghLog := mainGhFixture(t, dir, jsonBody)

	root := filepath.Join(dir, "root")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	sources := writeMainFile(t, dir, "sources.tsv", "issues\tmas-bandwidth/nova-tools\tfix\n")

	var out, errb bytes.Buffer
	code := run([]string{"pool", "--sources", sources, "--root", root}, &out, &errb, time.Now().UTC())
	if code != 0 {
		t.Fatalf("pool exit = %d, stderr=%s", code, errb.String())
	}
	calls, err := os.ReadFile(ghLog)
	if err != nil || !strings.Contains(string(calls), "gh issue list --repo mas-bandwidth/nova-tools") {
		t.Fatalf("the fake gh recorded %q (err=%v); want one `gh issue list --repo mas-bandwidth/nova-tools`", calls, err)
	}

	poolTSV := filepath.Join(root, "pool.tsv")
	raw, err := os.ReadFile(poolTSV)
	if err != nil {
		t.Fatal(err)
	}
	first := strings.SplitN(strings.TrimSpace(string(raw)), "\n", 2)[0]
	if got := strings.Split(first, "\t")[0]; got != "mas-bandwidth/nova-tools" {
		t.Fatalf("pool.tsv field 1 = %q, want %q (the locator, so cut renders the repo the card is about)", got, "mas-bandwidth/nova-tools")
	}

	templates := filepath.Join(dir, "templates")
	writeMainFile(t, templates, "models.tsv", "flash opencode/deepseek-v4-flash\npro opencode/deepseek-v4-pro\n")
	writeMainFile(t, templates, "fix.md", `RESULT <label> sha=<sha12>
You are a worker. The deadline is the machinery's.
STEP 1. mkdir -p scratch && git clone -q https://github.com/<source>.git . && git checkout -b <branch>
   check: git rev-parse HEAD prints a head.
STEP 2. Make the fix; report the red line and then the green line, one row per item.
STEP last. Write RESULT.md with line 1 equal to this card's line 1.`)

	code = run([]string{"cut", "--pool", poolTSV, "--templates", templates, "--out", filepath.Join(dir, "cards"), "--root", root}, &out, &errb, time.Now().UTC())
	if code != 0 {
		t.Fatalf("cut exit = %d, stderr=%s", code, errb.String())
	}
	card, err := os.ReadFile(filepath.Join(dir, "cards", "417.md"))
	if err != nil {
		t.Fatal(err)
	}
	step1 := ""
	for _, line := range strings.Split(string(card), "\n") {
		if strings.HasPrefix(line, "STEP 1") {
			step1 = line
			break
		}
	}
	if strings.Contains(step1, "https://github.com/issues.git") {
		t.Fatalf("STEP 1 = %q, still renders the source kind into the clone URL", step1)
	}
	if want := "https://github.com/mas-bandwidth/nova-tools.git"; !strings.Contains(step1, want) {
		t.Fatalf("STEP 1 = %q, want the clone URL %q (the locator, not the source kind)", step1, want)
	}
}
