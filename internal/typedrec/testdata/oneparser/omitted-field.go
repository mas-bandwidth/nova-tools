package oneparser

import "strings"

func parseOmitted(l string) bool {
	return strings.HasPrefix(l, "FINDINGS:")
}
