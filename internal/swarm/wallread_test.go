package swarm

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// THE LOG THAT COST A CARD, in its own shape. `js-under-20-bytes` (2026-09-19, rc=-1,
// wall=1200.04s, no RESULT.md) carried ONE `Operation not permitted`, on line 5 of its
// harness capture -- the harness's own startup banner, printed before STEP 1 -- and then
// worked for sixteen more model steps before dying in a provider turn that never answered.
// The post-mortem scan reported `WALL task=js-under-20-bytes path=/var/db/xcode_select_link`
// and the shift went looking at the wall.
const bannerRefusalLog = "\n> build · deepseek-v4-pro\n" +
	"xcode-select: error: unable to read data link at '/var/db/xcode_select_link', expected symbolic link (Operation not permitted)\n" +
	"# Todos\nSTEP 1: Confirm workspace\n" +
	"$ cd repo && grep -n needHome internal/codegen/jstable/fixedmodule.go\n" +
	"5:// runtime and every type's write/decode helper land\n"

// TestWallStoppedIgnoresARefusalTheCardMovedPast: a refusal with another tool call after it
// is one the model routed around. RED WITHOUT THE FIX: WallRefused takes the first refusal
// in the file whatever the card did next, so the banner line was named as the death.
func TestWallStoppedIgnoresARefusalTheCardMovedPast(t *testing.T) {
	t.Parallel()

	_, ok := WallRefused([]byte(bannerRefusalLog))
	require.True(t, ok, "the fixture must hold a refusal WallRefused finds, or this test proves nothing")
	w, ok := WallStopped([]byte(bannerRefusalLog))
	require.False(t, ok, "a refusal the card made sixteen more steps past is not what stopped it, got %+v", w)
}

// TestWallStoppedNamesARefusalWithNothingAfterIt: the other half. A refusal the card never
// got past is still a wall death, and the classification must survive the fix.
func TestWallStoppedNamesARefusalWithNothingAfterIt(t *testing.T) {
	t.Parallel()

	log := "STEP 2: build\n$ cc -o probe probe.c\n" +
		"cc: error: unable to read data link at '/var/db/xcode_select_link' (Operation not permitted)\n"
	w, ok := WallStopped([]byte(log))
	require.True(t, ok, "a refusal with no tool call after it is what stopped the card")
	require.Equal(t, "/var/db/xcode_select_link", w.Path, "the refusal names the path and the step it reached, got %+v", w)
	require.Equal(t, "2", w.Step, "the refusal names the path and the step it reached, got %+v", w)
}

// TestPermissionDeniedOnAPathIsARefusal: measured inside the swarm's own wall on hulk
// (landlock abi 4, 2026-09-19) -- `sh: 1: cannot create /tmp/nova-wall-probe-swarm:
// Permission denied`. RED WITHOUT THE FIX: this package knew only `SANDBOX REFUSED` and
// `Operation not permitted`, so no linux wall refusal was classified at all.
func TestPermissionDeniedOnAPathIsARefusal(t *testing.T) {
	t.Parallel()

	log := "STEP 1\n$ sh -c 'echo probe > /tmp/nova-wall-probe-swarm'\n" +
		"sh: 1: cannot create /tmp/nova-wall-probe-swarm: Permission denied\n"
	w, ok := WallStopped([]byte(log))
	require.True(t, ok, "landlock refuses with EACCES, which the C library spells `Permission denied`")
	require.Equal(t, "/tmp/nova-wall-probe-swarm", w.Path, "the refused path is named, got %q", w.Path)
	got := WallKind("sh: 1: cannot create /tmp/x: Permission denied")
	require.Equal(t, "write", got, "a refused create is a write, got %q", got)
	// A bare permission failure about something that is not a path is not the wall.
	_, ok = WallStopped([]byte("kill: Operation not permitted\n"))
	require.False(t, ok, "a refusal that names no path is not the wall refusing a read or a write")
}

