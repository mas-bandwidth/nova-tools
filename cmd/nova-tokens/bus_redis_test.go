package main

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/bus"
)

// SPEC-TOKENS rule 6: --bus reads the Redis bus's log (internal/bus, bus2:log) and counts
// exactly as the note directories were counted. The bus is faked behind the tool's world,
// so no socket is opened and no note directory exists.
func TestBusReadsTheRedisBusLog(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	out := mkdir(t, filepath.Join(dir, "out"))
	addr := busDir(t, mkdir(t, filepath.Join(dir, "bus")), "emma", "ada")
	const first = "01EMMA00000000000000000001"
	const second = "01EMMA00000000000000000002"
	busNote(t, addr, "emma", "", first, "tokens 2026-09-11", busDate, "2026-09-11\temma\tfable\tschema\tinput\t5\n")
	// A correction names the first by its ULID and replaces it.
	busNote(t, addr, "emma", "", second, "tokens 2026-09-11 at=2026-09-11T20:00:00Z build=b1 supersedes="+first, busDate,
		"2026-09-11\temma\tfable\tschema\tinput\t9\n# repos: schema\n")
	// Other traffic of the lane is opened (files=) and is not a tokens note.
	busNote(t, addr, "emma", "", "01EMMA00000000000000000003", "hello", busDate, "are you there?\n")
	// A body line that does not parse is named with its line in the BODY.
	busNote(t, addr, "ada", "", "01ADA000000000000000000001", "tokens 2026-09-11", busDate, "2026-09-11\tada\tfable\tschema\tinput\t1\nnot a line\n")

	r := invoke(t, "fold", "--out", out, "--day", "2026-09-11", "--repos", reposFile(t, dir), "--bus", addr)
	wantExit(t, r, 1)
	wantContains(t, r.all(), "TOKENS UNPARSED label=bus:ada note=01ADA000000000000000000001 line=2")
	src := lineWith(r.all(), "TOKENS SOURCE", "label=bus:emma")
	require.NotEmpty(t, src, r.all())
	for _, want := range []string{"kind=bus", "files=3", "superseded=1", "rows=1"} {
		assert.Contains(t, src, want)
	}
	wantContains(t, r.all(), "TOKENS SUPERSEDED label=bus:emma note="+first+" by="+second)
	day := read(t, filepath.Join(out, "2026-09-11.tsv"))
	row := lineWith(day, "fable\tschema")
	require.NotEmpty(t, row, day)
	assert.Contains(t, row, "\t10\t", "emma's correction (9) and ada's one good line (1), never emma's first (5)")
	assert.False(t, strings.Contains(r.all(), "participants.json"), "no roster file is read")
}

// A bus that does not answer is one unreadable source and the run says NO; it is never
// a short log.
func TestBusThatIsDownIsUnreadableNotEmpty(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	out := mkdir(t, filepath.Join(dir, "out"))
	addr := busDir(t, mkdir(t, filepath.Join(dir, "bus")), "emma")
	f, _ := testBuses.Load(addr)
	f.(*bus.Fake).Fail = errors.New("connection refused")
	r := invoke(t, "fold", "--out", out, "--day", "2026-09-11", "--repos", reposFile(t, dir), "--bus", addr)
	wantExit(t, r, 1)
	wantContains(t, r.all(), "UNREADABLE")
	wantContains(t, r.all(), "connection refused")

	// An address nothing answers at is the same.
	r = invoke(t, "fold", "--out", out, "--day", "2026-09-11", "--repos", reposFile(t, dir), "--bus", "127.0.0.1:1")
	wantExit(t, r, 1)
	wantContains(t, r.all(), "UNREADABLE")
}
