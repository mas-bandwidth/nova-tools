package main

import (
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/oneline/audit"
)

// The source-level tripwire behind the one-line guarantee: every argument this binary
// prints is quoted, numeric, literal, escaped through internal/oneline, or exempted here
// with its reason. See package audit for what the two walks see and what they cannot.
func TestEveryPrintedArgumentIsLiteralQuotedOrEscaped(t *testing.T) {
	audit.PrintedArguments(t, memoryAudit)
}

func TestNoOtherWriterOrShadowCanBypassTheEscape(t *testing.T) {
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
	Escapers: []string{"hitLine", "scoreFields", "chanNames", "hintFor", "commandLine"},
	// One entry per site, keyed by file, function and source text; sites with the same
	// text in the same function share an entry. Each is a claim a reader can check.
	Exempt: map[string]string{
		"main.go|parse|fs.Name()":                 "the verb's own name, chosen by this file at every flag.NewFlagSet",
		"main.go|parse|name":                      "a required flag's name, a literal at every call site in this file",
		"main.go|build|name":                      "the verb's own name, a literal at every call site in this file; two sites",
		"main.go|channelNames|verb":               "the verb's own name, a literal at every call site in this file; three sites",
		"main.go|stepFailed|verb":                 "the name of the quickstart step, a literal at all three call sites in this file",
		"main.go|checkK|verb":                     "the verb's own name, a literal at every call site in this file",
		"main.go|scoreFields|chn":                 "the name of the channel that scored the hit, one of the two channel names package memindex defines",
		"main.go|hitLine|token":                   "the event token, a literal at both call sites in this file",
		"main.go|hitLine|prefix":                  "empty, or cand=<n> built by Sprintf from an integer, at the two call sites in this file",
		"main.go|cmdStats|memindex.SchemaVersion": "a constant in package memindex",
		"main.go|cmdStats|buildTime":              "a time.Duration",
		"main.go|cmdVerify|f.Kind":                "one of the four kind literals package memindex assigns (coverage, backlink, wikilink, frontmatter); two sites",
		"main.go|cmdVerify|*links":                "validated above the site to be exactly gate or info",
	},
	Imports: []string{
		// runtime is read for GOOS alone, in commandLine: which shell the echoed
		// quickstart line has to paste into is a property of the machine printing it.
		// It writes to no stream.
		`"bufio"`, `"flag"`, `"fmt"`, `"io"`, `"os"`, `"path"`, `"runtime"`, `"sort"`, `"strings"`, `"time"`,
		`"github.com/mas-bandwidth/nova-tools/internal/memindex"`,
	},
	MinClassified: 30,
}
