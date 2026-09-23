package docs

import (
	"os"
	"strings"
	"testing"
)

// TestEvalMergeQueueNamesWhatTheQueueCannotDo asserts that
// docs/EVAL-MERGE-QUEUE.md evaluates GitHub's native merge queue
// against the land-loop by naming, for each deciding fact, whether
// the queue can handle it — a yes or a no with a reason — and then
// states, in one verdict, what the land loop does that the queue
// cannot.
func TestEvalMergeQueueNamesWhatTheQueueCannotDo(t *testing.T) {
	t.Parallel()

	body, err := os.ReadFile("../../docs/EVAL-MERGE-QUEUE.md")
	if err != nil {
		t.Fatalf("docs/EVAL-MERGE-QUEUE.md: %v", err)
	}
	content := string(body)

	// --- Verdict must be present. ---
	hasVerdict := strings.Contains(content, "Verdict") ||
		strings.Contains(content, "verdict") ||
		strings.Contains(content, "The land loop does")
	if !hasVerdict {
		t.Error("EVAL-MERGE-QUEUE.md missing a verdict naming what the land loop does the queue cannot")
	}

	// --- Four decisive capabilities, each must carry a yes/no and a reason. ---
	type cap struct {
		label    string // phrase that identifies this capability
		decision string // "yes" or "no"
		reason   string // keyword that must appear near the label
	}
	caps := []cap{
		{
			label:    "non-author friend line at head",
			decision: "no",
			reason:   "friend line",
		},
		{
			label:    "HOLD at head",
			decision: "no",
			reason:   "HOLD",
		},
		{
			label:    "ALLOWED_RED for a named check",
			decision: "no",
			reason:   "ALLOWED_RED",
		},
		{
			label:    "the way the land loop batches",
			decision: "yes",
			reason:   "batch",
		},
	}

	for _, c := range caps {
		idx := strings.Index(content, c.label)
		if idx < 0 {
			t.Errorf("EVAL-MERGE-QUEUE.md missing a %q capability (%s) — label not found",
				c.label, c.decision)
			continue
		}

		window := content[idx:]
		if len(window) > 1000 {
			window = window[:1000]
		}

		// Strip markdown bold/italic markers so we can scan for bare yes/no.
		sanitized := strings.Map(func(r rune) rune {
			if r == '*' || r == '_' {
				return -1
			}
			return r
		}, window)
		lower := strings.ToLower(sanitized)

		// Check: the decision word must appear within the first 800 sanitized
		// characters after the label as a standalone word.
		hasDecision := false
		searchEnd := len(lower)
		if searchEnd > 800 {
			searchEnd = 800
		}
		for i := 0; i < searchEnd; i++ {
			ch := lower[i]
			if ch != 'y' && ch != 'n' {
				continue
			}
			rest := lower[i:]
			// Must start with "yes" or "no" (full word)
			if strings.HasPrefix(rest, "yes ") || strings.HasPrefix(rest, "yes\n") || rest == "yes" {
				hasDecision = true
				break
			}
			if strings.HasPrefix(rest, "no ") || strings.HasPrefix(rest, "no\n") || rest == "no" {
				hasDecision = true
				break
			}
			if strings.HasPrefix(rest, "no|") || strings.HasPrefix(rest, "|no") || strings.HasPrefix(rest, "no\t") {
				hasDecision = true
				break
			}
		}
		if !hasDecision {
			t.Errorf("EVAL-MERGE-QUEUE.md missing %q decision near %q", c.decision, c.label)
			continue
		}

		// Reason must contain the keyword somewhere in the document.
		hasReason := strings.Count(content, c.reason) > 0
		if !hasReason {
			t.Errorf("EVAL-MERGE-QUEUE.md missing reasoning keyword %q near %q", c.reason, c.label)
		}
	}
}
