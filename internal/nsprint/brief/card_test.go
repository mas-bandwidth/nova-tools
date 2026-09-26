package brief

import (
	"strings"
	"testing"
)

// TestRenderCardByKind (#4095): a card's brief is rendered from its record by
// its kind (build, fix, read, rebase; work is build, review is read), with
// the title's fields, the card block and the typed-line contract; a kind with
// no template and a card with no model refuse naming the field.
func TestRenderCardByKind(t *testing.T) {
	t.Parallel()

	rec := map[string]string{
		"title":  "friend serve cards | PATHS: internal/nsprint/life/ | BASE: dev base-sha: abc123 | DONE-WHEN: `go test ./x` exits 0",
		"origin": "issue:nova-tools#4095", "stream": "nova-sprint + merge + bus",
		"pr": "4100", "head": "0123456789ab",
	}
	for kind, heading := range map[string]string{"build": "# Build brief", "work": "# Build brief", "fix": "# Fix brief",
		"read": "# Read brief", "review": "# Read brief", "rebase": "# Rebase brief"} {
		rec["kind"] = kind
		b, err := RenderCard("k-"+kind, rec, "opus-5.5")
		if err != nil {
			t.Fatalf("%s: %v", kind, err)
		}
		for _, want := range []string{"CARD: k-" + kind + "\n", "RULES-SHA: ", heading, "PATHS: internal/nsprint/life/",
			"BASE: dev\n", "base-sha: abc123", "DONE-WHEN: `go test ./x` exits 0", "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>",
			"ORIGIN: issue:nova-tools#4095", "PR: 4100", "HEAD: 0123456789ab", CardContract} {
			if !strings.Contains(string(b), want) {
				t.Fatalf("%s brief lacks %q:\n%s", kind, want, b)
			}
		}
	}
	rec["kind"] = "harvest"
	if _, err := RenderCard("k-h", rec, "opus-5.5"); err == nil || !strings.Contains(err.Error(), "field=kind want=build|fix|read|rebase got=harvest") {
		t.Fatalf("harvest: %v", err)
	}
	rec["kind"] = "build"
	if _, err := RenderCard("k-m", rec, ""); err == nil || !strings.Contains(err.Error(), "field=model") {
		t.Fatalf("no model: %v", err)
	}
	rec["title"] += " | MODEL: fable-5.1"
	if b, err := RenderCard("k-t", rec, ""); err != nil || !strings.Contains(string(b), "Co-Authored-By: Claude Fable 5.1 <") {
		t.Fatalf("title MODEL: %v\n%s", err, b)
	}
}
