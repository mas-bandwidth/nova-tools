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

	audit.PrintedArguments(t, memoryAudit)
}

func TestNoOtherWriterOrShadowCanBypassTheEscape(t *testing.T) {
	t.Parallel()

	audit.Bypasses(t, memoryAudit)
}

var memoryAudit = audit.Config{
	// These build text from values classified at their own Sprintf or accepted from
	// a fixed set: hitLine and scoreFields are walked by the same classifier, and chanNames
	// joins names that channelNames accepted from the two channels that exist.
	// hintFor is the fourth: it returns one of the package's own hint constants, or the
	// empty string, and nothing else — a switch over a flag name, with no caller text in
	// it. commandLine is the fifth: it puts every argument of an echoed quickstart step
	// through oneline.Escape and then that platform's shell quoting, and joins them with
	// single spaces, so the echo is one line whatever an argument holds. The
	// classifier walks each body like the others, so every claim here is checked.
	// buildinfo.Line is the `version` verb's whole line and the fifth escaper: it renders
	// every one of its four fields through oneline.Field inside internal/buildinfo, where
	// TestLineShape and TestLineHoldsWhateverTheStampContains pin it -- including against
	// a release stamp holding a newline, which is the one field of that line that comes
	// from outside the toolchain.
	Escapers: []string{"oneline.Quote", "hitLine", "scoreFields", "chanNames", "commandLine"},
	Exempt: map[string]string{
		"main.go|scoreFields|chn":                 "the name of the channel that scored the hit, one of the two channel names package memindex defines",
		"main.go|hitLine|token":                   "the event token, a literal at both call sites in this file",
		"main.go|hitLine|prefix":                  "empty, or cand=<n> built by Sprintf from an integer, at the two call sites in this file",
		"main.go|cmdStats|memindex.SchemaVersion": "a constant in package memindex",
		"main.go|cmdStats|buildTime":              "a time.Duration",
		"main.go|cmdVerify|f.Kind":                "one of the four kind literals package memindex assigns (coverage, backlink, wikilink, frontmatter); two sites",
	},
	Imports: []string{
		`"github.com/mas-bandwidth/nova-tools/internal/tool"`, // shared JSON renderer marshals the result as one escaped JSON record
		`"bufio"`, `"flag"`, `"fmt"`, `"io"`, `"math"`, `"os"`, `"path"`, `"runtime"`, `"sort"`, `"strings"`, `"time"`,
		// io/fs is the type of a directory walk and writes to no stream.
		`"io/fs"`,
		// boot resolves pinned memory paths under --root through the platform
		// path separator (filepath.Join/FromSlash) after validating them with
		// path's slash rules; it writes to no stream.
		`"path/filepath"`,
		// bounded prints the capped listings and the one MORE line that stands for what
		// they did not print. Every line reaching it is rendered by a fmt.Sprintf in THIS
		// package, which the classifier walks like any other print site, and bounded puts
		// its own two fields -- the kind and the remedy -- through oneline before writing
		// them. It writes to the stream the caller hands it and to nothing else.
		`"github.com/mas-bandwidth/nova-tools/internal/bounded"`,
		`"github.com/mas-bandwidth/nova-tools/internal/memindex"`,
	},
	MinClassified: 30,
}
