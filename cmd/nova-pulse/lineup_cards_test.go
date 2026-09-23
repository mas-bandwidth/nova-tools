package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// THE CARD HALF OF THE CODING-SPRINT PREFLIGHT (#2944). Every card queued for the sprint
// must lint at its base (the R4 lint, #2636), sit on a route in the allowed list (#2895),
// be one a ci card can be cut for (#2842, dry run), and carry NO-SUBAGENTS (#2533). One
// card that fails any of the four makes the lineup exit 1 and the RED line names it.

// lineupCardText is a coding card of the 2026-09-22 shape with its route and its
// NO-SUBAGENTS line as given; an empty value leaves the line out.
func lineupCardText(name, model, noSubagents, paths string) string {
	lines := []string{
		"RESULT " + name + " sha=09fbedc90521 -- a coding card",
		"KIND: fix",
		"LEG: go",
		"REPO: mas-bandwidth/nova-tools",
		"BASE: dev",
		"base-repo: https://github.com/mas-bandwidth/nova-tools.git",
		"base-sha: 09fbedc905218d5d4bdf5d039d0baff366ef2545",
		"PATHS: " + paths,
		"DONE-WHEN: `go test ./internal/pulse/ -run TestX` passes",
	}
	if model != "" {
		lines = append(lines, "MODEL: "+model)
	}
	if noSubagents != "" {
		lines = append(lines, "NO-SUBAGENTS: "+noSubagents)
	}
	lines = append(lines, "", "STEP 1. cd repo", "")
	return strings.Join(lines, "\n")
}

// lineupFakeLint is the R4 lint as the lineup calls it: a command handed `--card <path>`,
// exit 0 on a clean card, exit 2 with a LINT DRIFT line on a card that does not lint at
// base. It drifts the one card whose PATHS names a file that is not at base-sha, which is
// the nx-f19 shape of 2026-09-22.
func lineupFakeLint(t *testing.T, dir string) string {
	t.Helper()
	script := filepath.Join(dir, "fake-lint")
	body := `#!/usr/bin/env bash
card=""
while [ $# -gt 0 ]; do case "$1" in --card) card="$2"; shift 2;; *) shift;; esac; done
if grep -q 'not-at-base.go' "$card"; then
  echo "LINT DRIFT card=$(basename "$card") paths-at-base: 8: internal/pulse/not-at-base.go does not resolve at base-sha"
  exit 2
fi
echo "LINT OK card=$(basename "$card")"
`
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return script
}

func TestLineupCodingCardChecks(t *testing.T) {
	good := lineupCardText("card-good", "opencode/deepseek-v4-flash", "work in this session only", "internal/pulse/wire.go internal/pulse/wire_test.go")
	bad := map[string]struct {
		text  string
		check string
	}{
		"card-lint":      {lineupCardText("card-lint", "opencode/deepseek-v4-flash", "work in this session only", "internal/pulse/not-at-base.go"), "lint"},
		"card-dropped":   {lineupCardText("card-dropped", "openrouter/gpt-nano", "work in this session only", "internal/pulse/wire.go"), "route"},
		"card-subagents": {lineupCardText("card-subagents", "opencode/deepseek-v4-flash", "", "internal/pulse/wire.go"), "no-subagents"},
		"card-no-ci":     {lineupCardText("card-no-ci", "opencode/deepseek-v4-flash", "work in this session only", "docs/NOTES.md"), "ci-dry-run"},
	}
	queue := func(t *testing.T, cards map[string]string) string {
		t.Helper()
		dir := t.TempDir()
		q := filepath.Join(dir, "queue")
		if err := os.MkdirAll(filepath.Join(q, "pending"), 0o755); err != nil {
			t.Fatal(err)
		}
		for name, text := range cards {
			if err := os.WriteFile(filepath.Join(q, "pending", name+".md"), []byte(text), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		// The allowed list after the 10:31 PM cut (#2895): the dropped route is not on it.
		routes := "# allowed coding routes\nopencode/deepseek-v4-flash\nopencode/kimi-k3\n"
		if err := os.WriteFile(filepath.Join(q, "ROUTES-code"), []byte(routes), 0o644); err != nil {
			t.Fatal(err)
		}
		return q
	}
	run := func(t *testing.T, q string) (int, string, string) {
		t.Helper()
		var out, errb bytes.Buffer
		code := lineupCardChecks([]string{"--queue", q, "--lint-cmd", lineupFakeLint(t, t.TempDir())}, &out, &errb)
		return code, out.String(), errb.String()
	}

	t.Run("every card clean exits 0", func(t *testing.T) {
		code, out, errs := run(t, queue(t, map[string]string{"card-good": good}))
		if code != 0 {
			t.Fatalf("exit %d, want 0\nstdout:\n%s\nstderr:\n%s", code, out, errs)
		}
		if strings.Contains(out, "RED") || !strings.Contains(out, "LINEUP OK cards=1") {
			t.Fatalf("want one LINEUP OK line for one card, got:\n%s", out)
		}
	})
	for name, c := range bad {
		t.Run(name+" fails "+c.check, func(t *testing.T) {
			code, out, errs := run(t, queue(t, map[string]string{"card-good": good, name: c.text}))
			if code != 1 {
				t.Fatalf("exit %d, want 1\nstdout:\n%s\nstderr:\n%s", code, out, errs)
			}
			want := "LINEUP RED card=" + name + ".md check=" + c.check + " "
			if !strings.Contains(out, want) {
				t.Fatalf("want a line containing %q, got:\n%s", want, out)
			}
			if strings.Contains(out, "card=card-good.md") {
				t.Fatalf("the clean card drew a RED line:\n%s", out)
			}
		})
	}
	t.Run("an empty queue is RED, never OK", func(t *testing.T) {
		code, out, _ := run(t, queue(t, nil))
		if code != 1 || !strings.Contains(out, "LINEUP RED card=- check=queue ") {
			t.Fatalf("exit %d, want 1 with a queue RED line, got:\n%s", code, out)
		}
	})
	t.Run("a lint that cannot run is RED for the card", func(t *testing.T) {
		q := queue(t, map[string]string{"card-good": good})
		var out, errb bytes.Buffer
		code := lineupCardChecks([]string{"--queue", q, "--lint-cmd", filepath.Join(t.TempDir(), "no-such-lint")}, &out, &errb)
		if code != 1 || !strings.Contains(out.String(), "LINEUP RED card=card-good.md check=lint ") {
			t.Fatalf("exit %d, want 1 with a lint RED line, got:\n%s", code, out.String())
		}
	})
	t.Run("no queue flag exits 2", func(t *testing.T) {
		var out, errb bytes.Buffer
		if code := lineupCardChecks(nil, &out, &errb); code != 2 {
			t.Fatalf("exit %d, want 2", code)
		}
	})
}
