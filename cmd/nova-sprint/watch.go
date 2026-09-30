package main

import (
	"context"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/tty"
)

// where --watch: the view drawn in place, once every --every. The cursor is
// hidden while it watches and comes back when it ends, by the end of the
// watch or an interrupt. Each frame is built whole in memory and written with
// one write, so the screen never shows half of one; the cursor goes home,
// every line is cleared to its end as it is written, and everything below the
// frame is cleared after it, so a frame shorter than the last leaves nothing
// behind. Nothing scrolls, at any size of the screen: no newline follows the
// last line, a frame taller than the screen is cut at the bottom (nothing is
// added to say so), and a line is cut to one column less than the screen is
// wide, so that its last character never waits at the edge to wrap. Where the
// screen's size is not known (the output is not a terminal) the frame is
// written whole.

// The terminal sequences a frame is made of.
const (
	cursorHome = "\x1b[H"
	clearLine  = "\x1b[K" // to the end of the line
	clearBelow = "\x1b[J" // from the cursor to the end of the screen
	cursorHide = "\x1b[?25l"
	cursorShow = "\x1b[?25h"
)

// pauseStep is the longest the watch sleeps at once, so an interrupt is seen
// within it.
const pauseStep = 100 * time.Millisecond

// watchWriter draws the frames of a watch in place on w. size is the rows and
// columns of the screen w is, read for every frame (a terminal is resized),
// each 0 when not known.
type watchWriter struct {
	w    io.Writer
	size func() (rows, cols int)
}

// newWatchWriter is a watchWriter on w, whose screen size is read by size.
func newWatchWriter(w io.Writer, size func() (rows, cols int)) *watchWriter {
	return &watchWriter{w: w, size: size}
}

// hideCursor hides the cursor, in a write of its own.
// ignored: terminal cursor escape write failure is non-fatal
func (d *watchWriter) hideCursor() { _, _ = io.WriteString(d.w, cursorHide) }

// showCursor restores the cursor, in a write of its own.
// ignored: terminal cursor escape write failure is non-fatal
func (d *watchWriter) showCursor() { _, _ = io.WriteString(d.w, cursorShow) }

// frame draws text, the lines of one frame, in place: one write, whatever its
// size.
func (d *watchWriter) frame(text string) error {
	rows, cols := d.size()
	_, err := io.WriteString(d.w, watchFrame(text, rows, cols))
	return err
}

// watchFrame is the bytes of the frame that draws text on a screen of rows by
// cols (each 0 when not known): the cursor home, each line cleared to its end,
// no newline after the last line (a frame as tall as the screen must not
// scroll it), then everything below cleared. A frame taller than rows is cut
// at the bottom, and a line is cut to cols-1 columns (a column is a rune).
func watchFrame(text string, rows, cols int) string {
	lines := strings.Split(strings.TrimSuffix(text, "\n"), "\n")
	if rows > 0 && len(lines) > rows {
		lines = lines[:rows]
	}
	var b strings.Builder
	b.WriteString(cursorHome)
	for i, l := range lines {
		if i > 0 {
			b.WriteByte('\n')
		}
		if cols > 0 {
			l = clip(l, cols-1)
		}
		b.WriteString(l)
		b.WriteString(clearLine)
	}
	b.WriteString(clearBelow)
	return b.String()
}

// clip is line cut to its first n runes, on a rune boundary.
func clip(line string, n int) string {
	if len(line) <= n {
		return line // n bytes hold at most n runes
	}
	kept := 0
	for i := range line {
		if kept == n {
			return line[:i]
		}
		kept++
	}
	return line
}

// interruptContext is ctx, done at an interrupt or a termination.
func interruptContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
}

// screenSize is the rows and columns of the screen w draws on, each 0 when not
// known: w is not a terminal.
func screenSize(w io.Writer) (rows, cols int) {
	if f, ok := w.(*os.File); ok {
		return tty.Size(f)
	}
	return 0, 0
}

// pause sleeps d in steps of at most pauseStep and says whether the watch goes
// on: false once ctx is done.
func (a *app) pause(ctx context.Context, d time.Duration) bool {
	for d > 0 && ctx.Err() == nil {
		step := min(d, pauseStep)
		a.sleep(step)
		d -= step
	}
	return ctx.Err() == nil
}
