package oneparser

import "strings"

func parseSplit(l string) bool {
	p := strings.SplitN(l, ":", 2)
	if p[0] == "PROBES" {
		return true
	}
	return false
}