// TestWallReaderAnnouncesTheRefusalAsItArrives: the point of the reader. RED WITHOUT THE
// FIX: nothing read the child's output until the child was gone, so the coordinator learnt
// of a refusal at the reap -- twenty minutes later on the card that died of one.
func TestWallReaderAnnouncesTheRefusalAsItArrives(t *testing.T) {
	t.Parallel()

	var said []string
	r := NewWallReader("card-8311", func(line string) { said = append(said, line) })
	_, err := r.Write([]byte("STEP 2: write the file\n"))
	require.NoError(t, err)
	require.Empty(t, said, "nothing is announced before a refusal, got %v", said)
	_, err = r.Write([]byte("sh: 1: cannot create /etc/hosts: Permission denied\n"))
	require.NoError(t, err)
	want := "WALL REFUSED write /etc/hosts task=card-8311 step=2"
	require.Len(t, said, 1, "the typed line is announced once, as it arrives:\nwant %q\ngot  %v", want, said)
	require.Equal(t, want, said[0], "the typed line is announced once, as it arrives:\nwant %q\ngot  %v", want, said)
	// A SECOND refusal is not a second announcement: one line per card, the first one.
	_, err = r.Write([]byte("sh: 1: cannot create /etc/passwd: Permission denied\n"))
	require.NoError(t, err)
	require.Len(t, said, 1, "one announcement per card, got %v", said)
}

// TestWallReaderHoldsAPartialLine: the child's stdout and stderr arrive in whatever pieces
// the pipe hands over, and a refusal split across two writes is still a refusal.
func TestWallReaderHoldsAPartialLine(t *testing.T) {
	t.Parallel()

	var said []string
	r := NewWallReader("c", func(line string) { said = append(said, line) })
	_, _ = r.Write([]byte("sh: 1: cannot create /etc/ho"))
	require.Empty(t, said, "a line with no newline yet is not a line, got %v", said)
	_, _ = r.Write([]byte("sts: Permission denied\n"))
	require.Len(t, said, 1, "the two halves are one refusal, got %v", said)
	require.Contains(t, said[0], "/etc/hosts", "the two halves are one refusal, got %v", said)
}

// TestWallReaderForgetsARefusalTheCardMovedPast: the live half of WallStopped. A reader
// that saw a refusal and then a tool call reports no refusal at all, so a card ended for
// stillness is not reported as a wall death it had already worked past.
func TestWallReaderForgetsARefusalTheCardMovedPast(t *testing.T) {
	t.Parallel()

	r := NewWallReader("c", nil)
	_, _ = r.Write([]byte("xcode-select: error: unable to read data link at '/var/db/xcode_select_link' (Operation not permitted)\n"))
	_, _, ok := r.Stopped()
	require.True(t, ok, "a refusal with nothing after it is the refusal that stopped the card")
	_, _ = r.Write([]byte("$ grep -n needHome internal/codegen/jstable/fixedmodule.go\n"))
	w, _, ok := r.Stopped()
	require.False(t, ok, "a card that made another tool call moved past the refusal, got %+v", w)
}

// TestWriteBlockedResultNeverOverwritesAPublishedReport: a card that published owns its
// report, and the machinery never writes over one.
func TestWriteBlockedResultNeverOverwritesAPublishedReport(t *testing.T) {
	t.Parallel()

	job := t.TempDir()
	mine := filepath.Join(job, "RESULT.md")
	require.NoError(t, os.WriteFile(mine, []byte("RESULT: mine\nfindings: 0\n"), 0o644))
	_, wrote, err := WriteBlockedResult(job, "c", "write", "/etc/hosts", "2", "still")
	require.NoError(t, err, "a published report is never overwritten: wrote=%v err=%v", wrote, err)
	require.False(t, wrote, "a published report is never overwritten: wrote=%v err=%v", wrote, err)
	raw, err := os.ReadFile(mine)
	require.NoError(t, err, "the worker's own report is untouched: %q %v", raw, err)
	require.Contains(t, string(raw), "RESULT: mine", "the worker's own report is untouched: %q %v", raw, err)
}

// TestWriteBlockedResultNamesTheBlockAndCannotBeGreen: the report a card that published
// none is given. It carries the typed line, it says who wrote it, and it has NO findings
// head -- so it parses `plan-only` and can never be folded as work a worker did.
func TestWriteBlockedResultNamesTheBlockAndCannotBeGreen(t *testing.T) {
	t.Parallel()

	job := t.TempDir()
	path, wrote, err := WriteBlockedResult(job, "card-8311", "write", "/etc/hosts", "2", "the card wrote nothing for 300s")
	require.NoError(t, err, "a card with no report of its own is given one: %v %v", wrote, err)
	require.True(t, wrote, "a card with no report of its own is given one: %v %v", wrote, err)
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	body := string(raw)
	for _, want := range []string{"RESULT: BLOCKED card-8311", "WALL REFUSED write /etc/hosts task=card-8311 step=2", "written-by: nova-worker native"} {
		require.Contains(t, body, want, "the blocked report carries %q:\n%s", want, body)
	}
	rep := ParseReport(raw)
	require.Equal(t, ClassPlanOnly, rep.Class, "a report the machinery wrote must never be scored as a worker's work, got class %q", rep.Class)
}
