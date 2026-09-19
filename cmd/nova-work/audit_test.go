package main

import (
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/oneline/audit"
)

// The source-level tripwire behind the one-line guarantee: every argument this binary
// prints is quoted, numeric, literal, escaped through internal/oneline, or exempted here
// with its reason. See package audit for what the two walks see and what they cannot.
func TestEveryPrintedArgumentIsLiteralQuotedOrEscaped(t *testing.T) {
	audit.PrintedArguments(t, workAudit)
}

func TestNoOtherWriterOrShadowCanBypassTheEscape(t *testing.T) {
	audit.Bypasses(t, workAudit)
}

var workAudit = audit.Config{
	// oneline.Quote is the pasteable third rendering in that package: the resolver is
	// a command a person can copy out of the READY row and type back in, and Field
	// would render its space as \x20 and make it unpasteable.
	// field is set check's own thin wrapper over oneline.Field: the same escape,
	// with an absent value rendered `-` so that every SET line of one run has the
	// same shape. It cannot return an unescaped string, so the tripwire counts it.
	Escapers: []string{"oneline.Quote", "field"},
	Imports: []string{
		// buildinfo answers which build this is; its Line renders every field through
		// oneline.Field itself, and the version print site wraps the result in
		// oneline.Escape so the tripwire sees the escape.
		`"github.com/mas-bandwidth/nova-tools/internal/buildinfo"`,
		// jobs holds the graph values this binary reads and prints through oneline
		// fields; it writes nothing and reaches no network.
		`"github.com/mas-bandwidth/nova-tools/internal/jobs"`,
		// decide is the ladder `next` routes over. It is the seam onto
		// nova-decide's own rules, it prints nothing itself, and every field of
		// its answer that reaches a NEXT line goes through an oneline escape
		// here -- the rung and the reason both.
		`"github.com/mas-bandwidth/nova-tools/internal/decide"`,
		// swarm holds clip: it commits the card's branch, harvests the result and
		// resets the worktree with git, printing nothing; every path it is handed
		// comes from a flag and the one CLIP line goes through oneline fields.
		`"github.com/mas-bandwidth/nova-tools/internal/swarm"`,
		// worklang is the bounded plan reader; it parses bytes into values and every
		// field it yields is printed through oneline.
		`"github.com/mas-bandwidth/nova-tools/internal/worklang"`,
		// workclient is the S1 socket wire: it dials the session socket and writes the
		// request line, and writes nothing to stdout or stderr, so every byte a caller
		// reads is still printed by an escaped site in this package.
		`"github.com/mas-bandwidth/nova-tools/internal/workclient"`,
		// friends is the ask: it renders one note from a work set's unit, carries it
		// through nova-bus's own send path behind the Sender seam, and records the ask
		// back on the unit. It writes nothing to stdout or stderr, so every byte a
		// caller reads is still printed by an escaped site in this package.
		`"github.com/mas-bandwidth/nova-tools/internal/friends"`,
		// errors names ONE sentinel, errSendRefused, so a test can hand it to a fake
		// sender and watch nothing be recorded; it prints nothing.
		`"errors"`,
		// time parses ask's --deadline and --now and formats the stamps ask and asks
		// print, and it is the events verb's duration parsing and injected clock; every
		// one of those goes out through an oneline field.
		`"time"`,
		// The events verb's other edges. context is CancelFunc plumbing for the deadline
		// and writes nothing; the redis client and internal/ci are read and published
		// through their own APIs, and this package prints only the escaped lines below,
		// so none of them writes past oneline.
		`"context"`,
		`"github.com/redis/go-redis/v9"`,
		`"github.com/mas-bandwidth/nova-tools/internal/ci"`,
		// encoding/json reads set check's --minds registry, which is written in one of
		// the two JSON shapes a registry of minds already has. It decodes bytes into
		// names and prints nothing: every name it yields reaches stdout only through an
		// oneline field on a SET line in this package.
		`"encoding/json"`,
		// sort orders the candidates `next` chooses between -- by the ladder's
		// confidence, ties in the order the author wrote them -- and orders the
		// edits the attempt writer splices. It compares values and writes
		// nothing.
		`"sort"`,
		// record holds the durable card-result values and the store and consumer the
		// record verbs drive. It writes only through the store; every row it formats
		// goes out through record.FormatRow, which renders each field through
		// oneline.Field.
		`"github.com/mas-bandwidth/nova-tools/internal/record"`,
		// bounded is the capped line writer `results` prints through; every line it
		// takes is already a record.FormatRow, and it escapes the remedy it writes.
		`"github.com/mas-bandwidth/nova-tools/internal/bounded"`,
		// context carries cancellation into the Redis consumer; it writes nothing.
		`"context"`,
		// time is the clock behind the deps seam and the durations --deadline and
		// --since parse; the two durations a refusal names are rendered through
		// oneline.Escape before they reach a line.
		`"time"`,
		`"flag"`, `"fmt"`, `"io"`, `"os"`, `"strings"`,
	},
	MinClassified: 10,
}
