package main

import (
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/oneline/audit"
)

// The source-level tripwire behind the one-line guarantee: every argument this binary
// prints is quoted, numeric, literal, escaped through internal/oneline, or exempted here
// with its reason. See package audit for what the two walks see and what they cannot.
func TestEveryPrintedArgumentIsLiteralQuotedOrEscaped(t *testing.T) {
	audit.PrintedArguments(t, swarmAudit)
}

func TestNoOtherWriterOrShadowCanBypassTheEscape(t *testing.T) {
	audit.Bypasses(t, swarmAudit)
}

var swarmAudit = audit.Config{
	// One entry per site, keyed by file, function and source text; sites with the same
	// text in the same function share an entry. Each is a claim a reader can check.
	Exempt: map[string]string{
		"main.go|parse|f.verb":     "the verb's own name, a literal at every newFlags call site in this file",
		"main.go|want|name":        "a required flag's name, a literal at every call site in this file",
		"main.go|want|wants":       "the guidance that flag wants, a literal at every call site in this file",
		"main.go|wantCount|name":   "a required count flag's name, a literal at every call site in this file",
		"main.go|wantCount|wants":  "the guidance that count flag wants, a literal at every call site in this file",
		"main.go|refused|f.verb":   "the verb's own name, the value newFlags stored from that literal",
		"main.go|openPool|verb":    "the verb's own name, a literal at every call site in this file",
		"main.go|slotWord|sc.Slot": "an int from the sidecar; fmt.Sprint of an int cannot hold a control character",
		"main.go|cmdTemplate|body": "the named verbatim site: a template is a DOCUMENT a person redirects into a file, not an event line, so escaping it would fold it into one unusable line. Every byte of it is an embedded constant in package swarm. TestTemplatesCarryTheirConditions is the behavioural test for this site.",
		"main.go|cmdFinalize|line": "the other verbatim site, `finalize`'s line: swarm.FinalizeByHand BUILDS the whole sentence and passes every caller-supplied value through oneline.Field or oneline.Err, so the one-line guarantee is already made over the finished line. Escaping it a second time here would fold that line into one unreadable \\x0a form. Both print sites in this function share this entry; TestADispatcherRunsAJobEndToEnd exercises the path.",
	},
	Imports: []string{
		// package flag's two mouths are closed in newFlags: SetOutput(io.Discard) and a
		// no-op Usage, so it cannot print an argument this audit never sees. fmt, io, os,
		// os/exec, path/filepath, strings and time hold writers, and none here writes to a
		// stream except through the fmt calls the classifier walks; os/exec starts children
		// whose own output is the harness's, not this binary's line.
		`"flag"`, `"fmt"`, `"io"`, `"os"`, `"os/exec"`, `"path/filepath"`, `"strings"`, `"time"`,
		// bounded prints the capped listings and the one MORE line that stands for what
		// they did not print. Every line reaching it is rendered by a fmt.Sprintf in THIS
		// package, which the classifier walks like any other print site, and bounded puts
		// its own two fields -- the kind and the remedy -- through oneline before writing
		// them. It writes to the stream the caller hands it and to nothing else.
		`"github.com/mas-bandwidth/nova-tools/internal/bounded"`,
		// swarm builds the report, triage, cost, status and finalize lines. Every one of
		// them is rendered by a fmt.Sprintf inside that package, so this walk cannot see
		// its arguments; the values that reach one are put through oneline by that package,
		// and the two lines this binary prints whole are the two exempted verbatim sites
		// above.
		`"github.com/mas-bandwidth/nova-tools/internal/swarm"`,
	},
	MinClassified: 40,
}
