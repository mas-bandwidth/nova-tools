package main

import (
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/oneline/audit"
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
		"main.go|main|msg": "messages in disallowedVerbs map are literal constants",
		"main.go|cmdVersion|buildinfo.Line(\"nova-secrets\", version)": "shared buildinfo.Line renders the complete four-field version line through oneline.Field",
		"main.go|runNamesCLI|n":           "formatted event line from internal/secrets.RunNames",
		"main.go|runNamesCLI|more":        "formatted MORE line from internal/secrets.RunNames",
		"main.go|runNamesCLI|okLine":      "formatted OK line from internal/secrets.RunNames",
		"main.go|runCheckCLI|okLine":      "formatted OK line from internal/secrets.RunCheck",
		"main.go|runCheckCLI|l":           "formatted FAIL line from internal/secrets.RunCheck",
		"main.go|runCheckCLI|m":           "formatted MORE line from internal/secrets.RunCheck",
		"main.go|runCheckCLI|summaryLine": "formatted summary line from internal/secrets.RunCheck",
		"main.go|runGateCLI|line":         "formatted GATE line from internal/secrets.RunGate",
		"main.go|runKeygenCLI|l":          "formatted receipt line from internal/secrets.RunKeygen, printed in the order that package returns them",
		"main.go|runSeatAddCLI|l":         "formatted receipt line from internal/secrets.RunSeatAdd, which never renders a value",
		"main.go|runPlaceCLI|okLine":      "formatted OK line from internal/secrets.RunPlace",
		"main.go|runPlacedCLI|okLine":     "formatted OK line from internal/secrets.RunPlaced",
		"main.go|runPlacedCLI|l":          "formatted ITEM line from internal/secrets.RunPlaced",
		"main.go|runSealCLI|line":         "formatted SEAL OK line from internal/secrets.RunSeal",
	},
	Imports: []string{
		// cliflags answers `<tool> <verb> --help`. It hands back lines of THIS
		// package's own usage constant and never the verb it was asked about,
		// and it writes only to the stream this package gives it, so nothing
		// an argument carries can reach a stream through it.
		`"github.com/mas-bandwidth/nova-tools/internal/cliflags"`,
		`"flag"`, `"fmt"`, `"io"`, `"os"`, `"strings"`,
		`"github.com/mas-bandwidth/nova-tools/internal/buildinfo"`,
		`"github.com/mas-bandwidth/nova-tools/internal/oneline"`,
		`"github.com/mas-bandwidth/nova-tools/internal/secrets"`,
	},
	MinClassified: 20,
}
