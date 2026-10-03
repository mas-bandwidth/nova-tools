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

	audit.PrintedArguments(t, selfTalkAudit)
}

func TestNoOtherWriterOrShadowCanBypassTheEscape(t *testing.T) {
	t.Parallel()

	audit.Bypasses(t, selfTalkAudit)
}

var selfTalkAudit = audit.Config{
	// The skeleton renders every line. This package builds a tool.Out and does not
	// print one, so the walk classifies no printed argument and the floor is zero.
	Imports: []string{
		`"errors"`, `"flag"`, `"fmt"`, `"io"`, `"io/fs"`, `"os"`, `"strings"`,
		`"bytes"`, `"embed"`, `"path"`, `"path/filepath"`,
		`"github.com/mas-bandwidth/nova-tools/internal/oneline"`,
		`"github.com/mas-bandwidth/nova-tools/internal/selftalk"`,
		`"github.com/mas-bandwidth/nova-tools/internal/tool"`,
	},
}
