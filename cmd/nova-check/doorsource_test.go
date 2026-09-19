package main

import (
	"os"
	"strings"
	"testing"
)

// Defect #1451 gives every unusable invocation one line, and that line ends in the
// door `; run: nova-check help`. That sentence has one home: `refuse`, in main.go.
// #1794 needed the door on `version` and, because cmdVersion did not call refuse, it
// wrote the sentence out by hand in version.go -- a second copy. This test is a
// SOURCE sweep, not a behaviour test: the BEHAVIOUR is pinned by
// issue1451version_test.go, which stays green through this change and is what proves
// the printed line did not move. The literal is read out of main.go so the sweep
// follows the wording rather than a hand copy of it.

func TestTheDoorLiteralLivesInExactlyOnePlace(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("reading the package directory: %v", err)
	}
	var files []string
	for _, e := range entries {
		name := e.Name()
		if strings.HasSuffix(name, ".go") && !strings.HasSuffix(name, "_test.go") {
			files = append(files, name)
		}
	}
	if len(files) < 3 {
		t.Fatalf("found only %d non-test .go files; a sweep of almost nothing would pass by asking nothing", len(files))
	}

	mainSrc, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatalf("reading main.go: %v", err)
	}
	const refuseStart = "func refuse("
	i := strings.Index(string(mainSrc), refuseStart)
	if i < 0 {
		t.Fatalf("main.go no longer defines %s", refuseStart)
	}
	body := string(mainSrc)[i:]
	if j := strings.Index(body, "\n}\n"); j >= 0 {
		body = body[:j]
	}
	const doorStart = "; run: nova-check "
	k := strings.Index(body, doorStart)
	if k < 0 {
		t.Fatalf("the body of refuse does not contain the door prefix %q", doorStart)
	}
	rest := body[k:]
	end := strings.Index(rest, `\n`)
	if end < 0 {
		t.Fatalf("the door read out of refuse is not terminated by a \\n escape")
	}
	door := rest[:end]
	if door == "" {
		t.Fatalf("the door literal read out of main.go is empty")
	}

	var holders []string
	for _, name := range files {
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("reading %s: %v", name, err)
		}
		if strings.Contains(string(src), door) {
			holders = append(holders, name)
		}
	}

	if len(holders) != 1 || holders[0] != "main.go" {
		t.Errorf("the door %q is #1451's one sentence and must live in exactly one file, main.go; this sweep found it in %v; a second copy is a copy that will not be corrected when the first one is", door, holders)
	}
}
