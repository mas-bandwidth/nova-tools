package main

import (
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/oneline/audit"
)

func TestEveryPrintedArgumentIsLiteralQuotedOrEscaped(t *testing.T) {
	audit.PrintedArguments(t, secretsAudit)
}

func TestNoOtherWriterOrShadowCanBypassTheEscape(t *testing.T) {
	audit.Bypasses(t, secretsAudit)
}

var secretsAudit = audit.Config{
	Exempt: map[string]string{
		"main.go|main|msg":                "messages in disallowedVerbs map are literal constants",
		"main.go|runNamesCLI|n":           "formatted event line from internal/secrets.RunNames",
		"main.go|runNamesCLI|more":        "formatted MORE line from internal/secrets.RunNames",
		"main.go|runNamesCLI|okLine":      "formatted OK line from internal/secrets.RunNames",
		"main.go|runCheckCLI|okLine":      "formatted OK line from internal/secrets.RunCheck",
		"main.go|runCheckCLI|l":           "formatted FAIL line from internal/secrets.RunCheck",
		"main.go|runCheckCLI|m":           "formatted MORE line from internal/secrets.RunCheck",
		"main.go|runCheckCLI|summaryLine": "formatted summary line from internal/secrets.RunCheck",
		"main.go|runKeygenCLI|okLine":     "formatted OK line from internal/secrets.RunKeygen",
		"main.go|runKeygenCLI|l":          "formatted RULE line from internal/secrets.RunKeygen",
		"main.go|runKeygenCLI|noteLine":   "formatted NOTE line from internal/secrets.RunKeygen",
	},
	Imports: []string{
		`"flag"`, `"fmt"`, `"io"`, `"os"`, `"strings"`,
		`"github.com/mas-bandwidth/nova-tools/internal/oneline"`,
		`"github.com/mas-bandwidth/nova-tools/internal/secrets"`,
	},
	MinClassified: 20,
}
