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

	audit.PrintedArguments(t, workAudit)
}

func TestNoOtherWriterOrShadowCanBypassTheEscape(t *testing.T) {
	t.Parallel()

	audit.Bypasses(t, workAudit)
}

var workAudit = audit.Config{
	Exempt: map[string]string{
		"help.go|printVerbHelp|line": "prints lines sliced out of the static usage const",
	},
	// oneline.Quote is the pasteable third rendering in that package: the resolver is
	// a command a person can copy out of the READY row and type back in, and Field
	// would render its space as \x20 and make it unpasteable.
	// field is set check's own thin wrapper over oneline.Field: the same escape,
	// with an absent value rendered `-` so that every SET line of one run has the
	// same shape. It cannot return an unescaped string, so the tripwire counts it.
	Escapers: []string{"oneline.Quote", "field"},
	Imports: []string{
		// bounded is the capped line writer the dogfood report (#2762) and visualize
		// (#2883) print through: every line it takes is already escaped by the caller,
		// and it escapes the remedy it writes. Listed before #2623 removed its last
		// caller; restored with these two.
		`"github.com/mas-bandwidth/nova-tools/internal/bounded"`,
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
		// workreconcile is the E09 proving run and reconciliation engine (Issue #2082).
		`"github.com/mas-bandwidth/nova-tools/internal/workreconcile"`,
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
		// sort orders socketverbs.go's derived lists -- the sub-verbs a family
		// refusal names and the whole socket verb set a test walks -- so that
		// two runs print one line. It reorders strings this package already
		// holds and writes nothing itself.
		`"sort"`,
		// landed is set check --evaluate's resolver (#2664): it runs gh through the
		// Runner seam and returns verdicts; it writes nothing to stdout or stderr, and
		// every token of a verdict reaches a SET EVAL line through field.
		`"github.com/mas-bandwidth/nova-tools/internal/landed"`,
		// bytes holds set check's SET EVAL lines until the findings are printed; every
		// line in it was already written through the escaped sites of setland.go.
		`"bytes"`,
		// The push verb's edges. redisq is the ready set both this binary and nova-swarm
		// reach through; it takes a map of fields and returns an id, and every field it
		// hands back reaches stdout through an oneline field on the PUSH line here.
		`"github.com/mas-bandwidth/nova-tools/internal/redisq"`,
		// crypto/rand and encoding/hex mint the directory mode's card id, which Redis
		// mints for itself. They produce hex bytes and print nothing.
		`"crypto/rand"`,
		`"encoding/hex"`,
		// path/filepath names the temp file --write-status renames over the work set,
		// beside it in the same directory, and takes the push verb's card label off the
		// --card path with Base and Ext. It writes nothing, and the label it yields is
		// refused unless it matches [A-Za-z0-9._-]+ before it ever reaches a line.
		`"path/filepath"`,
		// regexp holds that one anchored label pattern and nothing else; it matches and
		// prints nothing.
		`"regexp"`,
		// strconv renders --priority and the id fallback as digits. A number has nothing
		// in it to escape, which is the audit's own numeric case.
		`"strconv"`,
		// safepath validates --stream so that directory-mode streams cannot escape the
		// queue root; it matches and prints nothing.
		`"github.com/mas-bandwidth/nova-tools/internal/safepath"`,
		// os/exec runs the verification verb's two reaches (#3459): the acceptance
		// suite, whose combined output is captured into a byte slice and parsed, and
		// git rev-parse/status, whose output is a sha and a dirty flag. Neither is
		// wired to stdout or stderr; a test name or sha reaches a VERIFICATION line
		// only through an oneline field.
		`"os/exec"`,
		`"flag"`, `"fmt"`, `"io"`, `"os"`, `"strings"`,
	},
	MinClassified: 10,
}
