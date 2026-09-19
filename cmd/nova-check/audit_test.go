package main

import (
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/oneline/audit"
)

// The source-level tripwire behind the one-line guarantee: every argument this binary
// prints is quoted, numeric, literal, escaped through internal/oneline, or exempted here
// with its reason. See package audit for what the two walks see and what they cannot.
func TestEveryPrintedArgumentIsLiteralQuotedOrEscaped(t *testing.T) {
	audit.PrintedArguments(t, checkAudit)
}

func TestNoOtherWriterOrShadowCanBypassTheEscape(t *testing.T) {
	audit.Bypasses(t, checkAudit)
}

var checkAudit = audit.Config{
	// hintFor returns one of this package's own hint constants, or the empty string, and
	// nothing else -- a switch over a flag name, with no caller text in it. The classifier
	// walks its body like any other listed escaper, so the claim is checked rather than
	// taken.
	// buildinfo.Line is the `version` verb's whole line and the fifth escaper: it renders
	// every one of its four fields through oneline.Field inside internal/buildinfo, where
	// TestLineShape and TestLineHoldsWhateverTheStampContains pin it -- including against
	// a release stamp holding a newline, which is the one field of that line that comes
	// from outside the toolchain.
	// convergenceHint is the same shape as hintFor, for the one verb whose
	// --ledger is a different document from the corpus verb's: a switch over a
	// flag name returning one of this package's own constants, and the
	// classifier walks its body like any other listed escaper.
	Escapers: []string{"hintFor", "convergenceHint", "buildinfo.Line"},
	// One entry per site, keyed by file, function and source text; two sites with the same
	// text in the same function share an entry. Each is a claim a reader can check.
	Exempt: map[string]string{
		"main.go|checkFailMax|fs.Name()": "the verb's own name, chosen by this file at every flag.NewFlagSet",
		"main.go|requireFlags|fs.Name()": "the verb's own name, chosen by this file at every flag.NewFlagSet",
		"main.go|requireFlags|name":      "a required flag's name, a key of the map this file's callers build from literals",
		"main.go|cmdAttest|att.SHA256":   "sixty-four hex digits from encoding/hex over a SHA-256 sum",
		// The convergence verb builds its lines in internal/converge, where every
		// field of every line goes through oneline.Field before it is joined --
		// a stream name, a trend word, a pull request's rounds, a build stamp, a
		// --by name. The claim is behavioral rather than structural, so it has a
		// test of its own: TestEveryFieldSurvivesAHostileValue in this package
		// runs a title, a ledger cell, a stamp and a --by name each holding a
		// newline, an `=` and a bidi override through the whole verb and asserts
		// one line per stream.
		"convergence.go|printLines|line": "one line from internal/converge, every field of it rendered through internal/oneline; pinned by TestEveryFieldSurvivesAHostileValue",
		"convergence.go|printJSON|raw":   "the object encoding/json built, whose encoder escapes every control character as \\u, so the whole object is one line whatever a title or a stamp holds",
	},
	Imports: []string{
		// version.go, and the reason it cannot write past the escape: buildinfo reads
		// debug.ReadBuildInfo and runtime's GOOS, GOARCH and Version, holds no writer of
		// its own, and returns a STRING that this package prints -- rendered field by
		// field through oneline.Field before it is returned.
		`"github.com/mas-bandwidth/nova-tools/internal/buildinfo"`,
		`"flag"`, `"fmt"`, `"io"`, `"os"`, `"sort"`, `"strings"`,
		// bounded prints the capped FAIL listings and the one MORE line that stands for
		// what they did not print. Every line reaching it is rendered by a fmt.Sprintf in
		// THIS package, which the classifier walks like any other print site, and bounded
		// puts its own two fields -- the kind and the remedy -- through oneline before
		// writing them. It writes to the stream the caller hands it and to nothing else.
		`"github.com/mas-bandwidth/nova-tools/internal/bounded"`,
		`"github.com/mas-bandwidth/nova-tools/internal/check"`,
		// dogfood.go's three, and why none of them can write past the escape:
		// internal/dogfood holds no writer at all -- it reads a command
		// reference and a directory of receipts, returns values, and renders
		// its line grammar (Row.Line, Summary.Line, Receipt.RecordLine) into
		// STRINGS this package prints through oneline.Escape. Its one reach
		// outside the process is a git subprocess whose output it parses and
		// never prints. context and time supply that subprocess's deadline and
		// the receipt's RFC3339 stamp; neither holds a stream.
		`"context"`,
		`"time"`,
		`"github.com/mas-bandwidth/nova-tools/internal/dogfood"`,
		// convergence.go's two. internal/converge holds no writer at all: it
		// reads a forge, a checkout, a directory and three documents through
		// seams, and returns VALUES -- a report whose every line it renders
		// through internal/oneline. encoding/json is the --json shape, and its
		// encoder escapes rather than prints: it returns bytes this file writes.
		`"encoding/json"`,
		`"github.com/mas-bandwidth/nova-tools/internal/converge"`,
	},
	MinClassified: 30,
}
