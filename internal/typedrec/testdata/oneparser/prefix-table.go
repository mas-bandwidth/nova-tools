package oneparser

import "strings"

func parseTable(l string) bool {
	for _, p := range []string{"RED:", "RED "} {
		if strings.HasPrefix(l, p) {
			return true
		}
	}
	return false
}
