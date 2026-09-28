//go:build slow

// The tests of this package that cost more than the per-commit run can pay:
// over five seconds each on the Linux bench, or a deadline, wedge or wall-clock
// bound proved by waiting it out. They are behind the `slow` build tag, so
// go-test-cmd and go-test-internal do not build them, and
// .github/workflows/nightly-slow.yml (and `make test-slow`) runs them whole,
// every night. Each carries the measurement that moved it. Nothing here is
// skipped or weakened.

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/onboarding"
	"github.com/mas-bandwidth/nova-tools/internal/update"
)

// SLOW: 9.1 s on hetzner at dev 64b9bec48, over the five-second line.
// TestHelpExampleLinesRunAsPrinted: every line of this tool's `example:` block runs, as printed,
// from the root of a checkout, and exits 0. nova-tools #1455 measured 28 of 61 pasted example lines
// exiting 2 because the line names an input the reader has not made. An example exiting 2 is a broken
// example (ONBOARDING point 1). SCOPE, said out loud: this covers nova-version's own `example:` block
// and the `## nova-version` section of docs/CLI.md -- the two sources this sweep changes -- and
// nothing else. The block is read from the bytes the tool prints and from main.go's documented copy,
// so neither can drift; the document's fenced blocks are read from docs/CLI.md and run in order, so a
// line that stops running as printed goes red here naming that line.
func TestHelpExampleLinesRunAsPrinted(t *testing.T) {
	t.Parallel()

	root := filepath.Join("..", "..")
	binDir := buildBinary(t, root, "nova-version")

	var banner bytes.Buffer
	update.Main("nova-version", []string{"help"}, "", &banner, &banner)
	printed, err := onboarding.ExampleLines(banner.String(), "nova-version")
	if err != nil {
		t.Fatalf("nova-version help: %v\n%s", err, banner.String())
	}
	if documented := mainDocumentedExamples(t); !reflect.DeepEqual(documented, printed) {
		t.Errorf("main.go documents the `example:` block as %q, but `nova-version help` prints %q; a documented block that drifts from the printed one is how a stranger's paste breaks", documented, printed)
	}
	work := checkout(t, root)
	for _, line := range printed {
		runExample(t, work, binDir, line)
	}

	doc, err := os.ReadFile(filepath.Join(root, "docs", "CLI.md"))
	if err != nil {
		t.Fatalf("docs/CLI.md: %v", err)
	}
	for _, heading := range []string{"First run", "Capture and compare installed binaries"} {
		block, err := onboarding.Transcript(string(doc), "nova-version", heading)
		if err != nil {
			t.Fatalf("docs/CLI.md `### %s`: %v", heading, err)
		}
		work := checkout(t, root)
		for _, line := range block {
			if strings.TrimSpace(line) == "" {
				continue
			}
			runExample(t, work, binDir, line)
		}
	}
}
