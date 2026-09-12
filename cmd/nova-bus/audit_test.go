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
	audit.PrintedArguments(t, messageBusAudit)
}

func TestNoOtherWriterOrShadowCanBypassTheEscape(t *testing.T) {
	t.Parallel()
	audit.Bypasses(t, messageBusAudit)
}

var messageBusAudit = audit.Config{
	// One entry per site, keyed by file, function and source text; sites with the same
	// text in the same function share an entry. Each is a claim a reader can check.
	Exempt: map[string]string{
		"main.go|parse|f.verb":           "the verb's own name, a literal at every newFlags call site in this file",
		"main.go|parse|name":             "a required flag's name, a literal map key at every call site in this file",
		"main.go|count|f.verb":           "the verb's own name, a literal at every newFlags call site in this file",
		"main.go|count|name":             "a required flag's name, a literal at every call site in this file",
		"main.go|openBus|verb":           "the verb's own name, a literal at every call site in this file",
		"main.go|printOpenEntries|token": "the event's second token, one of the three literals NOTE, HEARD and RECEIPT assigned above the site",
		"main.go|atLeastZero|f.verb":     "the verb's own name, a literal at every newFlags call site in this file",
		"main.go|atLeastZero|name":       "a threshold flag's name, a literal at every call site in this file",
		"main.go|lockCheckout|token":     "the verb's own event token, the literals \"INBOX\" and \"WAIT\" at the two call sites in this file",
		"main.go|gitArgs|f.verb":         "the verb's own name, a literal at every newFlags call site in this file",
		"main.go|attempts|f.verb":        "the verb's own name, a literal at every newFlags call site in this file",
		"main.go|gitTimeoutFlag|f.verb":  "the verb's own name, a literal at every newFlags call site in this file",
		"main.go|legacyLine|verb":        "the verb's own name, the literals \"inbox\", \"wait\" and \"check\" at the three call sites in this file",
		"main.go|waitLoop|lines": "the inbox listing this run has ALREADY printed, through the escape, into waitPoll's buffer -- every value in it went " +
			"through oneline.Field or oneline.Escape at the site that wrote it. It is many lines and it is not an event line: escaping it here would " +
			"fold a whole listing into one unreadable line, which is the mistake printTranscript below documents. The buffer exists because a poll " +
			"that finds nothing must print nothing, not because anything about the text changed. TestWaitReturnsWhenANoteArrivesDuringTheWait is the " +
			"behavioural test for this site.",
		"main.go|hiddenReason|legacy.Text": "not an event line: this function BUILDS a sentence, and both sites that print it pass the whole of it through " +
			"oneline.Escape, so the one-line guarantee is made once over the finished sentence rather than twice over its parts. The value itself is a " +
			"switch-day line that has been through bus.NewLegacyLine, so it is a UTC date or an RFC 3339 instant and nothing else.",
		"main.go|hiddenReason|at": "the same sentence, and a time this code formatted itself with bus.LegacyInstantLayout; two sites in the one call.",
		"main.go|cmdDraft|skeleton": "the skeleton itself, printed to stdout VERBATIM because it is a FILE and not an event line: a header of " +
			"several lines that a person redirects into a draft, and an escape would fold it into one unusable line -- the same mistake " +
			"as the escaped rebase transcript below. Every value in it has been checked before this line runs: From is the roster's own " +
			"spelling, To, Cc and every Re resolved against the roster and the bus, and --subject passed bus.OneLine, which refuses a " +
			"line break or a control character. Nothing unresolved reaches here: an unresolved anything is a DRAFT REFUSED on stderr and " +
			"this line never runs. TestDraftPrintsASkeletonTheParserReadsBack is the behavioural test for this site.",
		"main.go|printTranscript|tr": "git's own transcript, printed to stderr VERBATIM and deliberately not through the escape. " +
			"It is not an event line: the escaped, one-line SEND/RECEIPT/INBOX FAIL above it is, and this is the text a person " +
			"opened the terminal to read. Escaping it is what this change removes -- a forty-line rebase transcript rendered as " +
			"one line of \\x0d\\x0a, which nobody could read and which taught nobody anything. " +
			"TestARebaseConflictPrintsOneActionableLineAndTheTranscriptRaw is the behavioural test for this site.",
	},
	Escapers: []string{
		// Quote is oneline's third rendering: a double-quoted Go string literal, which
		// escapes every control character, every unprintable rune (U+2028, U+2029 and the
		// bidi controls among them) and the quote and backslash, so it is one line whatever
		// the value holds. quoteList is this package's own wrapper over it and its body is
		// walked by the same classifier.
		"oneline.Quote", "quoteList",
	},
	Imports: []string{
		// version.go's resolution order, which now lives once in internal/buildinfo
		// rather than in a copy per binary: it reads debug.ReadBuildInfo, holds no
		// writer of its own, and returns a string this package renders through
		// oneline.Field at the print site below.
		`"github.com/mas-bandwidth/nova-tools/internal/buildinfo"`,
		// bytes is `wait`'s buffer and holds no writer of its own: a bytes.Buffer is
		// written by the same fmt calls this walk classifies -- inboxListing prints INTO
		// one -- and read back as a string that is printed at the single exempted site
		// above. It reaches no stream by itself.
		`"bytes"`, `"flag"`, `"fmt"`, `"io"`, `"os"`, `"strings"`, `"time"`,
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
