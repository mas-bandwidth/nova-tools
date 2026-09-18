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
	Exempt: map[string]string{
		// HereLine composes the whole WAKE HERE line itself, every field through
		// oneline.Field, from three numbers this tool read out of the kernel: a
		// load average, a CPU count and a process count. There is no text from
		// anywhere else in it -- no path, no name, no command line -- so there
		// is nothing here for an escape to do that the line's own construction
		// has not already done.
		`main.go|cmdProbe|wake.HereLine(clock.Now(), wake.ReadBench(), *quietLoad)`: "the line is composed through oneline.Field inside internal/wake and carries only numbers the kernel gave",
	},
	Imports: []string{
		// cliflags answers `<tool> <verb> --help`. It hands back lines of THIS
		// package's own usage constant and never the verb it was asked about,
		// and it writes only to the stream this package gives it, so nothing
		// an argument carries can reach a stream through it.
		`"github.com/mas-bandwidth/nova-tools/internal/cliflags"`,
		`"context"`, `"flag"`, `"fmt"`, `"io"`, `"os"`, `"os/exec"`, `"sort"`, `"strconv"`, `"strings"`, `"time"`,
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
		// os/signal and syscall, for rule 15's stop: a watch ends on the first
		// change, at its deadline, or on the CALLER'S SIGINT or SIGTERM, and
		// signal.NotifyContext is how the second of those reaches the loop and
		// cancels the gh call in flight. Both read signals and write nothing:
		// what the stop produces is one constant WAKE STOPPED line whose only
		// variable fields are a duration and two counts.
		`"os/signal"`,
		`"syscall"`,
		// path/filepath, for the awake verb's from-<name>/BEAT read, which joins a
		// lane path the same way cursorTime does. It reads a path that os.ReadFile could
		// take as an already-joined string, and writes nothing, so it is here like the
		// errors import above: a path joiner, not a source an escape can reach.
		`"path/filepath"`,
		`"github.com/mas-bandwidth/nova-tools/internal/bounded"`,
		// internal/dispatch is the queued|dispatching|delivered|uncertain file
		// discipline over internal/wake's State. It writes state records and
		// returns strings/ints the caller prints through oneline.Field; it
		// prints nothing and shadows nothing.
		`"github.com/mas-bandwidth/nova-tools/internal/dispatch"`,
		`"github.com/mas-bandwidth/nova-tools/internal/buildinfo"`,
		`"github.com/mas-bandwidth/nova-tools/internal/wake"`,
	},
	MinClassified: 40,
}
