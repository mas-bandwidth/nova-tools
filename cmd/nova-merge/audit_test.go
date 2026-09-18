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
	audit.PrintedArguments(t, mergeAudit)
}

func TestNoOtherWriterOrShadowCanBypassTheEscape(t *testing.T) {
	t.Parallel()
	audit.Bypasses(t, mergeAudit)
}

var mergeAudit = audit.Config{
	// One entry per site, keyed by file, function and source text; each is a claim a
	// reader can check.
	Exempt: map[string]string{
		"main.go|foreignFlags|name":                                     "a flag's own name, one of the literals \"repo\", \"lane-branch\" and \"base\" in the loop above the site",
		"main.go|require|name":                                          "a required flag's name, a literal at every call site in this file",
		"main.go|require|wants":                                         "the sentence saying what that flag WANTS, a literal at every call site in this file",
		"pass.go|cmdRun|*loop":                                          "a time.Duration this binary's own flag package parsed; its String() is digits and unit letters and holds no separator",
		"verbs.go|cmdGate|s.name":                                       "a required flag's name, one of the three literals in the table declared above the site",
		"verbs.go|cmdGate|s.wants":                                      "the sentence saying what that flag WANTS, one of the three literals in the same table",
		"main.go|done|f.verb":                                           "the verb's own name, a literal at every newFlags call site in this file",
		"verbs.go|cmdInit|c.name":                                       "a flag's own name, one of the two literals \"base\" and \"lane-branch\" in the table above the site",
		"pass.go|cmdRun|strconv.FormatFloat(*hours, 'g', -1, 64)":       "a float this binary's own flag package parsed, rendered as digits, a dot and an exponent letter",
		"verbs.go|openLane|verb":                                        "the verb's own name, a literal at every call site in this file",
		"verbs.go|openLane|strings.ToUpper(verb)":                       "the same verb name upper-cased, a literal at every call site in this file",
		"verbs.go|nameAnEntryThisLaneDoesNotHold|strings.ToUpper(verb)": "the verb's own name upper-cased, the literal \"read\" at its one call site in this file",
		"verbs.go|nameAnUnheldObject|strings.ToUpper(verb)":             "the verb's own name upper-cased, the literals \"read\" and \"gate\" at its two call sites in this file",
		"verbs.go|nameAnUnheldObject|what":                              "the field's own name, the literals \"head\" and \"merge\" at its two call sites in this file",
		"verbs.go|foldRefused|verb":                                     "the verb's own name, the literals \"RUN\" and \"STATUS\" at its two call sites in pass.go",
		"verbs.go|stateWriteRefused|verb":                               "the verb's own name, the literal \"RUN\" at its one call site in pass.go",
		"verbs.go|cmdAdd|kind":                                          "the literals \"pr\" and \"branch\", assigned from isBranch above the site",
		"verbs.go|cmdAdd|yn":                                            "the literals \"yes\" and \"no\", assigned from --needs-read above the site",
		"verbs.go|cmdRead|current":                                      "the literals \"true\", \"false\" and \"-\", returned by standingOf in this file",
		"pass.go|discoverDefault|verb":                                  "the verb's own name, the literals \"RUN\" and \"STATUS\" at its three call sites in pass.go",
		"classify.go|classifyPacketEntry|line":                          "the decision renderer decide.Line has already rendered every name and value through oneline.Field; escaping it again would turn its \\x3d escapes into literal backslashes",
		"classify.go|classifyPacketEntry|mergePacketEvidence(e)":        "pr#<n> is digits, and branch#<name> renders the name through oneline.Field inside mergePacketEvidence",
		// The two batch sites are the shape this walk cannot see: a value built above the
		// print site. Each has a behavioral test of its own in batch_test.go.
		"batch.go|runBatch|batchLine(in, baseSHA, headSHA, members, dropped, append(skipped, step.name))": "the same six fields as `line` below, rendered through oneline.Field inside batchLine, built at the --require-lisp refusal with the step that could not run appended to the skipped list; TestBatchRequireLispFailsWhenTheStepCannotRun asserts the whole line",
		"batch.go|runBatch|batchLine(in, baseSHA, headSHA, members, dropped, nil)":                        "the same six fields again, rendered through oneline.Field inside batchLine, built at the empty-batch refusal with an empty skipped list because no step ran; TestBatchWithEveryMemberDroppedFailsBeforeTheSuiteRuns asserts the line",
		"queueaudit.go|cmdQueueAudit|mode": "one of the two literals \"dry-run\" and \"apply\", assigned from --apply in the three lines immediately above the site",
		"batch.go|runBatch|line":           "the six fields shared by BATCH OK and BATCH FAIL, each rendered through oneline.Field inside batchLine; TestBatchOKNamesTheBaseTheHeadAndTheDroppedMember asserts that whole line byte for byte",
		"batch.go|mergeMembers|in.name":    "the batch's own --name, inside a COMMIT MESSAGE rather than a line of the grammar, and held to safepath.NameOK at the flag site: letters, digits, dot, dash and underscore, which TestBatchRefusesANameThatIsNotOnePathElement pins",
	},
	// buildinfo.Line is the `version` verb's whole line and the fifth escaper: it renders
	// every one of its four fields through oneline.Field inside internal/buildinfo, where
	// TestLineShape and TestLineHoldsWhateverTheStampContains pin it -- including against
	// a release stamp holding a newline, which is the one field of that line that comes
	// from outside the toolchain.
	//
	// classifyLine is `classify`'s whole line: every value from outside -- the kind,
	// rerun, park and the raw answer -- is rendered through oneline.Field inside it, and
	// the rest are the numeric run id, pull request, confidence and floor.
	Escapers: []string{"buildinfo.Line", "classifyLine"},
	Imports: []string{
		// version.go, and the reason it cannot write past the escape: buildinfo reads
		// debug.ReadBuildInfo and runtime's GOOS, GOARCH and Version, holds no writer of
		// its own, and returns a STRING that this package prints -- rendered field by
		// field through oneline.Field before it is returned.
		`"github.com/mas-bandwidth/nova-tools/internal/buildinfo"`,
		`"crypto/sha256"`, `"encoding/hex"`, `"encoding/json"`, `"errors"`, `"flag"`, `"fmt"`,
		`"io"`, `"os"`, `"path/filepath"`, `"strconv"`, `"strings"`, `"time"`,
		// context, os/exec and syscall are simulate's: it runs each check as a child
		// under a deadline, in its own process group, and none of them writes to a
		// stream this package prints -- the child's output is captured and rendered
		// through oneline.Escape before it reaches a line.
		`"context"`, `"os/exec"`, `"syscall"`,
		// net/url holds no writer of its own: Parse and String are pure string transforms,
		// and the one use here strips a URL's userinfo so a token is never printed; the
		// result is rendered through oneline.Field at its print site.
		`"net/url"`,
		`"github.com/mas-bandwidth/nova-tools/internal/bounded"`,
		// goenv holds no writer of its own: Clean is a pure transform over a slice of
		// environment strings -- it drops GOFLAGS and the rest of its documented list
		// and returns what is left -- and it prints nothing. simulate hands its result
		// to each check's Env so that a caller's GOFLAGS cannot reshape the output
		// SIMULATE POISON quotes.
		`"github.com/mas-bandwidth/nova-tools/internal/goenv"`,
		`"github.com/mas-bandwidth/nova-tools/internal/merge"`,
		// safepath holds no writer of its own: RemoveUnder only decides whether a path
		// may be removed and returns an os error, which every caller renders through
		// oneline.Escape or oneline.Err before printing. It cannot write past the
		// escape.
		// safepath removes the scratch worktree this verb computed; it holds no writer.
		`"github.com/mas-bandwidth/nova-tools/internal/safepath"`,
		// context carries no writer: it is the deadline the typed-decision call runs under
		// (context.Background in classify.go, and decide.Client applies its own 10 s
		// timeout), the deadline simulate runs each check under, and react.go's CancelFunc
		// plumbing. It prints nothing. It is already named with simulate's imports above.
		//
		// internal/decide is the typed-decision route, used by both classifications here:
		// it builds one JSON request and parses the answers, and it never prints. Its one
		// line comes back through decide.Line, whose every field is escaped, and
		// classify_run.go's own line through classifyLine, an escaper above.
		`"github.com/mas-bandwidth/nova-tools/internal/decide"`,
		// bytes is a Buffer and not a stream: CutKind's one line is captured into it and
		// parsed for the card's name, never handed to stdout as it stands.
		`"bytes"`,
		// pulse's cut kind writes a card FILE and prints its one line into the Buffer
		// above; its stderr is this binary's own stderr, so no line of its reaches the
		// one line this verb prints.
		`"github.com/mas-bandwidth/nova-tools/internal/pulse"`,
		// react.go's redis client edge writes nothing itself; it is read through its own
		// API and this package prints only the escaped lines below, so it cannot write
		// past oneline.
		`"github.com/redis/go-redis/v9"`,
		// react.go publishes and reads through internal/ci, whose values are rendered
		// through oneline before this package prints them.
		`"github.com/mas-bandwidth/nova-tools/internal/ci"`,
		// internal/ci/slowtests is batch's reader of a `go test -json` stream, and it is
		// THE SAME DECODER cmd/nova-ci reads CI's own stream with. It holds no writer:
		// Parse decodes newline-delimited JSON into structs and returns them, and the
		// package and test names it returns reach a line through oneline.Field.
		`"github.com/mas-bandwidth/nova-tools/internal/ci/slowtests"`,
		// regexp holds no writer of its own: batch.go uses it to read a go.mod's `go`
		// directive, the version `go version` printed, and the `go: downloading ...`
		// notices it drops off the front of a failing step's output. Match and
		// FindStringSubmatch are pure reads that return strings, and every one of them
		// reaches a line through oneline.Escape or oneline.Field.
		`"regexp"`,
		// sort holds no writer of its own: queue.go uses it to put `queue status`'s
		// skipped set in one deterministic order, so two reads of one file print the
		// same lines. It returns nothing and prints nothing.
		`"sort"`,
	},
	MinClassified: 60,
}
