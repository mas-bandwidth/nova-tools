package main

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
)

// STEP 3 pins the one key both verbs use for the day they report: `day=`. fold used to
// print `date=` on its TOKENS DAY line while session printed `day=`, so the same fact had
// two names. The line the test reads is the TOKENS DAY line only; the other fold lines
// keep their own keys.
func TestFoldAndSessionPrintTheDayKey(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	repos := reposFile(t, dir)
	transcripts := mkdir(t, filepath.Join(dir, "transcripts"))
	window := msg("d1", "2026-09-11T09:12:00Z", "claude-fable-5-1",
		map[string]int{"input_tokens": 812, "output_tokens": 40}, "/work/schema/wire.md") + "\n"
	write(t, filepath.Join(transcripts, "window.jsonl"), window)

	foldOut := mkdir(t, filepath.Join(dir, "out"))
	f := invoke(t, "fold", "--out", foldOut, "--day", "2026-09-11", "--repos", repos, "--claude", "bench="+transcripts)
	wantExit(t, f, 0)
	foldDay := lineWith(f.stdout, "TOKENS DAY ")
	assert.Contains(t, foldDay, "day=2026-09-11", "fold's TOKENS DAY line carries day=")
	assert.NotContains(t, foldDay, "date=", "fold's TOKENS DAY line no longer carries date=")

	sessionOut := mkdir(t, filepath.Join(dir, "session-out"))
	session := write(t, filepath.Join(dir, "session.jsonl"), window)
	s := invoke(t, "session", "--claude-session", session, "--out", sessionOut)
	wantExit(t, s, 0)
	sessionDay := lineWith(s.stdout, "SESSION DAY ")
	assert.Contains(t, sessionDay, "day=2026-09-11", "session's SESSION DAY line carries the same day=")
}
