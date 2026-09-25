package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
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

// bannerVerbCount reads a verb count out of a usage banner: the number immediately
// before the word `verbs`, as a digit or as one of the words a sentence would use.
// The second result is false when the banner claims no count at all.
func bannerVerbCount(line string) (int, bool) {
	words := map[string]int{
		"one": 1, "two": 2, "three": 3, "four": 4, "five": 5, "six": 6, "seven": 7,
		"eight": 8, "nine": 9, "ten": 10, "eleven": 11, "twelve": 12, "thirteen": 13,
		"fourteen": 14, "fifteen": 15, "sixteen": 16, "seventeen": 17, "eighteen": 18,
		"nineteen": 19, "twenty": 20,
	}
	fields := strings.Fields(line)
	for i, f := range fields {
		if strings.Trim(f, ",.") != "verbs" || i == 0 {
			continue
		}
		prev := strings.ToLower(strings.Trim(fields[i-1], ",."))
		if n, ok := words[prev]; ok {
			return n, true
		}
		if n, err := strconv.Atoi(prev); err == nil {
			return n, true
		}
	}
	return 0, false
}

// The banner is the first line of `nova-pulse help` and the first sentence a first
// run reads, so a verb count in it is a number the usage under it must match. The
// usage declares every verb the binary answers, and the count rots the day one more
// lands, so the banner names none (docs/ONBOARDING.md point 1).
func TestHelpBannerVerbCountMatchesUsage(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{"help"}, &out, &errb, time.Now().UTC()); code != 0 {
		t.Fatalf("help exit = %d, stderr=%s", code, errb.String())
	}
	lines := strings.Split(out.String(), "\n")
	banner := lines[0]
	verbs := map[string]bool{}
	for _, line := range lines {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[0] == "nova-pulse" {
			verbs[fields[1]] = true
		}
	}
	claimed, ok := bannerVerbCount(banner)
	if ok && claimed != len(verbs) {
		t.Errorf("the banner claims %d verbs and the usage declares %d: %q", claimed, len(verbs), banner)
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
