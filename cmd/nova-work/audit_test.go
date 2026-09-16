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
	// The session client prints the session's own answer line byte for byte, so
	// escaping it would break the wire contract the spec pins; the line is read
	// back as one newline-terminated line and can add no second line.
	Exempt: map[string]string{
		"main.go|printReply|line": "the session's ONE answer line is passed through byte for byte, already bounded to one line by readReply",
	},
	Imports: []string{
		// bytes and errors bound the one reply line the client reads back from the
		// socket; neither writes to a stream.
		`"bytes"`, `"errors"`,
		// net dials the Unix socket the session names; the request goes out through
		// io.Copy and the reply is printed through the escaped sites above.
		`"net"`,
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
		`"flag"`, `"fmt"`, `"io"`, `"os"`, `"strings"`,
	},
	MinClassified: 10,
}
