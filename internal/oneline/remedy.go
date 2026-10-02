package oneline

import (
	"regexp"
	"strings"
)

// RemedyMarkers are the house forms of a remedy on a refusal line
// (docs/CLI-STYLE.md (f)): the command to run, the flag or value the input
// wants, where to read, or what to do next. A line holding any of them has left
// its reader a breadcrumb. No tool or verb fails silently, and every failure
// carries a breadcrumb that shows how to fix what went wrong. internal/ci's
// remedy rule reads the source for the same forms, so the two agree on what
// a remedy is.
var RemedyMarkers = []string{
	"run:", "run `", "remedy:", "remedy=", "fix:", "wants ", "see `",
	"rerun", "next:", " -h", "--help",
	"run nova-", "the repair is", "requires --", "needs --", "or the other", "drop one", "run this again", "run '",
}

// remedyPointerRe is the pointer to a tool's help ("see nova-x help verb", "nova-x help
// verb"): a tool name, then help. The bare words "see", "want", "retry" and "help" are
// prose ("see above", "no help available", "retry later") and are not a remedy on their own.
var remedyPointerRe = regexp.MustCompile(`\bnova-[a-z][a-z-]*( [a-z][a-z-]*)? help\b`)

// remedyImperativeRe is the other house form: an imperative that names the
// flag, the file or the value to use ("pass --overwrite to replace it", "name a
// --draft-dir", "drop --open", "give 1 to 64").
var remedyImperativeRe = regexp.MustCompile("(?i)\\b(pass|give|name|drop|use|set|add|raise|lower|install|start|create|edit|remove|delete|fix|choose|pick)\\s+((a|an|the|its|one|another)\\s+)?(--|-[a-z]|`|[0-9]|it\\b)")

// remedyFlagRe is the other words that name a next step only when a flag or a command
// in backticks follows them ("see --help", "retry with --force", "try `x -h`").
var remedyFlagRe = regexp.MustCompile("(?i)\\b(see|retry|try|want|wants)\\s+(with\\s+|using\\s+)?(--[a-z]|-[a-z]\\b|`)")

// HasRemedy reports whether a refusal's text carries a remedy in one of the
// house forms.
func HasRemedy(s string) bool {
	for _, m := range RemedyMarkers {
		if strings.Contains(s, m) {
			return true
		}
	}
	return remedyPointerRe.MatchString(s) || remedyImperativeRe.MatchString(s) || remedyFlagRe.MatchString(s)
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
