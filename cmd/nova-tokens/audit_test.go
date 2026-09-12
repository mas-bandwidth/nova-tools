package main

import (
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/oneline/audit"
)

// The source-level tripwire behind the one-line guarantee: every argument this binary
// prints is quoted, numeric, literal, escaped through internal/oneline, or exempted here
// with its reason.
func TestEveryPrintedArgumentIsLiteralQuotedOrEscaped(t *testing.T) {
	audit.PrintedArguments(t, tokensAudit)
}

func TestNoOtherWriterOrShadowCanBypassTheEscape(t *testing.T) {
	audit.Bypasses(t, tokensAudit)
}

var tokensAudit = audit.Config{
	Escapers: []string{"sourceLine", "unreadableLine", "unparsedLine", "dayLine", "aggFields"},
	// One entry per site, keyed by file, function and source text; each is a claim a
	// reader can check.
	Exempt: map[string]string{
		"main.go|cmdFold|counts": "the count line, built two lines above by a Sprintf whose every verb is %d over an integer; the classifier walks that Sprintf like any other print site. Two sites: the OK line and the FAIL line",
		"main.go|cmdReport|body": "the report's stdout IS the artifact: every line of it was rendered by tokens.BodyLine, which puts each of its stored fields through oneline.Field, and the lines are joined with \\n by this function. Escaping the join again would escape those newlines and destroy the note body this verb exists to print",
	},
	Imports: []string{
		`"flag"`, `"fmt"`, `"io"`, `"os"`, `"runtime"`, `"sort"`, `"strconv"`, `"strings"`, `"time"`,
		`"runtime/debug"`,
		`"github.com/mas-bandwidth/nova-tools/internal/bounded"`,
		`"github.com/mas-bandwidth/nova-tools/internal/oneline"`,
		`"github.com/mas-bandwidth/nova-tools/internal/tokens"`,
	},
	MinClassified: 30,
}
