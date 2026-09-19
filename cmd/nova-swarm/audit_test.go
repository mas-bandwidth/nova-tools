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
		"main.go|parse|f.verb":                        "the verb's own name, a literal at every newFlags call site in this file",
		"main.go|want|name":                           "a required flag's name, a literal at every call site in this file",
		"main.go|want|wants":                          "the guidance that flag wants, a literal at every call site in this file",
		"main.go|wantCount|name":                      "a required count flag's name, a literal at every call site in this file",
		"main.go|wantCount|wants":                     "the guidance that count flag wants, a literal at every call site in this file",
		"main.go|refused|f.verb":                      "the verb's own name, the value newFlags stored from that literal",
		"main.go|openPool|verb":                       "the verb's own name, a literal at every call site in this file",
		"main.go|slotWord|sc.Slot":                    "an int from the sidecar; fmt.Sprint of an int cannot hold a control character",
		"main.go|cmdTemplate|body":                    "the named verbatim site: a template is a DOCUMENT a person redirects into a file, not an event line, so escaping it would fold it into one unusable line. Every byte of it is an embedded constant in package swarm. TestTemplatesCarryTheirConditions is the behavioural test for this site.",
		"main.go|cmdFinalize|line":                    "the other verbatim site, `finalize`'s line: swarm.FinalizeByHand BUILDS the whole sentence and passes every caller-supplied value through oneline.Field or oneline.Err, so the one-line guarantee is already made over the finished line. Escaping it a second time here would fold that line into one unreadable \\x0a form. Both print sites in this function share this entry; TestADispatcherRunsAJobEndToEnd exercises the path.",
		"main.go|cmdNative|swarm.NoSlotsStoreRefusal": "a compile-time constant in package swarm (internal/swarm/slots.go): the ONE remedy line a native launch with no bench slot store prints, held in one place so that native, batch and the two native-argv builders cannot drift apart. It holds no caller-supplied value at all -- there is nothing in it to escape, and escaping a constant would only hide that fact. TestNativeWithoutASlotsStoreRefuses compares it byte for byte and asserts it is one line.",
		"route.go|cmdRoute|belowWord":                 "built in this function from oneline.Field-escaped answer names joined with a literal comma, so it is already one safe token; the ROUTE print site's other caller-supplied values go through oneline.Field or a numeric verb on the same line. TestRouteBelowFloorExits3 is the behavioural test for this site.",
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
		// swarm.WallLine (issue #644's follow-up) builds the `WALL task=<id> path=<p>
		// step=<n> [commits=<n> branch=<name>]` report line and puts every field through
		// oneline.Field inside itself. The path and step come from the card's own log and
		// the branch from the clone, so nothing but escaped fields can come back.
		"swarm.WallLine",
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
		// redisq (slice 1 of SPEC-STATE) reads the Redis Streams pull queue, the fenced
		// slot lease and the in-flight cap. It holds no writer of its own: every call
		// either returns a value this package prints through oneline.Field or an error
		// this package renders through oneline.Err, and the Lua scripts run inside Redis
		// and write only that instance's own keys.
		`"github.com/mas-bandwidth/nova-tools/internal/redisq"`,
		// decide (pull --decide, SPEC-JOBS section 5) makes one typed HTTP
		// request and returns typed answers; it holds no writer of this
		// package's stream, and the one value this binary takes from it -- the
		// chosen id -- is put through oneline.Field before it is printed.
		`"github.com/mas-bandwidth/nova-tools/internal/decide"`,
		// lanes (pull, SPEC-JOBS section 5) reads queue/lanes/ and writes the
		// card files it places; it never writes to a stream, and every id it
		// returns is put through oneline.Field before this package prints it.
		`"github.com/mas-bandwidth/nova-tools/internal/lanes"`,
		// native.go (issue #296) needs these and none of them writes a stream, so
		// none can write past the escape. context only gave CommandContext its deadline
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
