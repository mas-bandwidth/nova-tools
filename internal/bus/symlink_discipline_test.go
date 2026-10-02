package bus

import (
	"github.com/stretchr/testify/require"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A LANE'S STATE FILES ARE REGULAR FILES (security#30, finding 1).
//
// A hostile commit can leave a symlink where a lane state file belongs. Every one of these
// writes used to follow it and land outside the bus entirely, so each case here plants the
// link, runs the one write that reaches that path, and asks two questions a person would
// ask: did the file outside the bus change, and is the link still there for the next run.
func victim(t *testing.T, dir string) string {
	return victimHolding(t, dir, "original victim content\n")
}

func victimHolding(t *testing.T, dir, content string) string {
	t.Helper()
	v := filepath.Join(dir, "victim")
	require.NoError(t, os.WriteFile(v, []byte(content), 0o644))
	return v
}

func plant(t *testing.T, target, link string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(link), 0o755))
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("this filesystem will not make a symlink: %v", err)
	}
}

func unchanged(t *testing.T, v string) { unchangedHolding(t, v, "original victim content\n") }

func unchangedHolding(t *testing.T, v, content string) {
	t.Helper()
	raw, err := os.ReadFile(v)
	require.NoError(t, err)
	if string(raw) != content {
		require.False(t, string(raw) != content, "the file outside the bus was written through the link: %q", string(raw))
	}
}

func stillLink(t *testing.T, link string) {
	t.Helper()
	fi, err := os.Lstat(link)
	if err != nil {
		return // removed is fine; followed is not
	}
	require.False(t, fi.Mode().IsRegular(), "%s is a regular file now; the write replaced the link rather than refusing it", link)
}

func TestAppendIndexLineRefusesASymlinkedIndex(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	root := filepath.Join(dir, "bus")
	v := victim(t, dir)
	plant(t, v, filepath.Join(root, "from-x", IndexName))
	err := AppendIndexLine(root, IndexEntry{Lane: "from-x", ID: "deadbeef", Path: "from-x/2026-note.md", Date: "2026-09-13T00:00:00Z", To: []string{"bo"}})
	require.Error(t, err, "AppendIndexLine wrote through a symlinked INDEX and raised nothing")
	unchanged(t, v)
	stillLink(t, filepath.Join(root, "from-x", IndexName))
}

func TestReceiptAppendRefusesASymlinkedReceipts(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	root := filepath.Join(dir, "bus")
	v := victim(t, dir)
	plant(t, v, filepath.Join(root, "from-x", ReceiptsName))
	plan := ReceiptPlan{Lane: "from-x", Path: "from-x/" + ReceiptsName, Record: []string{"aa11bb22"}, Stamp: time.Now().UTC().Format(ReceiptStampLayout)}
	{
		err := plan.Append(root)
		require.Error(t, err, "ReceiptPlan.Append wrote through a symlinked RECEIPTS and raised nothing")
	}
	unchanged(t, v)
	stillLink(t, filepath.Join(root, "from-x", ReceiptsName))
}

// The lane reader (ReadLaneIndex) used to os.ReadFile its INDEX, which followed a planted
// symlink and blocked on a planted FIFO (issue #233). It now refuses both, never following
// and never waiting.
func TestReadLaneIndexRefusesASymlinkedIndex(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	root := filepath.Join(dir, "bus")
	v := victimHolding(t, dir, "deadbeef\tfrom-x/2026-note.md\t2026-09-13T00:00:00Z\t-\t-\n")
	plant(t, v, filepath.Join(root, "from-x", IndexName))
	_, err := ReadLaneIndex(root, "from-x")
	require.Error(t, err, "ReadLaneIndex read through a symlinked INDEX and raised nothing")
	require.Contains(t, err.Error(), "symlink", "the read refusal does not name the kind symlink: %v", err)
	unchangedHolding(t, v, "deadbeef\tfrom-x/2026-note.md\t2026-09-13T00:00:00Z\t-\t-\n")
}

func TestEnsureMergeAttributesRefusesASymlinkedAttributes(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	root := filepath.Join(dir, "bus")
	v := victim(t, dir)
	plant(t, v, filepath.Join(root, AttributesName))
	{
		_, err := EnsureMergeAttributes(root)
		require.Error(t, err, "EnsureMergeAttributes wrote through a symlinked .gitattributes and raised nothing")
	}
	unchanged(t, v)
	stillLink(t, filepath.Join(root, AttributesName))
}

func TestEnsureMergeAttributesFromRefusesASymlinkedAttributes(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	root := filepath.Join(dir, "bus")
	prefix := attributeLines[0] + "\n"
	v := victimHolding(t, dir, prefix)
	plant(t, v, filepath.Join(root, AttributesName))
	{
		_, err := EnsureMergeAttributesFrom(root, "")
		require.Error(t, err, "EnsureMergeAttributesFrom wrote through a symlinked .gitattributes and raised nothing")
	}
	unchangedHolding(t, v, prefix)
	stillLink(t, filepath.Join(root, AttributesName))
}

