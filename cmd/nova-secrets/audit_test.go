package main

import (
	"testing"

	"github.com/mas-bandwidth/nova-tools/pkg/oneline/audit"
)

func TestEveryPrintedArgumentIsLiteralQuotedOrEscaped(t *testing.T) {
	t.Parallel()
	audit.PrintedArguments(t, secretsAudit)
}

func TestNoOtherWriterOrShadowCanBypassTheEscape(t *testing.T) {
	t.Parallel()
	audit.Bypasses(t, secretsAudit)
}

var secretsAudit = audit.Config{
	Exempt: map[string]string{
		"main.go|cmdVersion|buildinfo.Line(\"nova-secrets\", version)": "shared buildinfo.Line renders the complete four-field version line through oneline.Field",
		"names.go|runNamesCLI|n":           "formatted event line from pkg/secrets.RunNames",
		"names.go|runNamesCLI|more":        "formatted MORE line from pkg/secrets.RunNames",
		"names.go|runNamesCLI|okLine":      "formatted OK line from pkg/secrets.RunNames",
		"check.go|runCheckCLI|okLine":      "formatted OK line from pkg/secrets.RunCheck",
		"check.go|runCheckCLI|l":           "formatted FAIL line from pkg/secrets.RunCheck",
		"check.go|runCheckCLI|m":           "formatted MORE line from pkg/secrets.RunCheck",
		"check.go|runCheckCLI|summaryLine": "formatted summary line from pkg/secrets.RunCheck",
		"gate.go|runGateCLI|line":          "formatted GATE line from pkg/secrets.RunGate",
		"keygen.go|runKeygenCLI|l":         "formatted receipt line from pkg/secrets.RunKeygen, printed in the order that package returns them",
		"seat.go|runSeatAddCLI|l":          "formatted receipt line from pkg/secrets.RunSeatAdd, which never renders a value",
		"place.go|runPlaceCLI|okLine":      "formatted OK line from pkg/secrets.RunPlace",
		"main.go|runPlacedCLI|okLine":      "formatted OK line from pkg/secrets.RunPlaced",
		"main.go|runPlacedCLI|l":           "formatted ITEM line from pkg/secrets.RunPlaced",
		"seal.go|runSealCLI|line":          "formatted SEAL OK line from pkg/secrets.RunSeal",
		"seat.go|runSeatInjectCLI|line":    "formatted SEAT INJECT OK line from pkg/secrets.RunSeatInject, which never renders a value",
		// A caller's word in a refusal is free text, printed plain in quotes (oneline.Quote is
		// strconv.Quote: one line, every control, separator and bidi rune escaped, injective).
		"main.go|dispatch|oneline.Quote(verb)": "the unknown verb, quoted on one line",
		// The token of a verb's refusal is the verb's own name, a literal of this file's
		// dispatch upper-cased, never a caller's word.
		"main.go|refuse|strings.ToUpper(verb)": "the verb's name, a literal of this file's dispatch, upper-cased",
	},
	Imports: []string{
		// the verb-help seam (the CLI style's rule (b), #4505): on -h it prints only flag names,
		// their usage literals and lines of this package's own usage const, to the stdout run
		// hands it; it never prints an argument, so nothing it writes can carry a newline in.
		`"github.com/mas-bandwidth/nova-tools/pkg/nsprint/verbflag"`,
		`"flag"`, `"fmt"`, `"io"`, `"os"`, `"path/filepath"`, `"strings"`,
		// slices.Index finds exec's '--' delimiter; it writes nothing.
		`"slices"`,
		// names --json: tool.Out renders one JSON object through encoding/json, which
		// escapes every control character, so nothing it writes can break the line.
		`"github.com/mas-bandwidth/nova-tools/pkg/tool"`,
		`"github.com/mas-bandwidth/nova-tools/pkg/buildinfo"`,
		`"github.com/mas-bandwidth/nova-tools/pkg/oneline"`,
		`"github.com/mas-bandwidth/nova-tools/pkg/secrets"`,
	},
	MinClassified: 20,
}
