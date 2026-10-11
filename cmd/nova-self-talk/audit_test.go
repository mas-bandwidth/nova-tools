package main

import (
	"testing"

	"github.com/mas-bandwidth/nova-tools/pkg/oneline/audit"
)

// The source-level tripwire behind the one-line guarantee: every argument this binary
// prints is quoted, numeric, literal, escaped through pkg/oneline, or exempted here
// with its reason. See package audit for what the two walks see and what they cannot.
func TestEveryPrintedArgumentIsLiteralQuotedOrEscaped(t *testing.T) {
	t.Parallel()

	audit.PrintedArguments(t, selfTalkAudit)
}

func TestNoOtherWriterOrShadowCanBypassTheEscape(t *testing.T) {
	t.Parallel()

	audit.Bypasses(t, selfTalkAudit)
}

var selfTalkAudit = audit.Config{
	// hintFor returns one of this package's own hint constants, or the empty string, and
	// nothing else -- a map over a kind of refusal, with no caller text in it. The
	// classifier walks its body like any other listed escaper, so the claim is checked
	// rather than taken.
	// buildinfo.Line is the `version` verb's whole line and the fifth escaper: it renders
	// every one of its four fields through oneline.Field inside pkg/buildinfo, where
	// TestLineShape and TestLineHoldsWhateverTheStampContains pin it -- including against
	// a release stamp holding a newline, which is the one field of that line that comes
	// from outside the toolchain.
	// bounded.MoreLine is the MORE line every capped listing prints, its kind through
	// oneline.Field and its remedy through oneline.Escape inside pkg/bounded.
	Escapers: []string{"hintFor", "buildinfo.Line", "bounded.MoreLine"},
	// One entry per site, keyed by file, function and source text; sites with the same
	// text in the same function share an entry. Each is a claim a reader can check.
	Exempt: map[string]string{
		"scan.go|lines|selftalk.RuleDocumentBanner": "a constant in package selftalk",
	},
	Imports: []string{
		// example.go uses atomicfile only to publish embedded page bytes to a file.
		// It has no stdout or stderr writer; failures return through the escaped refusal.
		`"github.com/mas-bandwidth/nova-tools/pkg/atomicfile"`,
		// the verb-help seam (the CLI style's rule (b), #4505): on -h it prints only flag names,
		// their usage literals and lines of this package's own usage const, to the stdout run
		// hands it; it never prints an argument, so nothing it writes can carry a newline in.
		`"github.com/mas-bandwidth/nova-tools/pkg/nsprint/verbflag"`,
		// version.go, and the reason it cannot write past the escape: buildinfo reads
		// debug.ReadBuildInfo and runtime's GOOS, GOARCH and Version, holds no writer of
		// its own, and returns a STRING that this package prints -- rendered field by
		// field through oneline.Field before it is returned.
		`"github.com/mas-bandwidth/nova-tools/pkg/buildinfo"`,
		`"errors"`, `"flag"`, `"fmt"`, `"io"`, `"io/fs"`, `"os"`, `"slices"`, `"strings"`,
		// cmp.Or picks between two strings a caller already holds; it holds no writer.
		`"cmp"`,
		// unicode/utf8 decides whether a named page is text before the scan; it holds
		// no writer, and the refusal it feeds is rendered through oneline.
		`"unicode/utf8"`,
		// bytes compares a page on disk with the one built in; embed holds the example pages;
		// path and path/filepath join names. None of them writes to a stream.
		`"bytes"`, `"embed"`, `"path"`, `"path/filepath"`,
		// tool renders the --json form: Out.Render(stdout, true) marshals one value with
		// encoding/json, which escapes every control character, so a JSON line is one line
		// whatever a file name or a sentence holds. Its typed-line rendering is not used here.
		`"github.com/mas-bandwidth/nova-tools/pkg/tool"`,
		// bounded prints the capped finding listings and the one MORE line that stands
		// for what they did not print. Every line reaching it is rendered by a
		// fmt.Sprintf in THIS package, which the classifier walks like any other print
		// site, and bounded puts its own two fields -- the kind and the remedy -- through
		// oneline before writing them. It writes to the stream the caller hands it and
		// to nothing else.
		`"github.com/mas-bandwidth/nova-tools/pkg/bounded"`,
		`"github.com/mas-bandwidth/nova-tools/internal/selftalk"`,
	},
	MinClassified: 10,
}
