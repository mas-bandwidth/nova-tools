package pulse

// manager is SPEC-PULSE's manager tier: the shift a cheap model used to hold by hand, run as a
// verb, with NO model call. A cycle is wait, notes, harvest, triage, merge, refill, one line.
// Quiet time makes no call and sends nothing; an unknown policy key is a refusal, because a
// policy the tool half-understands is a policy nobody approved.

import (
	"os"
	"regexp"
	"strconv"
	"strings"
)

// gateLine matches the one gate a card may carry: AFTER: PR<n> merged.
var gateLine = regexp.MustCompile(`AFTER: PR([0-9]+) merged`)

func splitList(s string) []string {
	var out []string
	for _, f := range strings.Split(s, ",") {
		if t := strings.TrimSpace(f); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// attemptOf is the ATTEMPT: line a requeued card carries; a card out for the first time has none.
func attemptOf(card string) int {
	for _, l := range strings.Split(card, "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(l), "ATTEMPT:"); ok {
			if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
				return n
			}
		}
	}
	return 0
}

// sha12 is a head as a line carries it: twelve characters, enough to tell two heads apart.
func sha12(s string) string { return s[:min(12, len(s))] }

func readLines(path string) []string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var out []string
	for _, l := range strings.Split(string(raw), "\n") {
		if strings.TrimSpace(l) != "" {
			out = append(out, l)
		}
	}
	return out
}
