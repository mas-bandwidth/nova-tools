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
		`"flag"`, `"fmt"`, `"io"`, `"os"`, `"strings"`,
		// context (push) only supplies the background context each Redis call is made
		// under; it holds no writer of its own.
		`"context"`,
		// crypto/rand (push) fills the card id's sixteen random bytes; a read into a
		// local array holds no writer and writes no stream.
		`"crypto/rand"`,
		// encoding/hex (push) renders those bytes as the id string; bytes in, a string
		// out, and it holds no writer.
		`"encoding/hex"`,
		// path/filepath (push) splits the card path into its base and extension to
		// derive the label; it reads no stream and writes none.
		`"path/filepath"`,
		// regexp (push) compiles the label grammar. The *regexp.Regexp holds no writer;
		// it is only asked whether the label matches.
		`"regexp"`,
		// strconv (push) turns the priority int into the one field value it occupies;
		// Itoa of an int cannot hold a control character and holds no writer.
		`"strconv"`,
		// time (push) stamps pushed-at and supplies main's clock; a UTC timestamp
		// rendered in RFC3339 cannot hold a control character and time holds no writer.
		`"time"`,
		// redisq (push) is the one interface every Redis call goes through: the XADD
		// that appends the card and the XGROUP CREATE that ensures the group. It opens
		// no stream of its own, and a refused dial reaches stderr through oneline.Err.
		`"github.com/mas-bandwidth/nova-tools/internal/redisq"`,
	},
	MinClassified: 10,
}
