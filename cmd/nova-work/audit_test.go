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
	Escapers: []string{"oneline.Quote"},
	Imports: []string{
		// buildinfo answers which build this is; its Line renders every field through
		// oneline.Field itself, and the version print site wraps the result in
		// oneline.Escape so the tripwire sees the escape.
		`"github.com/mas-bandwidth/nova-tools/internal/buildinfo"`,
		// jobs holds the graph values this binary reads and prints through oneline
		// fields; it writes nothing and reaches no network.
		`"github.com/mas-bandwidth/nova-tools/internal/jobs"`,
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
		// The events verb's edges. context is CancelFunc plumbing for the deadline and
		// writes nothing; time is duration parsing and the injected clock, both rendered
		// through oneline; the redis client and internal/ci are read and published
		// through their own APIs, and this package prints only the escaped lines below,
		// so none of them writes past oneline.
		`"context"`,
		`"time"`,
		`"github.com/redis/go-redis/v9"`,
		`"github.com/mas-bandwidth/nova-tools/internal/ci"`,
		`"flag"`, `"fmt"`, `"io"`, `"os"`, `"strings"`,
	},
	MinClassified: 10,
}
