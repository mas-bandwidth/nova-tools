package main

import (
	"strings"
	"testing"
)

// Property P8 from Ten out of Ten Ledger (line 134):
// "Concrete starting values (bm25, k 3-5) sit in refusals, not the root synopsis.
// BRIEF: the root synopsis line shows --channels bm25 --k 3 and the check value (2-3),
// held by the banner test."
//
// The root help output must not hide concrete practical values behind abstract
// placeholders like `--channels <list> --k <n>` that require triggering refusals
// to discover. The root synopsis line for search features `--channels bm25 --k 3`,
// and the check synopsis / guidance provides concrete check values (2-3).

func TestBannerFeaturesConcreteSynopsisValues(t *testing.T) {
	t.Parallel()

	for _, flag := range []string{"help", "-h", "--help"} {
		exit, stdout, stderr := runCLI(t, "", flag)
		if exit != 0 {
			t.Fatalf("`nova-memory %s` exit = %d, want 0; stderr: %s", flag, exit, stderr)
		}
		if stderr != "" {
			t.Errorf("`nova-memory %s` stderr not empty: %s", flag, stderr)
		}

		// 1. Assert that the root help output explicitly contains `--channels bm25 --k 3`.
		const wantSearchSynopsis = "--channels bm25 --k 3"
		if !strings.Contains(stdout, wantSearchSynopsis) {
			t.Errorf("`nova-memory %s` output does not contain %q:\n%s", flag, wantSearchSynopsis, stdout)
		}

		// 2. Assert that check threshold / value guidance (2-3) is present in the banner.
		if !strings.Contains(stdout, "2-3") {
			t.Errorf("`nova-memory %s` output does not contain check value guidance (2-3):\n%s", flag, stdout)
		}
		if !strings.Contains(stdout, "check values: 2-3") && !strings.Contains(stdout, "--k 2-3") {
			t.Errorf("`nova-memory %s` output missing explicit check guidance (2-3):\n%s", flag, stdout)
		}
	}
}

// TestUsageBlockCarriesConcreteSynopsisLines isolates the synopsis lines under `usage:`
// and ensures the search and check verbs feature concrete recommended values rather
// than opaque placeholders.
func TestUsageBlockCarriesConcreteSynopsisLines(t *testing.T) {
	t.Parallel()

	exit, stdout, stderr := runCLI(t, "", "help")
	if exit != 0 {
		t.Fatalf("`nova-memory help` exit = %d; stderr: %s", exit, stderr)
	}

	lines := strings.Split(stdout, "\n")
	inUsage := false
	var searchLine, checkLine string
	for _, l := range lines {
		trim := strings.TrimSpace(l)
		if trim == "usage:" {
			inUsage = true
			continue
		}
		if inUsage && trim == "" {
			inUsage = false
			break
		}
		if inUsage {
			if strings.HasPrefix(trim, "nova-memory search ") {
				searchLine = trim
			} else if strings.HasPrefix(trim, "nova-memory check ") {
				checkLine = trim
			}
		}
	}

	if searchLine == "" {
		t.Fatalf("no `nova-memory search` synopsis line found under `usage:`:\n%s", stdout)
	}
	if !strings.Contains(searchLine, "--channels bm25 --k 3") {
		t.Errorf("search synopsis line does not show concrete values `--channels bm25 --k 3`: %q", searchLine)
	}

	if checkLine == "" {
		t.Fatalf("no `nova-memory check` synopsis line found under `usage:`:\n%s", stdout)
	}
	if !strings.Contains(checkLine, "--channels bm25") || !strings.Contains(checkLine, "--k 2-3") {
		t.Errorf("check synopsis line does not show concrete values `--channels bm25 --k 2-3`: %q", checkLine)
	}
}

// TestVerbHelpQuotesConcreteSynopsis asserts that `<verb> -h` inherits the concrete
// synopsis line from the banner for both search and check.
func TestVerbHelpQuotesConcreteSynopsis(t *testing.T) {
	t.Parallel()

	// search -h quotes `nova-memory search ... --channels bm25 --k 3`
	exit, stdout, stderr := runCLI(t, "", "search", "-h")
	if exit != 0 {
		t.Fatalf("`nova-memory search -h` exit = %d; stderr: %s", exit, stderr)
	}
	if !strings.Contains(stdout, "--channels bm25 --k 3") {
		t.Errorf("`nova-memory search -h` does not quote `--channels bm25 --k 3`:\n%s", stdout)
	}

	// check -h quotes `nova-memory check ... --channels bm25 --k 2-3`
	exit, stdout, stderr = runCLI(t, "", "check", "-h")
	if exit != 0 {
		t.Fatalf("`nova-memory check -h` exit = %d; stderr: %s", exit, stderr)
	}
	if !strings.Contains(stdout, "--channels bm25") || !strings.Contains(stdout, "--k 2-3") {
		t.Errorf("`nova-memory check -h` does not quote concrete check values:\n%s", stdout)
	}
}
