package main

import (
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/oneline/audit"
)

// The source-level tripwire behind the one-line guarantee: every argument this
// binary prints is quoted, numeric, literal, escaped through internal/oneline,
// or exempted here with its reason. See package audit for what the two walks
// see and what they cannot.
func TestEveryPrintedArgumentIsLiteralQuotedOrEscaped(t *testing.T) {
	audit.PrintedArguments(t, wakeAudit)
}

func TestNoOtherWriterOrShadowCanBypassTheEscape(t *testing.T) {
	audit.Bypasses(t, wakeAudit)
}

var wakeAudit = audit.Config{
	Exempt: map[string]string{},
	Imports: []string{
		`"context"`, `"flag"`, `"fmt"`, `"io"`, `"os"`, `"os/exec"`, `"strconv"`, `"strings"`, `"time"`,
		`"github.com/mas-bandwidth/nova-tools/internal/bounded"`,
		`"github.com/mas-bandwidth/nova-tools/internal/wake"`,
	},
	MinClassified: 40,
}
