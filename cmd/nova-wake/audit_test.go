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
		// runtime, for the version verb's os/arch/toolchain, which are
		// constants of the build and are printed through oneline.Field like
		// everything else: it writes nothing and shadows nothing.
		`"runtime"`,
		// runtime/debug, for the build stamp the version verb prints and the
		// nova-bus pin is derived from (cmd/nova-wake/version.go). It reads
		// build information, writes nothing and shadows nothing, and what it
		// returns is printed through oneline.Field like everything else.
		`"runtime/debug"`,
		// errors, for errors.Is over internal/wake's ErrRecoveryPending: a held
		// advance is the tool obeying step 4 and not a source that failed, so
		// the loop tells the two apart by sentinel rather than by a string
		// (#164, F1, 2026-09-12). It compares errors, writes nothing and
		// shadows nothing; the sentence the caller prints is a constant, printed
		// through w.note like every other WAKE NOTE.
		`"errors"`,
		`"github.com/mas-bandwidth/nova-tools/internal/bounded"`,
		`"github.com/mas-bandwidth/nova-tools/internal/buildinfo"`,
		`"github.com/mas-bandwidth/nova-tools/internal/wake"`,
	},
	MinClassified: 40,
}
