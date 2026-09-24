package oneparser

import "strings"

const floor = "FLO" + "OR:"

func parseConcat(l string) (string, bool) {
	return strings.CutPrefix(l, floor)
}
