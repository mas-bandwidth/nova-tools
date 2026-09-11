package main

import (
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/oneline/audit"
)

// The source-level tripwire behind the one-line guarantee: every argument this binary
// prints is quoted, numeric, literal, escaped through internal/oneline, or exempted here
// with its reason. See package audit for what the two walks see and what they cannot.
func TestEveryPrintedArgumentIsLiteralQuotedOrEscaped(t *testing.T) {
	audit.PrintedArguments(t, boardAudit)
}

func TestNoOtherWriterOrShadowCanBypassTheEscape(t *testing.T) {
	audit.Bypasses(t, boardAudit)
}

var boardAudit = audit.Config{
	// oneline.Quote is the third rendering in that package: one line, and PASTEABLE. It is
	// what quickstart's two shell lines go through, because Field would render a space as
	// \x20 and nobody could paste the result — which is the bug `nova-bus names` was
	// written around.
	Escapers: []string{"oneline.Quote"},
	Exempt: map[string]string{
		"main.go|capped|remedy": "the remedy a listing's MORE line carries: a fmt.Sprintf over a literal and an int, built at this file's two call sites and never caller text; internal/bounded also puts it through oneline.Escape before writing it",
	},
	Imports: []string{
		// path/filepath joins the board directory and a card's file name for the ADD NOTE
		// that says what a --dir add left unlanded. It builds a string and writes nothing:
		// the joined path goes out through oneline.Field like every other value here.
		//
		// runtime and runtime/debug are version.go, and neither can write past the escape:
		// runtime answers GOOS, GOARCH and Version and holds no writer at all;
		// runtime/debug is read once for this binary's own build information and every
		// field of it goes out through oneline.Field.
		`"crypto/rand"`, `"flag"`, `"fmt"`, `"io"`, `"os"`, `"path/filepath"`, `"runtime"`, `"runtime/debug"`, `"strings"`, `"time"`,
		`"github.com/mas-bandwidth/nova-tools/internal/board"`,
		`"github.com/mas-bandwidth/nova-tools/internal/bounded"`,
	},
	MinClassified: 40,
}
