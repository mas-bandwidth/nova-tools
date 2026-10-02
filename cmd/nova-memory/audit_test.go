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
	// commandLine is the fourth: it puts every argument of an echoed quickstart step
	// through oneline.Escape and then that platform's shell quoting, and joins them with
	// single spaces, so the echo is one line whatever an argument holds. The
	// classifier walks each body like the others, so every claim here is checked.
	// buildinfo.Line is the `version` verb's whole line and the fifth escaper: it renders
	// every one of its four fields through oneline.Field inside internal/buildinfo, where
	// TestLineShape and TestLineHoldsWhateverTheStampContains pin it -- including against
	// a release stamp holding a newline, which is the one field of that line that comes
	// from outside the toolchain.
	Escapers: []string{"oneline.Quote", "hitLine", "scoreFields", "chanNames", "commandLine", "buildinfo.Line"},
	// One entry per site, keyed by file, function and source text; sites with the same
	// text in the same function share an entry. Each is a claim a reader can check.
	Exempt: map[string]string{
		"main.go|stepFailed|verb":                 "the name of the quickstart step, a literal at all three call sites in this file",
		"main.go|scoreFields|chn":                 "the name of the channel that scored the hit, one of the two channel names package memindex defines",
		"main.go|hitLine|token":                   "the event token, a literal at both call sites in this file",
		"main.go|hitLine|prefix":                  "empty, or cand=<n> built by Sprintf from an integer, at the two call sites in this file",
		"main.go|cmdStats|memindex.SchemaVersion": "a constant in package memindex",
		"main.go|cmdStats|buildTime":              "a time.Duration",
		"main.go|cmdVerify|f.Kind":                "one of the four kind literals package memindex assigns (coverage, backlink, wikilink, frontmatter); two sites",
		"main.go|cmdVerify|links":                 "required, and checked before the verb runs to be exactly gate or info",
		"main.go|loadPin|why":                     "one of the seven literal reasons the switches above it assign",
	},
	Imports: []string{
		// the skeleton: dispatch, help and refusals, each value through internal/oneline,
		// and the shared renderer that marshals a --json result as one escaped JSON record
		`"github.com/mas-bandwidth/nova-tools/internal/tool"`,
		// version.go, and the reason it cannot write past the escape: buildinfo reads
		// debug.ReadBuildInfo and runtime's GOOS, GOARCH and Version, holds no writer of
		// its own, and returns a STRING that this package prints -- rendered field by
		// field through oneline.Field before it is returned.
		`"github.com/mas-bandwidth/nova-tools/internal/buildinfo"`,
		// runtime is read for GOOS alone, in commandLine: which shell the echoed
		// quickstart line has to paste into is a property of the machine printing it.
		// It writes to no stream.
		`"bufio"`, `"fmt"`, `"io"`, `"math"`, `"os"`, `"path"`, `"runtime"`, `"sort"`, `"strings"`, `"time"`,
		// maps and slices hold no writer: they return keys, sorted copies and membership,
		// which this package renders through oneline at its own print sites.
		`"maps"`, `"slices"`,
		// bytes holds a quickstart step's JSON in memory; encoding/json wraps that step's
		// object, already rendered by tool.Out, as one value of the quickstart's own;
		// strconv renders the demonstration paragraph's line number. None writes.
		`"bytes"`, `"encoding/json"`, `"strconv"`,
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
