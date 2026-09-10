package main

import (
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/oneline/audit"
)

// The source-level tripwire behind the one-line guarantee: every argument this binary
// prints is quoted, numeric, literal, escaped through internal/oneline, or exempted here
// with its reason. See package audit for what the two walks see and what they cannot.
func TestEveryPrintedArgumentIsLiteralQuotedOrEscaped(t *testing.T) {
	audit.PrintedArguments(t, selfTalkAudit)
}

func TestNoOtherWriterOrShadowCanBypassTheEscape(t *testing.T) {
	audit.Bypasses(t, selfTalkAudit)
}

var selfTalkAudit = audit.Config{
	// hintFor returns one of this package's own hint constants, or the empty string, and
	// nothing else -- a switch over a kind of refusal, with no caller text in it. The
	// classifier walks its body like any other listed escaper, so the claim is checked
	// rather than taken.
	Escapers: []string{"hintFor"},
	// One entry per site, keyed by file, function and source text; sites with the same
	// text in the same function share an entry. Each is a claim a reader can check.
	Exempt: map[string]string{
		"main.go|run|c.Verdict":                   "a selftalk.Verdict, one of the two constants package selftalk declares; two sites",
		"main.go|run|selftalk.RuleDocumentBanner": "a constant in package selftalk",
		"main.go|run|i.Shape":                     "one of the four Shape constants package selftalk declares",
	},
	Imports: []string{
		`"errors"`, `"flag"`, `"fmt"`, `"io"`, `"os"`, `"strings"`,
		// bounded prints the capped finding listings and the one MORE line that stands
		// for what they did not print. Every line reaching it is rendered by a
		// fmt.Sprintf in THIS package, which the classifier walks like any other print
		// site, and bounded puts its own two fields -- the kind and the remedy -- through
		// oneline before writing them. It writes to the stream the caller hands it and
		// to nothing else.
		`"github.com/mas-bandwidth/nova-tools/internal/bounded"`,
		`"github.com/mas-bandwidth/nova-tools/internal/selftalk"`,
	},
	MinClassified: 10,
}
