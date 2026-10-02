package swarm

import (
	"regexp"
	"strings"
)

// PlaceholderCheck is the token of a line of the card template left unfilled.
const PlaceholderCheck = "placeholder"

// placeholderRE is a template's fill-in: <...> on one line.
var placeholderRE = regexp.MustCompile(`<[^<>\n]+>`)

// UnfilledTemplateLines is every line of a card that is a line of the card template
// (Template("card")) still holding its <...> fill-ins, as the template printed it: a card
// cut from the template and handed out with REPO: <owner>/<name> stages nothing and lands
// nowhere (tool ledger W12). A line partly filled in, or a card not cut from the template,
// is not this finding: only the template's own lines are read, so a card that writes
// `--count <n>` in its task is never caught by it. nova-swarm lint names each on a NOTE
// line and nova-sprint add says so on its own.
func UnfilledTemplateLines(card string) []CardHeaderFinding {
	unfilled := map[string]bool{}
	for _, l := range strings.Split(templateCard, "\n") {
		if placeholderRE.MatchString(l) {
			unfilled[strings.TrimSpace(l)] = true
		}
	}
	var out []CardHeaderFinding
	for i, l := range strings.Split(card, "\n") {
		if t := strings.TrimSpace(l); t != "" && unfilled[t] {
			out = append(out, CardHeaderFinding{Check: PlaceholderCheck, Line: i + 1, Excerpt: l})
		}
	}
	return out
}
