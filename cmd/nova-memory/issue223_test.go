package main

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// nova-tools #223: nova-play companion view: browse shared moments without
// rewriting the record. The issue asks whether this belongs in nova-memory as
// a viewer command, and the answer this binary gives is "yes": a `view` verb
// renders an explicitly selected sample of Markdown records into a static
// timeline, every card linked back to its source, without writing anything.
//
// The tripwires on this card are the ones the issue names as the evidence
// before calling the experiment useful:
//
//   - two record layouts (frontmatter and trailer) render side by side;
//   - every card names its source, and the timeline orders known dates;
//   - missing metadata stays missing — no guessed author, no guessed date;
//   - excluded records never enter generated output;
//   - every source file is byte-identical before and after `view` ran.
//
// The last is the headline the issue title points at: a viewer must not
// rewrite a record, even when it reads one.

func issue223HashFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func issue223WriteSource(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// `nova-memory view` is the verb the issue asks this binary to grow. It is
// not present in the base tree, so the run on base is "unknown subcommand"
// and this test fails red on the untouched head. After the production
// change adds the verb — rendering through the same reader the issue
// describes — the test goes green. When the production change is reverted
// but this test is kept, the run falls back to "unknown subcommand" and
// the test goes red again.
func TestIssue223ViewRendersTwoLayoutsWithoutRewritingSources(t *testing.T) {
	dir := t.TempDir()
	frontmatter := issue223WriteSource(t, dir, "moment-one.md", `---
author: Emma
date: 2026-09-18
kind: human
---

# The brass fitting

We stayed late to watch the light.
`)
	trailer := issue223WriteSource(t, dir, "moment-two.md", `# Salt on the wind

Stella logged the evening watch.

Author: Stella
Date: 2026-09-19
Kind: ai
Supersedes: The brass fitting
`)
	nometa := issue223WriteSource(t, dir, "unfiled.md", `# Unfiled thought

No metadata here.
`)
	private := issue223WriteSource(t, dir, "draft-private.md", `---
author: Emma
date: 2026-09-20
kind: human
---

# Not for the timeline

This record was explicitly excluded.
`)

	before := map[string]string{
		frontmatter: issue223HashFile(t, frontmatter),
		trailer:     issue223HashFile(t, trailer),
		nometa:      issue223HashFile(t, nometa),
		private:     issue223HashFile(t, private),
	}

	exit, stdout, stderr := runCLI(t, "",
		"view",
		"--exclude", "draft*",
		"--max", "0",
		frontmatter, trailer, nometa, private,
	)
	if exit != 0 {
		t.Fatalf("exit = %d, want 0; stdout: %s\nstderr: %s", exit, stdout, stderr)
	}

	// Two different layouts render side by side, each card naming its
	// author, date, kind and the exact source it came from.
	for _, want := range []string{
		"VIEW OK",
		"date=2026-09-18", "author=Emma", "kind=human", frontmatter, "The brass fitting",
		"date=2026-09-19", "author=Stella", "kind=ai", trailer, "Salt on the wind",
		"supersedes=The\\x20brass\\x20fitting",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("view output missing %q:\n%s", want, stdout)
		}
	}

	// Missing metadata stays missing — a viewer guesses nothing.
	if !strings.Contains(stdout, "author=unknown") {
		t.Errorf("view output must mark the unfiled record author unknown:\n%s", stdout)
	}
	if !strings.Contains(stdout, "date=unknown") {
		t.Errorf("view output must mark the unfiled record date unknown:\n%s", stdout)
	}

	// Chronological order for known dates, unknowns last in selection order.
	iOne := strings.Index(stdout, "moment-one.md")
	iTwo := strings.Index(stdout, "moment-two.md")
	iNone := strings.Index(stdout, "unfiled.md")
	if iOne < 0 || iTwo < 0 || iNone < 0 {
		t.Fatalf("view output must link every card back to its source:\n%s", stdout)
	}
	if !(iOne < iTwo && iTwo < iNone) {
		t.Errorf("cards out of chronological order (one=%d two=%d unfiled=%d):\n%s",
			iOne, iTwo, iNone, stdout)
	}

	// The excluded record never enters generated output, by path or by
	// content. A viewer that quietly read it and surfaced its title would
	// betray exactly the seam this test is pinning.
	if strings.Contains(stdout, "draft-private.md") {
		t.Errorf("view output names the excluded record:\n%s", stdout)
	}
	if strings.Contains(stdout, "Not for the timeline") {
		t.Errorf("view output carries the excluded record's content:\n%s", stdout)
	}

	// The headline the issue title names: viewing rewrote nothing. Every
	// source file is byte-identical to what the test wrote before it ran.
	for path, want := range before {
		if got := issue223HashFile(t, path); got != want {
			t.Errorf("viewing rewrote %s: hash %s, want %s", path, got, want)
		}
	}
}
