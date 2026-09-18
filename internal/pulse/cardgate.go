package pulse

// THE CARD GATE: `AFTER: PR<n> merged`.
//
// SPEC-PULSE.md's rule 4 of "Rate and convergence" says a gated card launches ITSELF: it
// stays gated while the forge reports PR<n> open, and the first tick after the forge reports
// it merged is the tick that launches it -- never a person noticing. Replay 43 is
// `gated-card-launches-on-merge`.
//
// Until 2026-09-18 the line was WRITTEN by manager.cutCard and COUNTED by queueState, and
// nothing anywhere read it at the moment a card was placed. `fill` took the ready directory
// in filename order and launched what it found, so a card carrying `AFTER: PR9999 merged`
// went out on a bench while PR 9999 did not exist (the manager dogfood, edge 1). A gate
// nobody enforces is worse than no gate: the card it was meant to hold is the one whose
// dependency has not landed.
//
// The forge is a SEAM -- PRSource, the same one `sweep` reads pull requests through -- so a
// test drives the whole rule against a fake and no test of this package reaches GitHub.

import (
	"os"
	"strconv"
	"strings"
)

// cardGateOf is the pull request a card waits on, or 0 when it waits on none. The line is
// manager.cutCard's own: `AFTER: PR<n> merged`, matched by gateLine.
func cardGateOf(text string) int {
	m := gateLine.FindStringSubmatch(text)
	if m == nil {
		return 0
	}
	n, err := strconv.Atoi(m[1])
	if err != nil || n <= 0 {
		return 0
	}
	return n
}

// releaseCardGate is the card without its gate line: what is written back the moment the
// forge says the PR is merged, so nothing asks again and the card reads as any other.
func releaseCardGate(text string) string {
	var out []string
	for _, line := range strings.Split(text, "\n") {
		if gateLine.MatchString(strings.TrimSpace(line)) && strings.HasPrefix(strings.TrimSpace(line), "AFTER:") {
			continue
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

// readCard is a card's whole text, or "" when it cannot be read.
func readCard(path string) string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(raw)
}

// gateState asks the forge what a gated card is waiting for. It answers whether the gate is
// open (the PR is MERGED) and the one word that says why it is not -- the PR's state, or
// `unread` when the forge could not be asked at all. An unreadable forge is never an open
// gate: a card whose dependency cannot be checked waits, which is what it was gated for.
func gateState(src PRSource, repo string, pr int) (bool, string) {
	if src == nil {
		return false, "no-forge"
	}
	view, err := src.View(repo, pr)
	if err != nil {
		return false, "unread"
	}
	state := strings.TrimSpace(view.State)
	if state == "" {
		return false, "unread"
	}
	return strings.EqualFold(state, "MERGED"), strings.ToUpper(state)
}
