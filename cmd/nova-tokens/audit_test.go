package main

import (
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/oneline/audit"
)

// The source-level tripwire behind the one-line guarantee: every argument this binary
// prints is quoted, numeric, literal, escaped through internal/oneline, or exempted here
// with its reason.
func TestEveryPrintedArgumentIsLiteralQuotedOrEscaped(t *testing.T) {
	t.Parallel()

	audit.PrintedArguments(t, tokensAudit)
}

func TestNoOtherWriterOrShadowCanBypassTheEscape(t *testing.T) {
	t.Parallel()

	audit.Bypasses(t, tokensAudit)
}

var tokensAudit = audit.Config{
	Escapers: []string{"sourceLine", "unreadableLine", "unparsedLine", "dayLine", "aggFields"},
	// One entry per site, keyed by file, function and source text; each is a claim a
	// reader can check.
	Exempt: map[string]string{
		"ledger.go|cmdReportStore|line": "one REPORT line built in the loop above from literal key names, oneline.Field over each key value, a %d row count and strconv.FormatInt or the literal dash per type; nothing in it is unescaped text",
		"main.go|cmdFold|counts":        "the count line, built two lines above by a Sprintf whose every verb is %d over an integer; the classifier walks that Sprintf like any other print site. Two sites: the OK line and the FAIL line",
		"main.go|cmdReport|body":        "the report's stdout IS the artifact: every line of it was rendered by tokens.BodyLine, which puts each of its stored fields through oneline.Field, and the lines are joined with \\n by this function. Escaping the join again would escape those newlines and destroy the note body this verb exists to print",
	},
	Imports: []string{
		// version.go's resolution order, which now lives once in internal/buildinfo
		// rather than in a copy per binary: it reads debug.ReadBuildInfo, holds no
		// writer of its own, and returns a string this package renders through
		// oneline.Field at the print site below.
		`"github.com/mas-bandwidth/nova-tools/internal/buildinfo"`,
		`"flag"`, `"fmt"`, `"io"`, `"os"`, `"path/filepath"`, `"runtime"`, `"sort"`, `"strconv"`, `"strings"`, `"time"`,
		`"runtime/debug"`,
		// ledger.go's store seam: context carries no writer, and internal/record returns
		// ledger rows from the fleet Redis that this package renders through oneline.Field
		// at the print site; record prints nothing itself.
		`"context"`,
		`"github.com/mas-bandwidth/nova-tools/internal/record"`,
		`"github.com/mas-bandwidth/nova-tools/internal/bounded"`,
		`"github.com/mas-bandwidth/nova-tools/internal/oneline"`,
		`"github.com/mas-bandwidth/nova-tools/internal/tokens"`,
	},
	MinClassified: 30,
}
