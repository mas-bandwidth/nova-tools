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
	// buildinfo.Line is the `version` verb's whole line and the fifth escaper: it renders
	// every one of its four fields through oneline.Field inside internal/buildinfo, where
	// TestLineShape and TestLineHoldsWhateverTheStampContains pin it -- including against
	// a release stamp holding a newline, which is the one field of that line that comes
	// from outside the toolchain.
	Escapers: []string{
		"buildinfo.Line",
		// usageSuffix (main.go) renders the NATIVE OK suffix and puts the looked-for store
		// path through oneline.Field inside itself before returning, so the ` usage=none
		// path=<looked>` tail it adds is already one safe token. Only the empty string (a
		// store answered) and an oneline.Field-escaped path can come back.
		"usageSuffix",
		// fenceSuffix (main.go, issue #644) renders the other NATIVE OK tail and puts the
		// rejected path through oneline.Field inside itself before returning, so the
		// ` fence=rejected path=<p>` it adds is already one safe token. The path comes from
		// the harness's own capture -- a file a card can write -- so nothing but the empty
		// string and an oneline.Field-escaped path can come back.
		"fenceSuffix",
	},
	Imports: []string{
		// version.go, and the reason it cannot write past the escape: buildinfo reads
		// debug.ReadBuildInfo and runtime's GOOS, GOARCH and Version, holds no writer of
		// its own, and returns a STRING that this package prints -- rendered field by
		// field through oneline.Field before it is returned.
		`"github.com/mas-bandwidth/nova-tools/internal/buildinfo"`,
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
		// native.go (issue #296) needs these five and none of them writes a stream, so
		// none can write past the escape. context only gives CommandContext its deadline
		// and holds no writer; crypto/sha256 and encoding/hex compute and hex-encode the
		// two recorded hashes (bytes in, a string out); encoding/base64 decodes the wall's
		// cwdb64 receipt and holds no writer, and the cwd it yields is put through
		// oneline.Field before this package prints it; encoding/json reads the auth file
		// and writes only dataHome/auth.json, which is the child's credential, not this
		// binary's line.
		`"context"`, `"crypto/sha256"`, `"encoding/base64"`, `"encoding/hex"`, `"encoding/json"`,
		// publish.go (slice 7) needs bytes and it writes to no stream. bytes.Buffer only
		// holds the trimmed stdout/stderr of the git and gh children it samples, and every
		// one of those strings is put through oneline.Field or oneline.Err before this
		// package prints it, so none of it can write past the escape.
		`"bytes"`,
		// strconv (native.go, slice 10) only turns the child's exit code into the one
		// column it occupies: Itoa of an int cannot hold a control character, and it
		// holds no writer of its own.
		`"strconv"`,
		// runtime (authmode.go, issue #915) reads GOOS and nothing else. It holds no
		// writer, and the one value selects which permission-bit rule the auth copy asks:
		// NTFS reports 0666 for every readable file, so the unix looseness check refused
		// every auth file on windows-latest. It is the same platform question
		// executable.go asks of PATHEXT, made a parameter so linux can hold the windows
		// answer to its contract.
		`"runtime"`,
		// regexp (lint.go) compiles the patterns the card lint matches a card's text
		// against; a *regexp.Regexp holds no writer and writes no stream. Every line it
		// selects leaves this package through oneline.Escape(oneline.Cap(...)) on the
		// LINT DRIFT line, so a card cannot write past the escape. It only reads the one
		// file the caller named and writes nothing at all.
		`"regexp"`,
	},
	MinClassified: 40,
}
