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
		"main.go|foreignFlags|name":               "a flag's own name, one of the literals \"repo\", \"lane-branch\" and \"base\" in the loop above the site",
		"main.go|require|name":                    "a required flag's name, a literal at every call site in this file",
		"main.go|require|wants":                   "the sentence saying what that flag WANTS, a literal at every call site in this file",
		"pass.go|cmdRun|*loop":                    "a time.Duration this binary's own flag package parsed; its String() is digits and unit letters and holds no separator",
		"verbs.go|cmdGate|s.name":                 "a required flag's name, one of the three literals in the table declared above the site",
		"verbs.go|cmdGate|s.wants":                "the sentence saying what that flag WANTS, one of the three literals in the same table",
		"main.go|done|f.verb":                     "the verb's own name, a literal at every newFlags call site in this file",
		"verbs.go|openLane|verb":                  "the verb's own name, a literal at every call site in this file",
		"verbs.go|openLane|strings.ToUpper(verb)": "the same verb name upper-cased, a literal at every call site in this file",
		"verbs.go|nameAnEntryThisLaneDoesNotHold|strings.ToUpper(verb)": "the verb's own name upper-cased, the literal \"read\" at its one call site in this file",
		"verbs.go|nameAnUnheldObject|strings.ToUpper(verb)":             "the verb's own name upper-cased, the literals \"read\" and \"gate\" at its two call sites in this file",
		"verbs.go|nameAnUnheldObject|what":                              "the field's own name, the literals \"head\" and \"merge\" at its two call sites in this file",
		"verbs.go|foldRefused|verb":                                     "the verb's own name, the literals \"RUN\" and \"STATUS\" at its two call sites in pass.go",
		"verbs.go|cmdAdd|kind":                                          "the literals \"pr\" and \"branch\", assigned from isBranch above the site",
		"verbs.go|cmdAdd|yn":                                            "the literals \"yes\" and \"no\", assigned from --needs-read above the site",
		"verbs.go|cmdRead|current":                                      "the literals \"true\", \"false\" and \"-\", returned by standingOf in this file",
	},
	Imports: []string{
		`"crypto/sha256"`, `"encoding/hex"`, `"encoding/json"`, `"errors"`, `"flag"`, `"fmt"`,
		`"io"`, `"os"`, `"path/filepath"`, `"strconv"`, `"strings"`, `"time"`,
		`"github.com/mas-bandwidth/nova-tools/internal/bounded"`,
		`"github.com/mas-bandwidth/nova-tools/internal/merge"`,
	},
	MinClassified: 60,
}
