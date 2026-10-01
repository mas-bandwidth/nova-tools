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

	audit.PrintedArguments(t, swarmAudit)
}

func TestNoOtherWriterOrShadowCanBypassTheEscape(t *testing.T) {
	t.Parallel()

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
		"main.go|cmdTemplate|body": "the named verbatim site: a template is a DOCUMENT a person redirects into a file, not an event line, so escaping it would fold it into one unusable line. Every byte of it is an embedded constant in package swarm. TestTemplatesCarryTheirConditions is the behavioural test for this site.",
		"native.go|nativeRun|line": "the line NewWallReader announces a refusal on: swarm.WallRefusedLine builds the whole line and puts the kind, the path, the task and the step through oneline.Field inside itself, so what arrives at this closure is already one safe token, and escaping it a second time would fold it into one unreadable form. TestNativeIdleZeroWatchesNothing asserts the line this site prints byte for byte.",
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
		// doctorOtherSentence (doctor.go) renders the tail of a DOCTOR UNREADABLE line about
		// the other binary: its path through oneline.Field and its stamp
		// through doctorExcerpt (oneline.Cap then oneline.Escape) inside themselves, beside
		// literal words, so what comes back is one escaped token; doctorExcerpt is the
		// stamp's bounded escape itself.
		"doctorOtherSentence", "doctorExcerpt",
		// swarm.WallLine (issue #644's follow-up) builds the `WALL task=<id> path=<p>
		// step=<n> [commits=<n> branch=<name>]` report line and puts every field through
		// oneline.Field inside itself. The path and step come from the card's own log and
		// the branch from the clone, so nothing but escaped fields can come back.
		"swarm.WallLine",
		// swarm.WallRefusedLine (the wall-hang lane) builds `WALL REFUSED <what> <path>
		// task=<id> step=<n>` and puts every field through oneline.Field inside itself. The
		// kind, the path and the step all come from the card's own output -- a stream a card
		// writes -- so nothing but escaped fields can come back.
		"swarm.WallRefusedLine",
		// swarm.CardIdleLine is the same line for a card that named no refusal at all, and it
		// escapes the task and the step the same way; its idle seconds are a float verb.
		"swarm.CardIdleLine",
		// termSuffix (main.go, issue #779) renders the one token a manager's TERM carries,
		// ` reason=terminated`, and puts the reason through oneline.Field inside itself, so
		// it is another one-safe-token tail like fenceSuffix. Only the empty string and the
		// escaped literal can come back.
		"termSuffix",
		// stoppedSuffix (main.go, SPEC-SWARM rule 13d, issue #1545) renders the one token a
		// budget stop carries, ` stopped=<tokens|max_turns|max_cache_read|unverifiable>`,
		// and puts the word through oneline.Field inside itself, so it is another
		// one-safe-token tail like termSuffix. The word is one of four compile-time
		// constants in nativesample.go and never a caller-supplied value at all, so only
		// the empty string and an escaped literal can come back.
		"stoppedSuffix",
		// swarm.PublicRefusalLine (CARD-8390) renders the whole CARD REFUSED line and
		// puts the repo and the worker name through oneline.Field inside
		// internal/swarm before returning, so the line it returns is already one
		// safe token. The repo comes from the card's own text -- a file a card
		// author writes -- and the worker name from the description, so nothing
		// but Field-escaped fields can come back.
		"swarm.PublicRefusalLine",
		// l.Line (nova-tools#2033) is SlotLease.Line: every field of the SLOT list
		// row -- id, owner, label, until, state, kind, weight, stranded -- goes
		// through oneline.Field (or a numeric verb) inside internal/swarm before
		// the string returns, so cmdSlotsList printing it whole cannot write past
		// the escape. TestSlotsListMarksADeadHolderStrandedWithItsLabel is the
		// behavioural test for this site.
		"l.Line",
	},
	Imports: []string{
		// the verb-help seam (the CLI style's rule (b), #4505): on -h it prints only flag names,
		// their usage literals and lines of this package's own usage const, to the stdout run
		// hands it; it never prints an argument, so nothing it writes can carry a newline in.
		`"github.com/mas-bandwidth/nova-tools/internal/nsprint/verbflag"`,
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
		// doctor.go: errors builds and inspects the read error a version run returns, and
		// io/fs supplies only the ErrNotExist sentinel that error is compared with; neither
		// holds a writer, and the words the refusal prints from them pass through oneline.
		`"errors"`, `"io/fs"`,
		// doctor.go: bytes only finds the newline that ends a stamp in the child's output
		// and holds no writer; what is kept is printed through doctorExcerpt.
		`"bytes"`,
		// native_proc_unix.go (issue #779) needs these two and neither writes a stream.
		// os/signal only routes the manager's SIGTERM into a channel the run selects on;
		// syscall only sets Setpgid -- the process-group flag that lets the deadline reap
		// the whole tree -- and holds no writer of its own.
		`"os/signal"`, `"syscall"`,
		// nativesample.go (SPEC-SWARM rule 13d, issue #1545) needs sync, and it holds no
		// writer of any kind. sync.Mutex and sync.Once are the only two things taken from
		// it: the mutex guards the figures the sampling goroutine and the launch's own
		// goroutine share, and the Once closes the stop channel and sends the one stop word
		// exactly once. Neither can write to a stream, and the sampler itself prints
		// nothing at all -- what it learns leaves it as values this package renders through
		// oneline.Field on the NATIVE OK line.
		`"sync"`,
		// member.go: bufio only scans a RESULT.md line by line and holds no writer; what
		// it reads is kept as values (the head, the one-line report) that reach a stream
		// only as arguments of nova-sprint's verbs, never as a print of this binary.
		// internal/member is the fleet member's loop; it prints only through the writer
		// this verb hands it, and every argument it prints is a card id, a count or a
		// verb name from the sprint's own JSON.
		`"bufio"`, `"github.com/mas-bandwidth/nova-tools/internal/member"`,
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
		// cardlimits holds two integer constants (the card lint's size advice and the bound
		// nova-sprint add refuses a brief over) and no code: it prints nothing and holds no
		// writer; the numbers reach a line only through numeric verbs of the size note and
		// the LINT OK and LINT SIZE lines.
		`"github.com/mas-bandwidth/nova-tools/internal/cardlimits"`,
		// safepath (issue #1923) answers ONE question about a string -- NameOK, is this a
		// name and not a path -- and returns a bool. It holds no writer of any kind and
		// prints nothing; the label it judges is rendered by this package through
		// oneline.Field in the refusal that follows.
		`"github.com/mas-bandwidth/nova-tools/internal/safepath"`,
		// nogh (nova-tools #3600) installs the refusing gh file into <slot>/shim beside the
		// shell wrappers and returns its path or an error. It prints nothing to any stream
		// of this binary: the one line it holds is the gh script's own stderr, written by
		// that script in the card's shell, never by nova-swarm.
		`"github.com/mas-bandwidth/nova-tools/internal/nogh"`,
		// cardcontract (docs/SPEC-CARD-CONTRACT.md) writes FILES -- the frame, JOB.md and the
		// profile's shims into <slot>/shim -- through atomicfile, and reads RESULT.md and
		// pushed.tsv into values. It prints nothing to any stream of this binary; the lines
		// the shims print are their own, in the card's shell, never nova-swarm's.
		`"github.com/mas-bandwidth/nova-tools/internal/cardcontract"`,
		// typedrec (the one-typed-parser rule, #2506) reads a card's RESULT.md into a value
		// and says whether a string is a commit id; it holds no writer and prints nothing.
		`"github.com/mas-bandwidth/nova-tools/internal/typedrec"`,
		// atomicfile writes one FILE whole (a temporary beside it, fsync, rename): it takes a
		// path and the bytes of the file and puts no byte on any stream of this binary. The
		// audit's Write check lets atomicfile.Write by its package name for that reason.
		`"github.com/mas-bandwidth/nova-tools/internal/atomicfile"`,
		// subproc starts one child under a deadline (Command) or a cancellable context
		// (Long) and returns the *exec.Cmd to this package, which wires the streams. It
		// holds no writer of this package's stream and prints nothing itself.
		`"github.com/mas-bandwidth/nova-tools/internal/subproc"`,
		// gitrun (memberpush.go) runs one git under its budget and returns its two streams
		// as bytes to this package; it holds no writer of this package's stream and prints
		// nothing. What the push keeps from them reaches a stream only as a member.Push
		// value the member loop folds onto one line.
		`"github.com/mas-bandwidth/nova-tools/internal/gitrun"`,
		// decide (pull --decide, SPEC-JOBS section 5) makes one typed HTTP
		// request and returns typed answers; it holds no writer of this
		// package's stream, and the one value this binary takes from it -- the
		// chosen id -- is put through oneline.Field before it is printed.
		`"github.com/mas-bandwidth/nova-tools/internal/decide"`,
		// events (nova-tools #2563) writes the card-end entry to the cards:done stream, and
		// it IS a writer of this package's stream: `native` hands events.Writer the run's
		// own stderr so a card that could not be measured says so. It cannot write past
		// the escape, and the reason is mechanical rather than a promise -- events.Writer
		// has exactly ONE print site, `note` (internal/events/writer.go), which renders
		// the whole message through oneline.Escape before writing it, so a Redis error or
		// a card label carrying a newline cannot split one skip into two lines. Nothing
		// else in that package holds a writer: Emit goes to Redis, and Validate refuses a
		// field with a control character in it before it ever reaches the store.
		`"github.com/mas-bandwidth/nova-tools/internal/events"`,
		// internal/seatcred is --seat / NOVA_SEAT (#4052): it takes the flag out
		// of the arguments and resolves the seat's Redis login for the card-end
		// writer, in memory. It writes nothing to any writer, and the one line
		// this tool prints about it goes through oneline.Escape; it never carries
		// a password.
		`"github.com/mas-bandwidth/nova-tools/internal/seatcred"`,
		// native.go (issue #296) needs these and none of them writes a stream, so
		// none can write past the escape. context only gave CommandContext its deadline
		// and holds no writer; crypto/sha256 and encoding/hex compute and hex-encode the
		// two recorded hashes (bytes in, a string out); encoding/base64 decodes the wall's
		// cwdb64 receipt and holds no writer, and the cwd it yields is put through
		// oneline.Field before this package prints it; encoding/json reads the auth file
		// and writes only dataHome/auth.json, which is the child's credential, not this
		// binary's line.
		`"context"`, `"crypto/sha256"`, `"encoding/base64"`, `"encoding/hex"`, `"encoding/json"`,
		// native.go (issue #591) needs net and net/url to read the keyless provider's
		// loopback host:port out of the carried config; neither writes a stream, so neither
		// can write past the escape: url.Parse reads the baseURL string and net.SplitHostPort
		// / net.JoinHostPort / net.ParseIP split, join and classify a host, holding no writer.
		`"net"`, `"net/url"`,
		// publish.go (slice 7) needs bytes and it writes to no stream. bytes.Buffer only
		// holds the trimmed stdout/stderr of the git and gh children it samples, and every
		// one of those strings is put through oneline.Field or oneline.Err before this
		// package prints it, so none of it can write past the escape.
		`"bytes"`,
		// strconv (native.go, slice 10) only turns the child's exit code into the one
		// column it occupies: Itoa of an int cannot hold a control character, and it
		// holds no writer of its own.
		`"strconv"`,
		// sync/atomic (native.go, issue #2632) only increments the counter that
		// distinguishes two results publishes in one process. AddUint64 returns a
		// number; the package holds no writer and prints nothing.
		`"sync/atomic"`,
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
		// route.go needs math, sort and decide and none of them writes a stream, so
		// none can write past the escape. math only rounds the complexity score
		// into the integer the routes table names; sort only orders the answer
		// names so the ROUTE line and its below list are deterministic; decide
		// POSTs the state and the four questions and returns typed answers, with
		// the key travelling only on the Authorization header and never printed.
		`"math"`, `"sort"`,
		`"github.com/mas-bandwidth/nova-tools/internal/decide"`,
		// testguard (bench.go) is the host guard: one atomic load on the way to an ssh
		// child, and nothing at all when NOVA_TEST_NO_HOST is unset, which is every
		// production run. It holds no writer and writes no stream. Its one output is a
		// PANIC under the test guard, which the runtime writes, in a test process, on a
		// path this binary never takes in production.
		`"github.com/mas-bandwidth/nova-tools/internal/testguard"`,
	},
	MinClassified: 40,
}
