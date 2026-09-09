package main

import (
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/oneline/audit"
)

// The source-level tripwire behind the one-line guarantee: every argument this binary
// prints is quoted, numeric, literal, escaped through internal/oneline, or exempted here
// with its reason. See package audit for what the two walks see and what they cannot.
func TestEveryPrintedArgumentIsLiteralQuotedOrEscaped(t *testing.T) {
	audit.PrintedArguments(t, messageBusAudit)
}

func TestNoOtherWriterOrShadowCanBypassTheEscape(t *testing.T) {
	audit.Bypasses(t, messageBusAudit)
}

var messageBusAudit = audit.Config{
	// One entry per site, keyed by file, function and source text; sites with the same
	// text in the same function share an entry. Each is a claim a reader can check.
	Exempt: map[string]string{
		"main.go|parse|f.verb":   "the verb's own name, a literal at every newFlags call site in this file; three sites",
		"main.go|parse|name":     "a required flag's name, a literal map key at every call site in this file",
		"main.go|count|f.verb":   "the verb's own name, a literal at every newFlags call site in this file",
		"main.go|count|name":     "a required flag's name, a literal at both call sites in this file",
		"main.go|openTable|verb": "the verb's own name, a literal at every call site in this file; two sites",
		"main.go|cmdInbox|token": "the event's second token, one of the three literals NOTE, HEARD and RECEIPT assigned above the site",
		"main.go|cmdCheck|token": "the event's two tokens, the literals BUS FAIL and BUS WARN assigned above the site",
		"main.go|gitArgs|f.verb": "the verb's own name, a literal at every newFlags call site in this file",
	},
	Imports: []string{
		`"flag"`, `"fmt"`, `"io"`, `"os"`, `"strings"`, `"time"`,
		`"github.com/mas-bandwidth/nova-tools/internal/bus"`,
		// version.go, and the reason each one cannot write past the escape: runtime
		// answers GOOS, GOARCH and Version and holds no writer at all; runtime/debug
		// is read here for ReadBuildInfo only, and its printing half (PrintStack,
		// SetTraceback) writes to a stream this binary never hands it. Both values
		// reach the line through oneline.Field like any other, which is why neither
		// appears in Exempt above.
		`"runtime"`, `"runtime/debug"`,
	},
	MinClassified: 140,
}
