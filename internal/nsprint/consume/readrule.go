package consume

import (
	"strconv"
	"strings"
)

// RequiredReads computes the required reader count N for a card under policy:
// N is readers_security when any path in the card's paths starts with a prefix
// in security_paths (split on space or comma); otherwise N is readers.
// Each value must be an integer >= 1; absent or invalid defaults to 1 for readers
// and 2 for readers_security.
func RequiredReads(policy map[string]string, card map[string]string) int {
	readers := 1
	if n, err := strconv.Atoi(policy["readers"]); err == nil && n >= 1 {
		readers = n
	}
	security := 2
	if n, err := strconv.Atoi(policy["readers_security"]); err == nil && n >= 1 {
		security = n
	}
	secPrefixes := strings.FieldsFunc(policy["security_paths"], func(r rune) bool {
		return r == ' ' || r == ','
	})
	for _, p := range strings.Fields(card["paths"]) {
		for _, prefix := range secPrefixes {
			if strings.HasPrefix(p, prefix) {
				return security
			}
		}
	}
	return readers
}
