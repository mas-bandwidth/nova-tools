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

func mainGhFixture(t *testing.T, dir, jsonBody string) {
	t.Helper()
	bin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\ncat " + `"` + strings.ReplaceAll(jsonBody, `"`, `\"`) + `"`
	if err := os.WriteFile(filepath.Join(bin, "gh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
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

func TestPoolMaxFlagBoundsCandidates(t *testing.T) {
	dir := t.TempDir()
	jsonBody := writeMainFile(t, dir, "issues.json", `[
  {"number": 1, "title": "one", "labels": [{"name": "card"}], "body": ""},
  {"number": 2, "title": "two", "labels": [{"name": "card"}], "body": ""},
  {"number": 3, "title": "three", "labels": [{"name": "card"}], "body": ""}
]`)
	mainGhFixture(t, dir, jsonBody)

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
