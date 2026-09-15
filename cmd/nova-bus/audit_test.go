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
		"main.go|parse|f.verb":            "the verb's own name, a literal at every newFlags call site in this file",
		"main.go|parse|name":              "a required flag's name, a literal map key at every call site in this file",
		"main.go|count|f.verb":            "the verb's own name, a literal at every newFlags call site in this file",
		"main.go|count|name":              "a required flag's name, a literal at every call site in this file",
		"main.go|receiptMaxWords|f.verb":  "the verb's own name, a literal at every newFlags call site in this file",
		"main.go|openBus|verb":            "the verb's own name, a literal at every call site in this file",
		"main.go|printOpenEntries|token":  "the event's second token, one of the three literals NOTE, HEARD and RECEIPT assigned above the site",
		"main.go|printBodyItem|kind":      "the event's second token, one of the three literals NOTE, HEARD and RECEIPT assigned immediately above the site",
		"main.go|printBodyItem|bodyBytes": "the note body is the explicitly requested verbatim byte payload; framing is emitted separately and the body is never escaped or rewritten",
		"main.go|atLeastZero|f.verb":      "the verb's own name, a literal at every newFlags call site in this file",
		"main.go|atLeastZero|name":        "a threshold flag's name, a literal at every call site in this file",
		"main.go|lockCheckout|token":      "the verb's own event token, the literals \"INBOX\" and \"WAIT\" at the two call sites in this file",
		"main.go|gitArgs|f.verb":          "the verb's own name, a literal at every newFlags call site in this file",
		"main.go|attempts|f.verb":         "the verb's own name, a literal at every newFlags call site in this file",
		"main.go|gitTimeoutFlag|f.verb":   "the verb's own name, a literal at every newFlags call site in this file",
		"main.go|legacyLine|verb":         "the verb's own name, the literals \"inbox\", \"wait\" and \"check\" at the three call sites in this file",
		"main.go|waitLoop|lines": "the inbox listing this run has ALREADY printed, through the escape, into waitPoll's buffer -- every value in it went " +
			"through oneline.Field or oneline.Escape at the site that wrote it. It is many lines and it is not an event line: escaping it here would " +
			"fold a whole listing into one unreadable line, which is the mistake printTranscript below documents. The buffer exists because a poll " +
			"that finds nothing must print nothing, not because anything about the text changed. TestWaitReturnsWhenANoteArrivesDuringTheWait is the " +
			"behavioural test for this site.",
		"main.go|waitLoop|next": "the caller's own wait command echoed back after `next=` so they can re-arm it: flag names are literals and every value " +
			"in it has already been through the flag parser (--bus is a repo root, --as a roster name, --remote and --branch through bus.ValidGitArg, " +
			"durations and counts are numbers), and it must stay pasteable spaces-and-all -- escaping it folds a command into one unreadable token, " +
			"the same one-line-vs-pasteable tradeoff as printSwitchDayNote's INBOX SWITCH sentence. TestWaitEndsWithRearmLine is the behavioural test.",
		"main.go|hiddenReason|legacy.Text": "not an event line: this function BUILDS a sentence, and both sites that print it pass the whole of it through " +
			"oneline.Escape, so the one-line guarantee is made once over the finished sentence rather than twice over its parts. The value itself is a " +
			"switch-day line that has been through bus.NewLegacyLine, so it is a UTC date or an RFC 3339 instant and nothing else.",
		"main.go|hiddenReason|at": "the same sentence, and a time this code formatted itself with bus.LegacyInstantLayout; two sites in the one call.",
		"main.go|cmdDraft|name":   "a reply-only flag's name, one of the five literals in replyOnlyFlags (reply.go), which this loop walks",
		"reply.go|cmdDraftReply|offTheListingReason(c, t, target, me, legacy, hasCursor, replyTargetName(target))": "not an event line's argument but a SENTENCE this package built, the way hiddenReason's is: " +
			"every value inside it went through oneline.Field at the site that wrote it, and the sentence is one line by construction. The one-line " +
			"guarantee is made once over the finished sentence rather than twice over its parts. TestTargetNotOnTheOpenListIsItsOwnRefusal is the " +
			"behavioural test for this site.",
		"reply.go|cmdDraftReply|note": "one DRAFT NOTE sentence out of the finite, enumerated set in docs/SPEC-BUS-REPLY.md, built above this loop: every value in " +
			"one is a literal or has been through oneline.Field or oneline.Quote at the site that wrote it. TestReplyReceiptStaysOneLineAtSixHundredOpenNotes " +
			"holds the count and TestReplySubjectMatchingTwoNotesTakesTheNewestAndSaysSo holds the text.",
		"main.go|cmdDraft|skeleton": "the skeleton itself, printed to stdout VERBATIM because it is a FILE and not an event line: a header of " +
			"several lines that a person redirects into a draft, and an escape would fold it into one unusable line -- the same mistake " +
			"as the escaped rebase transcript below. Every value in it has been checked before this line runs: From is the roster's own " +
			"spelling, To, Cc and every Re resolved against the roster and the bus, and --subject passed bus.OneLine, which refuses a " +
			"line break or a control character. Nothing unresolved reaches here: an unresolved anything is a DRAFT REFUSED on stderr and " +
			"this line never runs. TestDraftPrintsASkeletonTheParserReadsBack is the behavioural test for this site.",
		"main.go|cmdPrepare|artifactJSON": "the prepared artifact itself, printed to stdout VERBATIM because it is a machine-readable JSON " +
			"object and not an event line: a self-contained artifact that a caller saves, and an escape would fold it or escape its quotes. " +
			"Every value in it has been checked before this line runs. TestPrepareDecidingTests is the behavioural test for this site.",
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
		"oneline.Quote", "quoteList", "cappedList",
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
		// errors is read-only over error VALUES -- errors.Is and errors.As, which
		// refuseContinuation uses to tell one --after refusal from another -- and
		// errors.New, which makes one. None of the three holds a writer or reaches a
		// stream: the sentence they choose between is printed by fmt at the site above,
		// through oneline like every other line here.
		//
		// strconv is the same shape from the other side: FormatInt turns the byte count on
		// an INBOX BODIES GAP line into digits. It is a converter, it writes to no stream,
		// and its result reaches the line through oneline.Field.
		`"bytes"`, `"encoding/base64"`, `"encoding/json"`, `"errors"`, `"flag"`, `"fmt"`, `"io"`, `"os"`, `"path/filepath"`, `"strconv"`, `"strings"`, `"time"`,
		`"github.com/mas-bandwidth/nova-tools/internal/bus"`,
		// board is `wait`'s re-arm command: board.Quote single-quotes each argument of the
		// next= command it echoes, so a --bus path holding a space pastes back whole. It is
		// a pure string transformer that holds no writer and reaches no stream -- its result
		// lands in the next= value, which the waitLoop|next exemption above owns, and goes
		// through fmt like every other value there.
		`"github.com/mas-bandwidth/nova-tools/internal/board"`,
		// errors is reply.go's: errors.Is over the two sentinel refusals a no-replace
		// publish makes, and errors.New for one refusal's own text. It holds no writer at
		// all and reaches no stream.
		`"errors"`,
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
