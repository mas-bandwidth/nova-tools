package pulse

// The branch names git accepts and the names it refuses, in process. `rowan/has a space`
// was CUT OK on 2026-09-18 because nobody asked the question before ls-remote: the answer
// is fixed, so it is arithmetic, and no subprocess runs to get it.

import "testing"

func TestCheckRefFormatBranchRefusesWhatGitRefuses(t *testing.T) {
	bad := map[string]string{
		"":                  "empty",
		"rowan/has a space": "a space",
		"-dashed":           "a leading dash",
		"@":                 "the single @",
		"rowan/..":          "two dots",
		"rowan//two":        "an empty component",
		"rowan/ends.":       "a trailing dot",
		"rowan/.hidden":     "a component starting with a dot",
		"rowan/x.lock":      "a .lock component",
		"rowan/x@{1}":       "@{",
		"rowan/back\\slash": "a backslash",
		"rowan/star*":       "a glob character",
		"rowan/q?":          "a glob character",
		"rowan/colon:":      "a colon",
		"rowan/tilde~":      "a tilde",
		"rowan/caret^":      "a caret",
		"rowan/br[acket":    "a bracket",
		"rowan/tab\tbed":    "a tab",
		"/leading":          "a leading slash",
		"trailing/":         "a trailing slash",
	}
	for name, why := range bad {
		if got := checkRefFormatBranch(name); got == "" {
			t.Errorf("%q was accepted; git check-ref-format --branch refuses it (%s)", name, why)
		}
	}
	good := []string{"dev", "main", "rowan/cut-fill-edges", "rowan/issue-42-fix-the-widget", "v1.2.3", "a.b/c-d_e"}
	for _, name := range good {
		if why := checkRefFormatBranch(name); why != "" {
			t.Errorf("%q was refused as %q; git check-ref-format --branch accepts it", name, why)
		}
	}
}

// TestCardFileNameIsTheQueuesContract: every cut card is a card-<n>.md, and a label that
// already carries the prefix is not given it twice.
func TestCardFileNameIsTheQueuesContract(t *testing.T) {
	for label, want := range map[string]string{
		"42":        "card-42.md",
		"one":       "card-one.md",
		"card-0912": "card-0912.md",
	} {
		if got := cardFileName(label); got != want {
			t.Errorf("cardFileName(%q) = %q, want %q", label, got, want)
		}
	}
}
