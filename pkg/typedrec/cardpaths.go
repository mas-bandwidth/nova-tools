package typedrec

import "strings"

// CardPaths is the scope named by a card's PATHS and NEW fields, in that order.
// The reader supplies values from the card's strict typed header; an absent field,
// an empty comma-separated entry or none contributes no path.
func CardPaths(read func(string) (string, bool)) []string {
	var out []string
	for _, key := range []string{"PATHS", "NEW"} {
		value, _ := read(key)
		for _, g := range strings.Split(value, ",") {
			if g = strings.TrimSpace(g); g != "" && g != "none" {
				out = append(out, g)
			}
		}
	}
	return out
}
