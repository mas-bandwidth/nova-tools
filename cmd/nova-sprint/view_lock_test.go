package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// THE CLASS RULE: THE SPRINT VIEW IS LOCKED (SPEC-CI, `sprint-view-locked`).
//
// Glenn 2026-10-01: "strike everything that shows to my sprint table" and "This drift
// (visually) and logically away from the design is a real problem." internal/sprint/VIEW.lock
// holds, as plain text a reader sees in a diff, the frame `nova-sprint where` prints for
// whereFixture and every write `where --watch` makes for it, control bytes escaped. This test
// renders both from the real renderer (a.where and whereLoop, which live in package main,
// so the test lives here and internal/ci cannot hold it) and is red on any difference.
//
// It is not a golden: nothing regenerates the lock, and no flag or environment variable
// writes it. A PR that changes what the view shows changes VIEW.lock by hand in the same PR,
// where a read sees it.

// lockPiece is one piece of text between newlines as the lock writes it: a backslash is
// `\\`, the escape byte `\e`, any other control byte `\xNN`, and a `$` ends the piece, so
// trailing spaces and blank lines are visible. A final `$` alone means the text ends in a
// newline.
func lockPiece(s string) string {
	var b strings.Builder
	for _, c := range []byte(s) {
		switch {
		case c == '\\':
			b.WriteString(`\\`)
		case c == 0x1b:
			b.WriteString(`\e`)
		case c < 0x20 || c == 0x7f:
			fmt.Fprintf(&b, `\x%02x`, c)
		default:
			b.WriteByte(c)
		}
	}
	return b.String() + "$\n"
}

// lockSection is one named piece of output in the lock's form.
func lockSection(name, text string) string {
	var b strings.Builder
	b.WriteString("== " + name + "\n")
	for _, p := range strings.Split(text, "\n") {
		b.WriteString(lockPiece(p))
	}
	return b.String()
}

// renderViewLock is the lock's body as the real renderer prints it now: the frame of where,
// then each write of where --watch for the same store.
func renderViewLock(t *testing.T) string {
	t.Helper()
	ta := whereFixture(t)
	plain := ta.ok("where")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	screen := &writeLog{after: func(n int, _ string) {
		if n == 2 { // the cursor hidden, then the frame
			cancel()
		}
	}}
	var errb strings.Builder
	code := ta.a.whereLoop(ctx, ta.whereRun(true), screen, &errb)
	require.Equal(t, 0, code, errb.String())
	require.Empty(t, errb.String())
	require.Len(t, screen.writes, 3, "the watch writes: the cursor hidden, the frame, the cursor shown")

	return lockSection("where", plain) +
		lockSection("where --watch, write 1: the cursor hidden", screen.writes[0]) +
		lockSection("where --watch, write 2: the frame", screen.writes[1]) +
		lockSection("where --watch, write 3: the cursor shown", screen.writes[2])
}

// lockBody is the lock file's text after its header: from the first `== ` line on.
func lockBody(raw string) string {
	if i := strings.Index("\n"+raw, "\n== "); i >= 0 {
		return raw[i:]
	}
	return ""
}

func TestSprintViewIsLocked(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile(filepath.Join("..", "..", "internal", "sprint", "VIEW.lock"))
	require.NoError(t, err, "the lock file internal/sprint/VIEW.lock is missing")
	want, got := lockBody(string(raw)), renderViewLock(t)
	if want == got {
		return
	}
	wl, gl := strings.Split(want, "\n"), strings.Split(got, "\n")
	for i := 0; i < len(wl) || i < len(gl); i++ {
		var w, g string
		if i < len(wl) {
			w = wl[i]
		}
		if i < len(gl) {
			g = gl[i]
		}
		if w != g {
			assert.Fail(t, "the sprint view is locked", "Glenn 2026-10-01: the design is complete and locked. "+
				"what nova-sprint where shows no longer matches internal/sprint/VIEW.lock; a PR that changes "+
				"the view changes the lock file by hand in the same PR, where a read sees it.\n"+
				"first difference at line %d of the lock body:\n  lock has:  %q\n  view has:  %q", i+1, w, g)
			return
		}
	}
}
