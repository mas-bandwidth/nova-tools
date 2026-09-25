package play

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"strings"
	"testing"
)

func hashFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// Companion view for nova-tools #223: browsing shared moments must render an
// explicitly selected sample of Markdown records into a static timeline —
// every card linked back to its source — without rewriting any record, with
// unknown dates/authors staying unknown, and with excluded records never
// entering the output.
func TestViewRendersTwoLayoutsWithoutRewritingSources(t *testing.T) {
	dir := t.TempDir()
	frontmatter := writeSource(t, dir, "moment-one.md", `---
author: Emma
date: 2026-09-18
kind: human
---

# The brass fitting

We stayed late to watch the light.
`)
	trailer := writeSource(t, dir, "moment-two.md", `# Salt on the wind

Stella logged the evening watch.

Author: Stella
Date: 2026-09-19
Kind: ai
Supersedes: The brass fitting
`)
	nometa := writeSource(t, dir, "unfiled.md", `# Unfiled thought

No metadata here.
`)
	private := writeSource(t, dir, "draft-private.md", `---
author: Emma
date: 2026-09-20
kind: human
---

# Not for the timeline

This record was explicitly excluded.
`)

	before := map[string]string{
		frontmatter: hashFile(t, frontmatter),
		trailer:     hashFile(t, trailer),
		nometa:      hashFile(t, nometa),
		private:     hashFile(t, private),
	}

	out, err := View([]string{frontmatter, trailer, nometa, private}, []string{"draft*"}, 0)
	if err != nil {
		t.Fatalf("View: %v", err)
	}

	// Two different record layouts render side by side, each card naming author,
	// date, kind and its exact source.
	for _, want := range []string{
		"date=2026-09-18", "author=Emma", "kind=human", frontmatter, "The brass fitting",
		"date=2026-09-19", "author=Stella", "kind=ai", trailer, "Salt on the wind",
		"supersedes=The\\x20brass\\x20fitting",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("view output missing %q:\n%s", want, out)
		}
	}

	// Missing metadata stays missing: no guessed author, no guessed date.
	if !strings.Contains(out, "author=unknown") || !strings.Contains(out, "date=unknown") {
		t.Errorf("view output must mark the unfiled record author and date unknown:\n%s", out)
	}

	// Chronological order for known dates, unknowns last in selection order.
	iOne := strings.Index(out, "moment-one.md")
	iTwo := strings.Index(out, "moment-two.md")
	iNone := strings.Index(out, "unfiled.md")
	if iOne < 0 || iTwo < 0 || iNone < 0 {
		t.Fatalf("view output must link every card back to its source:\n%s", out)
	}
	if !(iOne < iTwo && iTwo < iNone) {
		t.Errorf("cards out of chronological order (one=%d two=%d unfiled=%d):\n%s", iOne, iTwo, iNone, out)
	}

	// The excluded record never enters generated output, by path or by content.
	if strings.Contains(out, "draft-private.md") || strings.Contains(out, "Not for the timeline") {
		t.Errorf("view output carries the excluded record:\n%s", out)
	}

	// Viewing rewrote nothing: every source is byte-identical afterwards.
	for path, h := range before {
		if got := hashFile(t, path); got != h {
			t.Errorf("viewing rewrote %s: hash %s, want %s", path, got, h)
		}
	}
}
