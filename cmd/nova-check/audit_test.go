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
	//
	// oneline.Quote is oneline's third rendering and the sixth escaper: a double-quoted
	// Go string literal, which escapes every control character, every unprintable rune
	// (U+2028, U+2029 and the bidi controls among them) and the quote and the backslash
	// itself, so it is one line whatever the value holds. It renders the four values of
	// the `hygiene` MORE line's remedy, which is a command a PERSON pastes back into a
	// shell (#1804): Field would spell an identity `Emma\x20<emma@example.com>`, which
	// is one token for a scanner and a line nobody can run. TestHygieneMoreCommandRuns-
	// AsPrinted is the behavioural test for those sites -- it splits the printed remedy
	// the way a shell would and runs it.
	Escapers: []string{"hintFor", "buildinfo.Line", "oneline.Quote"},
	// One entry per site, keyed by file, function and source text; two sites with the same
	// text in the same function share an entry. Each is a claim a reader can check.
	Exempt: map[string]string{
		"main.go|requireFlags|name":    "a required flag's name, a key of the map this file's callers build from literals",
		"main.go|cmdAttest|att.SHA256": "sixty-four hex digits from encoding/hex over a SHA-256 sum",
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
		// hygiene.go, and why internal/hygiene cannot write past the escape: it holds no
		// writer of its own. It runs git as a subprocess, parses what came back and returns
		// Findings -- three STRING fields this package renders through oneline.Field and
		// oneline.Escape at the one print site that carries them. Its errors are returned,
		// never printed, and reach the stream only through refuse, which escapes them.
		// The matched text of a secret finding is not in any field it returns (its own
		// TestHygieneRejectsAKeyShapeAndNeverPrintsIt searches every field for it).
		`"github.com/mas-bandwidth/nova-tools/internal/hygiene"`,
	},
	MinClassified: 30,
}
