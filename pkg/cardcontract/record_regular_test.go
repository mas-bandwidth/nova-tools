package cardcontract

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/mas-bandwidth/nova-tools/pkg/typedrec"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// theFinish is a finish as the gh shim records it (shims.go's nova_result), thePushed a
// line as the git shim appends one to the push record.
const theFinish = `head: 0123456789abcdef0123456789abcdef01234567
branch: sprint/c1.w1
verdict: ok
gate: -
output: -
report: the card landed

## Body

the work
`

const thePushed = "sprint/c1.w1\t0123456789abcdef0123456789abcdef01234567\t0123456789abcdef0123456789abcdef01234567\n"

// The finish and the push record live in the job directory, the walled child's first
// --write, and the member reads them outside the wall into the card's result and the pull
// request body. The child can rewrite the names, so a path that is not a regular file -- a
// symlink out of the wall, a FIFO -- reads as no record at all, the outcome an absent file
// already has (security#66 finding 1, the posture pkg/swarm's readRegular gives every
// dispatcher file).
func TestASymlinkedFinishIsNoFinish(t *testing.T) {
	t.Parallel()

	t.Run("a regular finish still reads", func(t *testing.T) {
		t.Parallel()
		job := t.TempDir()
		require.NoError(t, os.MkdirAll(filepath.Join(job, ".sprint"), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(job, FinishName), []byte(theFinish), 0o644))
		require.NoError(t, os.WriteFile(filepath.Join(job, PushedName), []byte(thePushed), 0o644))

		cr, shimmed := ReadFinish(job)
		require.True(t, shimmed, "a regular finish read as none")
		assert.Equal(t, "0123456789abcdef0123456789abcdef01234567", cr.Head)
		assert.Equal(t, "sprint/c1.w1", cr.Branch)
		assert.Equal(t, "ok", cr.Verdict)
		assert.True(t, IsFinish(job, []byte(theFinish)), "the recorded finish compared unequal")
		branch, head := LastPushed(job)
		assert.Equal(t, "sprint/c1.w1", branch)
		assert.Equal(t, "0123456789abcdef0123456789abcdef01234567", head)
	})

	t.Run("a finish linked out of the job", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		job := filepath.Join(root, "job")
		require.NoError(t, os.MkdirAll(filepath.Join(job, ".sprint"), 0o755))
		outside := filepath.Join(root, "outside.md")
		require.NoError(t, os.WriteFile(outside, []byte(theFinish), 0o644))
		require.NoError(t, os.Symlink(outside, filepath.Join(job, FinishName)))

		cr, shimmed := ReadFinish(job)
		assert.False(t, shimmed, "the finish behind the link read as a finish")
		assert.Equal(t, typedrec.CardResult{}, cr)
		assert.False(t, IsFinish(job, []byte(theFinish)), "the finish behind the link compared equal")
	})

	t.Run("a .sprint linked out of the job", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		job := filepath.Join(root, "job")
		require.NoError(t, os.MkdirAll(job, 0o755))
		outside := filepath.Join(root, "outside-sprint")
		require.NoError(t, os.MkdirAll(outside, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(outside, "finish.md"), []byte(theFinish), 0o644))
		require.NoError(t, os.Symlink(outside, filepath.Join(job, ".sprint")))

		_, shimmed := ReadFinish(job)
		assert.False(t, shimmed, "a finish through a linked .sprint read as a finish")
	})

	t.Run("a push record linked out of the job", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		job := filepath.Join(root, "job")
		require.NoError(t, os.MkdirAll(filepath.Join(job, ".sprint"), 0o755))
		outside := filepath.Join(root, "outside.tsv")
		require.NoError(t, os.WriteFile(outside, []byte(thePushed), 0o644))
		require.NoError(t, os.Symlink(outside, filepath.Join(job, PushedName)))

		branch, head := LastPushed(job)
		assert.Empty(t, branch, "the pushed line behind the link read as a push")
		assert.Empty(t, head)
	})
}

// A FIFO at the finish would park an open with no writer: read as a plain file, the member's
// ReadFile waits with no deadline and the launch never posts. This test cannot run red
// without parking its own binary, so it is not in the red run; reverted, it hangs to the
// package's timeout.
func TestAFifoFinishIsNoFinish(t *testing.T) {
	t.Parallel()
	job := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(job, ".sprint"), 0o755))
	plantFIFO(t, filepath.Join(job, FinishName))

	cr, shimmed := ReadFinish(job)
	assert.False(t, shimmed, "a FIFO read as a finish")
	assert.Equal(t, typedrec.CardResult{}, cr)
}

// A record past the size ceiling is no record: the member holds what it reads whole and
// renders it into the pull request body, so a planted finish.md must not set its memory.
func TestAFinishPastTheSizeCapIsNoFinish(t *testing.T) {
	t.Parallel()
	job := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(job, ".sprint"), 0o755))
	big := append(bytes.Repeat([]byte("x"), maxRecord), []byte("\n"+theFinish)...)
	require.NoError(t, os.WriteFile(filepath.Join(job, FinishName), big, 0o644))

	cr, shimmed := ReadFinish(job)
	assert.False(t, shimmed, "a finish past the size cap read as a finish")
	assert.Equal(t, typedrec.CardResult{}, cr)
	assert.False(t, IsFinish(job, big))
}
