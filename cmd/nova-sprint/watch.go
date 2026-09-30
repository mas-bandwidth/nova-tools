package main

import (
	"context"
	"io"
	"strings"
	"time"
)

// where --watch: the view drawn in place, once every --every. The cursor is
// hidden while it watches and comes back when it ends, by the end of the
// watch or an interrupt. Each frame is built whole in memory and written with
// one write, so the screen never shows half of one; the cursor goes home,
// every line is cleared to its end as it is written, and everything below the
// frame is cleared after it, so a frame shorter than the last leaves nothing
// behind. Nothing scrolls: no newline follows the last line.

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

// watchWriter draws the frames of a watch in place on w.
type watchWriter struct{ w io.Writer }

// newWatchWriter is a watchWriter on w.
func newWatchWriter(w io.Writer) *watchWriter { return &watchWriter{w: w} }

// hideCursor hides the cursor, in a write of its own.
func (d *watchWriter) hideCursor() { _, _ = io.WriteString(d.w, cursorHide) }

// showCursor restores the cursor, in a write of its own.
func (d *watchWriter) showCursor() { _, _ = io.WriteString(d.w, cursorShow) }

// frame draws text, the lines of one frame, in place: one write.
func (d *watchWriter) frame(text string) error {
	_, err := io.WriteString(d.w, watchFrame(text))
	return err
}

// watchFrame is the bytes of the frame that draws text: the cursor home, each
// line cleared to its end, no newline after the last line (a frame as tall as
// the screen must not scroll it), then everything below cleared.
func watchFrame(text string) string {
	lines := strings.Split(strings.TrimSuffix(text, "\n"), "\n")
	var b strings.Builder
	b.WriteString(cursorHome)
	for i, l := range lines {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(l)
		b.WriteString(clearLine)
	}
	b.WriteString(clearBelow)
	return b.String()
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
