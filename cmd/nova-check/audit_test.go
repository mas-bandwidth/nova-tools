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
	Escapers: []string{"hintFor"},
	// One entry per site, keyed by file, function and source text; two sites with the same
	// text in the same function share an entry. Each is a claim a reader can check.
	Exempt: map[string]string{
		"main.go|parseFlags|fs.Name()":      "the verb's own name, chosen by this file at every flag.NewFlagSet; two sites",
		"main.go|requireFlags|fs.Name()":    "the verb's own name, chosen by this file at every flag.NewFlagSet",
		"main.go|requireFlags|name":         "a required flag's name, a key of the map this file's callers build from literals",
		"main.go|cmdAttest|att.SHA256":      "sixty-four hex digits from encoding/hex over a SHA-256 sum",
		"main.go|cmdNoCode|source":          "one of the three provenance constants in package check (DenyFloor, DenyReplaced, DenyExtended), as effectiveDenyList returns it; two sites",
		"main.go|cmdNoCode|check.DenyFloor": "a constant in package check",
	},
	Imports: []string{
		`"flag"`, `"fmt"`, `"io"`, `"os"`, `"sort"`, `"strings"`,
		`"github.com/mas-bandwidth/nova-tools/internal/check"`,
	},
	MinClassified: 30,
}
