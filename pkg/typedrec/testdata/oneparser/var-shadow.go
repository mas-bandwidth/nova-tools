package oneparser

import "strings"

// A package-level var bound to a constant string and never assigned is a
// constant in all but name (cold HOLD on #3497, item 2).
var headPrefix = "HEAD:"

func parseVarShadow(l string) (string, bool) {
	return strings.CutPrefix(l, headPrefix)
}
