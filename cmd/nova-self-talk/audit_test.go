package main

import (
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/oneline/audit"
)

// The source-level tripwire behind the one-line guarantee: every argument this binary
// prints is quoted, numeric, literal, escaped through internal/oneline, or exempted here
// with its reason. See package audit for what the two walks see and what they cannot.
func TestEveryPrintedArgumentIsLiteralQuotedOrEscaped(t *testing.T) {
	audit.PrintedArguments(t, selfTalkAudit)
}

func TestNoOtherWriterOrShadowCanBypassTheEscape(t *testing.T) {
	audit.Bypasses(t, selfTalkAudit)
}

var selfTalkAudit = audit.Config{
	// One entry per site, keyed by file, function and source text; sites with the same
	// text in the same function share an entry. Each is a claim a reader can check.
	Exempt: map[string]string{
		"main.go|run|c.Verdict":                   "a selftalk.Verdict, one of the two constants package selftalk declares; two sites",
		"main.go|run|selftalk.RuleDocumentBanner": "a constant in package selftalk",
		"main.go|run|i.Shape":                     "one of the four Shape constants package selftalk declares",
	},
	Imports: []string{
		`"errors"`, `"flag"`, `"fmt"`, `"io"`, `"os"`, `"strings"`,
		`"github.com/mas-bandwidth/nova-tools/internal/selftalk"`,
	},
	MinClassified: 10,
}
