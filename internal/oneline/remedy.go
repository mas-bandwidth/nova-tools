package oneline

import (
	"regexp"
	"strings"
)

// RemedyMarkers are the house forms of a remedy on a refusal line
// (docs/CLI-STYLE.md (f)): the command to run, the flag or value the input
// wants, where to read, or what to do next. A line holding any of them has left
// its reader a breadcrumb. The owner's rule (2026-09-30): every tool and verb
// "should never fail silently, and they should always provide helpful
// breadcrumbs how to fix anything going wrong". internal/ci's remedy rule reads
// the source for the same forms, so the two agree on what a remedy is.
var RemedyMarkers = []string{
	"run:", "run `", "remedy:", "remedy=", "fix:", "wants ", "want ", "see `", "see ",
	"rerun", "retry", "next:", " -h", "--help", " help",
	"run nova-", "the repair is", "requires --", "needs --", "or the other", "drop one", "run this again", "run '",
}

// remedyImperativeRe is the other house form: an imperative that names the
// flag, the file or the value to use ("pass --overwrite to replace it", "name a
// --draft-dir", "drop --open", "give 1 to 64").
var remedyImperativeRe = regexp.MustCompile("(?i)\\b(pass|give|name|drop|use|set|add|raise|lower|install|start|create|edit|remove|delete|fix|choose|pick)\\s+((a|an|the|its|one|another)\\s+)?(--|-[a-z]|`|[0-9]|it\\b)")

// HasRemedy reports whether a refusal's text carries a remedy in one of the
// house forms.
func HasRemedy(s string) bool {
	for _, m := range RemedyMarkers {
		if strings.Contains(s, m) {
			return true
		}
	}
	return remedyImperativeRe.MatchString(s)
}

// WithRemedy is what rendered through Escape, ended with "; run: <next>" when
// it carries no remedy of its own: a refusal printer calls it so that no line it
// prints leaves the reader without a next step, and a line that already names
// one is left as it is. It escapes, so it is an escaper in its own right (the
// audit counts it as one); Escape changes nothing it already rendered, so
// WithRemedy(Err(err), next) escapes once.
func WithRemedy(what, next string) string {
	if HasRemedy(what) {
		return Escape(what)
	}
	return Escape(what + "; run: " + next)
}
