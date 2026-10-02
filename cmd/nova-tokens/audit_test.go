package main

import (
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/oneline/audit"
)

// The source-level tripwire behind the one-line guarantee: every argument this binary
// prints is quoted, numeric, literal, escaped through internal/oneline, or exempted here
// with its reason.
func TestEveryPrintedArgumentIsLiteralQuotedOrEscaped(t *testing.T) {
	t.Parallel()

	audit.PrintedArguments(t, tokensAudit)
}

func TestNoOtherWriterOrShadowCanBypassTheEscape(t *testing.T) {
	t.Parallel()

	audit.Bypasses(t, tokensAudit)
}

var tokensAudit = audit.Config{
	// s.line, s.factFields and s.dryRunFields (out.go) build a typed line from key=value
	// pairs, every key and value through oneline.Field and a tail through oneline.Escape;
	// fields is the loop they share, and its one Sprint site is exempt below for that
	// reason.
	Escapers: []string{"sourceLine", "unreadableLine", "unparsedLine", "dayLine", "s.line", "s.factFields", "s.dryRunFields"},
	// One entry per site, keyed by file, function and source text; each is a claim a
	// reader can check.
	Exempt: map[string]string{
		"ledger.go|cmdReportStore|line": "one REPORT line built in the loop above from literal key names, oneline.Field over each key value, a %d row count and strconv.FormatInt or the literal dash per type; nothing in it is unescaped text",
		"out.go|fields|kv[i+1]":         "a value of a key=value pair, put through oneline.Field on the same line",
		"main.go|cmdReport|body":        "the report's stdout IS the artifact: every line of it was rendered by tokens.BodyLine, which puts each of its stored fields through oneline.Field, and the lines are joined with \\n by this function. Escaping the join again would escape those newlines and destroy the note body this verb exists to print",
	},
	Imports: []string{
		// the verb-help seam (the CLI style's rule (b), #4505): on -h it prints only flag names,
		// their usage literals and lines of this package's own usage const, to the stdout run
		// hands it; it never prints an argument, so nothing it writes can carry a newline in.
		`"github.com/mas-bandwidth/nova-tools/internal/nsprint/verbflag"`,
		// version.go's resolution order, which now lives once in internal/buildinfo
		// rather than in a copy per binary: it reads debug.ReadBuildInfo, holds no
		// writer of its own, and returns a string this package renders through
		// oneline.Field at the print site below.
		`"github.com/mas-bandwidth/nova-tools/internal/buildinfo"`,
		`"flag"`, `"fmt"`, `"io"`, `"os"`, `"path/filepath"`, `"runtime"`, `"sort"`, `"strconv"`, `"strings"`, `"time"`,
		// maps and slices hold no writer: they return keys, sorted copies and membership,
		// which this package renders through oneline at its own print sites.
		`"maps"`, `"slices"`,
		// syscall is read for one constant, ENOTDIR, the error os.MkdirAll gives a path
		// that exists and is no directory (session's dry run names it as the write would);
		// it writes nothing.
		`"syscall"`,
		`"runtime/debug"`,
		// ledger.go's store seam: context carries no writer, and internal/record returns
		// ledger rows from the fleet Redis that this package renders through oneline.Field
		// at the print site; record prints nothing itself.
		`"context"`,
		`"github.com/mas-bandwidth/nova-tools/internal/record"`,
		// ledger.go's seat (#3461): redisauth.Auth (a leaf: no net, no os/exec) resolves the ACL user and the password from
		// the environment, the one config nova-sprint dials with; it holds no writer and
		// returns strings and an error this package prints through oneline.Err.
		`"github.com/mas-bandwidth/nova-tools/internal/nsprint/redisauth"`,
		`"github.com/mas-bandwidth/nova-tools/internal/bounded"`,
		// atomicfile writes one FILE whole (the report note and the ledger): it takes a path
		// and the bytes of the file and puts no byte on any stream of this binary.
		`"github.com/mas-bandwidth/nova-tools/internal/atomicfile"`,
		`"github.com/mas-bandwidth/nova-tools/internal/oneline"`,
		`"github.com/mas-bandwidth/nova-tools/internal/tokens"`,
		// the --json rendering and the refusal under --json: tool.Out renders the one
		// result as a JSON object (encoding/json, every string escaped) on the stdout this
		// package hands it, and prints nothing else.
		`"github.com/mas-bandwidth/nova-tools/internal/tool"`,
	},
	MinClassified: 30,
}
