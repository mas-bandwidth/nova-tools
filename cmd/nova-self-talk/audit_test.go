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
	// buildinfo.Line is the `version` verb's whole line and the fifth escaper: it renders
	// every one of its four fields through oneline.Field inside internal/buildinfo, where
	// TestLineShape and TestLineHoldsWhateverTheStampContains pin it -- including against
	// a release stamp holding a newline, which is the one field of that line that comes
	// from outside the toolchain.
	Escapers: []string{"hintFor", "buildinfo.Line"},
	// One entry per site, keyed by file, function and source text; sites with the same
	// text in the same function share an entry. Each is a claim a reader can check.
	Exempt: map[string]string{
		"main.go|run|c.Verdict":                   "a selftalk.Verdict, one of the two constants package selftalk declares; two sites",
		"main.go|run|selftalk.RuleDocumentBanner": "a constant in package selftalk",
		"main.go|run|i.Shape":                     "one of the four Shape constants package selftalk declares",
	},
	Imports: []string{
		// version.go, and the reason it cannot write past the escape: buildinfo reads
		// debug.ReadBuildInfo and runtime's GOOS, GOARCH and Version, holds no writer of
		// its own, and returns a STRING that this package prints -- rendered field by
		// field through oneline.Field before it is returned.
		`"github.com/mas-bandwidth/nova-tools/internal/buildinfo"`,
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
