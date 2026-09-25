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
	Escapers: []string{"hintFor", "convergenceHint", "buildinfo.Line", "oneline.Quote"},
	// One entry per site, keyed by file, function and source text; two sites with the same
	// text in the same function share an entry. Each is a claim a reader can check.
	Exempt: map[string]string{
		"main.go|requireFlags|name":    "a required flag's name, a key of the map this file's callers build from literals",
		"main.go|cmdAttest|att.SHA256": "sixty-four hex digits from encoding/hex over a SHA-256 sum",
		// The convergence verb builds its lines in internal/converge, where every
		// field of every line goes through oneline.Field before it is joined --
		// a stream name, a trend word, a pull request's rounds, a build stamp, a
		// --by name. The claim is behavioral rather than structural, so it has a
		// test of its own: TestEveryFieldSurvivesAHostileValue in the converge
		// package runs a title, a ledger cell, a stamp and a --by name each
		// holding a newline, an `=` and a bidi override through the whole verb
		// and asserts one line per stream.
		"convergence.go|printLines|line": "one line from internal/converge, every field of it rendered through internal/oneline; pinned by TestEveryFieldSurvivesAHostileValue",
		"convergence.go|printJSON|raw":   "the object encoding/json built, whose encoder escapes every control character as \\u, so the whole object is one line whatever a title or a stamp holds",
		`hygiene.go|cmdHygiene|strings.Join(hygiene.Kinds(), ", ")`: "the card kinds this toolchain declares, read from internal/hygiene/kinds.txt, " +
			"which is embedded into this binary at build time and holds nothing a caller can write. " +
			"TestHygieneRefusesAKindTheToolDoesNotDeclare and TestHygieneAcceptsEveryDeclaredKind are the behavioural tests for this site.",
		// The staged mode's one write that is not display text: an object id
		// fed to `git cat-file --batch`'s STDIN pipe, a lookup key that must
		// reach git verbatim. Nothing the pipe carries is printed; what this
		// package prints is the reply's classification, through the escaped
		// FAIL lines. TestNoCodeStagedSaysNo and TestNoCodeStagedClassifiesTheIndex
		// are the behavioural tests for the reader it feeds.
		"staged.go|stagedBlobHeads|oid": "a hex object id from git's own diff-index output, written to the batch reader's stdin pipe rather than to any output stream; it is a lookup key that must reach git verbatim, and the reply's classification -- not this -- is what gets printed, escaped, in the FAIL lines",
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
		// staged.go's six, and why none of them can write past the escape.
		// bufio READS the batch reader's framed stream (NewReader, ReadString,
		// ReadByte) from a pipe this package opened; bytes holds the stdout
		// and stderr buffers the git subprocesses write into, which are read
		// here and reach a stream only through refuse or the FAIL lines, both
		// escaped; errors builds one-line git failure text; os/exec runs the
		// git plumbing (rev-parse, diff-index, cat-file --batch) whose output
		// is parsed, never printed raw; path/filepath resolves and splits
		// paths for the root test and the path reasons; strconv parses the
		// batch reply's size and quotes a status letter. The one write among
		// them is the Fprintf that feeds an object id to the batch's stdin,
		// and that site is exempted by name below.
		`"bufio"`, `"bytes"`, `"errors"`, `"os/exec"`, `"path/filepath"`, `"strconv"`,
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