func TestWriteResolvedRefusesASymlinkedConflictPath(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	root := filepath.Join(dir, "bus")
	v := victim(t, dir)
	plant(t, v, filepath.Join(root, "from-x", IndexName))
	// The git add at the end fails in a directory that is no repo; the write it is asked
	// to stage happens first, which is the whole of the finding.
	_ = writeResolved(root, "from-x/"+IndexName, "resolved content\n")
	unchanged(t, v)
	stillLink(t, filepath.Join(root, "from-x", IndexName))
}

func TestAppendIndexSuffixRefusesASymlinkedIndex(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	root := filepath.Join(dir, "bus")
	v := victim(t, dir)
	link := filepath.Join(root, "from-x", IndexName)
	plant(t, v, link)
	{
		err := appendIndexSuffix(root, link, "original victim content\n", "original victim content\nappended\n")
		require.Error(t, err, "appendIndexSuffix appended through a symlinked INDEX and raised nothing")
	}
	unchanged(t, v)
	stillLink(t, link)
}

func TestReplaceLaneFileDoesNotWriteThroughAPlantedTemp(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	root := filepath.Join(dir, "bus")
	v := victim(t, dir)
	require.NoError(t, os.MkdirAll(filepath.Join(root, "from-x"), 0o755))
	plant(t, v, filepath.Join(root, "from-x", IndexName+TempSuffix))
	{
		err := replaceLaneFile(root, "from-x/"+IndexName, "a line\n")
		require.NoError(t, err, "replaceLaneFile refused a correct write: %v", err)
	}
	unchanged(t, v)
	raw, err := os.ReadFile(filepath.Join(root, "from-x", IndexName))
	if err != nil || string(raw) != "a line\n" {
		require.False(t, err != nil || string(raw) != "a line\n", "the lane file did not get its content: %q %v", string(raw), err)
	}
}

// A stranded temporary is still a lane state file's temporary, whatever unique name it was
// written under: the lane walk must step over it rather than report a stray.
func TestAStrandedUniqueTempIsStillALaneStateTemp(t *testing.T) {
	t.Parallel()

	require.False(t, !isLaneStateTemp(IndexName+TempSuffix), "the fixed temp name stopped being recognised")
	require.False(t, !isLaneStateTemp(IndexName+".ab12cd34ef56"+TempSuffix), "a unique temp name for a lane state file is not recognised as one")
	require.False(t, !isLaneStateTemp("."+IndexName+".tmp-0a1b2c3d"), "atomicfile's temporary for a lane state file is not recognised as one")
	require.False(t, isLaneStateTemp(".notes.tmp-0a1b2c3d") || isLaneStateTemp("."+IndexName+".tmp-xyz") || isLaneStateTemp("."+IndexName+".tmp-0a1b"), "a stray dot-temporary became a lane state temp")
	require.False(t, isLaneStateTemp("notes"+TempSuffix) || isLaneStateTemp("notes.ab12"+TempSuffix), "a stray temporary became a lane state temp")
	require.False(t, !strings.HasSuffix(IndexName+".ab12"+TempSuffix, TempSuffix), "a unique temp no longer ends in the reserved suffix")
}

// A LANE IS A DIRECTORY IN THE BUS, NOT A DOOR OUT OF IT (Fable's cold read of #226, F1).
//
// The first pass checked the final component only, so a commit that makes the LANE a
// symlink -- `from-x` pointing at a directory outside the checkout -- wrote every one of a
// lane's state files outside the bus with nothing raised. Every component from the bus root
// down is a component of the path.
func TestAppendIndexLineRefusesASymlinkedLaneDirectory(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	root := filepath.Join(dir, "bus")
	outside := filepath.Join(dir, "outside")
	require.NoError(t, os.MkdirAll(outside, 0o755))
	require.NoError(t, os.MkdirAll(root, 0o755))
	plant(t, outside, filepath.Join(root, "from-x"))
	err := AppendIndexLine(root, IndexEntry{Lane: "from-x", ID: "deadbeef", Path: "from-x/2026-note.md", Date: "2026-09-13T00:00:00Z", To: []string{"bo"}})
	require.Error(t, err, "AppendIndexLine wrote through a symlinked LANE DIRECTORY and raised nothing")
	if _, statErr := os.Lstat(filepath.Join(outside, IndexName)); statErr == nil {
		raw, _ := os.ReadFile(filepath.Join(outside, IndexName))
		require.FailNowf(t, "assertion failed", "the line landed outside the bus: %q", string(raw))
	}
}

func TestWriteLaneFileRefusesASymlinkedLaneDirectory(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	root := filepath.Join(dir, "bus")
	outside := filepath.Join(dir, "outside")
	require.NoError(t, os.MkdirAll(outside, 0o755))
	require.NoError(t, os.MkdirAll(root, 0o755))
	plant(t, outside, filepath.Join(root, "from-x"))
	{
		err := replaceLaneFile(root, "from-x/"+CursorName, "a cursor\n")
		require.Error(t, err, "replaceLaneFile rewrote a lane file through a symlinked lane directory")
	}
	{
		_, statErr := os.Lstat(filepath.Join(outside, CursorName))
		require.False(t, statErr == nil, "the rewrite landed outside the bus")
	}
}
