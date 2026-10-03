package main

import (
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/oneline/audit"
)

// The source-level tripwire behind the one-line guarantee: every argument this binary
// prints is quoted, numeric, literal, escaped through internal/oneline, or exempted here
// with its reason. See package audit for what the two walks see and what they cannot.
func TestEveryPrintedArgumentIsLiteralQuotedOrEscaped(t *testing.T) {
	t.Parallel()

	audit.PrintedArguments(t, checkAudit)
}

func TestNoOtherWriterOrShadowCanBypassTheEscape(t *testing.T) {
	t.Parallel()

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
	Escapers: []string{"hintFor", "buildinfo.Line"},
	// One entry per site, keyed by file, function and source text; two sites with the same
	// text in the same function share an entry. Each is a claim a reader can check.
	Exempt: map[string]string{
		"json.go|Write|p":              "the unchanged text renderer forwards bytes already escaped at every print site audited in this package; JSON never forwards them",
		"main.go|requireFlags|name":    "a required flag's name, a key of the map this file's callers build from literals",
		"main.go|cmdAttest|att.SHA256": "sixty-four hex digits from encoding/hex over a SHA-256 sum",
		// The staged mode's one write that is not display text: an object id
		// fed to `git cat-file --batch`'s STDIN pipe, a lookup key that must
		// reach git verbatim. Nothing the pipe carries is printed; what this
		// package prints is the reply's classification, through the escaped
		// FAIL lines. TestNoCodeStagedSaysNo and TestNoCodeStagedClassifiesTheIndex
		// are the behavioural tests for the reader it feeds.
		"staged.go|stagedBlobHeads|oid": "a hex object id from git's own diff-index output, written to the batch reader's stdin pipe rather than to any output stream; it is a lookup key that must reach git verbatim, and the reply's classification -- not this -- is what gets printed, escaped, in the FAIL lines",
	},
	Imports: []string{
		// the verb-help seam (the CLI style's rule (b), #4505): on -h it prints only flag names,
		// their usage literals and lines of this package's own usage const, to the stdout run
		// hands it; it never prints an argument, so nothing it writes can carry a newline in.
		`"github.com/mas-bandwidth/nova-tools/internal/nsprint/verbflag"`,
		// version.go, and the reason it cannot write past the escape: buildinfo reads
		// debug.ReadBuildInfo and runtime's GOOS, GOARCH and Version, holds no writer of
		// its own, and returns a STRING that this package prints -- rendered field by
		// field through oneline.Field before it is returned.
		`"github.com/mas-bandwidth/nova-tools/internal/buildinfo"`,
		`"flag"`, `"fmt"`, `"io"`, `"os"`, `"sort"`, `"strings"`,
		// staged.go's git children run under a context's deadline, so a wait has an
		// end; context itself holds no writer.
		`"context"`,
		// bounded prints the capped FAIL listings and the one MORE line that stands for
		// what they did not print. Every line reaching it is rendered by a fmt.Sprintf in
		// THIS package, which the classifier walks like any other print site, and bounded
		// puts its own two fields -- the kind and the remedy -- through oneline before
		// writing them. It writes to the stream the caller hands it and to nothing else.
		`"github.com/mas-bandwidth/nova-tools/internal/bounded"`,
		`"github.com/mas-bandwidth/nova-tools/internal/check"`,
		// The shared envelope escapes line fields and JSON strings before writing.
		`"github.com/mas-bandwidth/nova-tools/internal/tool"`,
		// gitrun starts one bounded git child and hands back its stdout and stderr as bytes
		// to this package (stagedGit, the cat-file batch); it prints to no stream, and what
		// comes back is read, never printed, except through the escaped error line.
		`"github.com/mas-bandwidth/nova-tools/internal/gitrun"`,
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
	},
	MinClassified: 20,
}
