//go:build !windows

package bus

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// noReplaceRename is this build's second create-exclusive publish, and on !windows it
// has nothing to call: renameat2 with RENAME_NOREPLACE and renamex_np with RENAME_EXCL
// are not reachable from the standard library with no dependencies. These tests pin the
// refusal the way the function's comment states it — this build's own sentence, never a
// borrowed errno that would claim a call was made, and nothing created, moved or
// replaced at the destination.

// TestNoreplaceOtherCoverRefusalIsThisBuildsOwnSentence covers noReplaceRename's one
// path on this build: the refusal naming this build's absence. Main path: a real source
// and destination get the sentence, which names both kernel calls it cannot reach.
// Refusal: a destination path the OS itself would reject (a NUL byte) gets the same
// sentence, not os.ErrInvalid — a borrowed errno here would tell a reader the call was
// made and answered, and no call was made.
func TestNoreplaceOtherCoverRefusalIsThisBuildsOwnSentence(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	from := filepath.Join(dir, "from.md")
	require.NoError(t, os.WriteFile(from, []byte("the draft\n"), 0o644))
	for _, c := range []struct {
		name string
		from string
		to   string
	}{
		{name: "main path: a real source and destination", from: from, to: filepath.Join(dir, "to.md")},
		{name: "refusal: a destination the OS itself would reject gets the same sentence", from: from, to: dir + "/\x00to"},
		{name: "refusal: nothing named, no call made", from: "", to: ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			err := noReplaceRename(c.from, c.to)
			require.Error(t, err, "noReplaceRename(%q, %q) returned no error", c.from, c.to)
			assert.ErrorIs(t, err, errNoNoReplaceRename, "the refusal is not this build's own error")
			assert.NotErrorIs(t, err, os.ErrInvalid, "a borrowed errno claims the call was made and answered")
			for _, want := range []string{"renameat2", "RENAME_NOREPLACE", "renamex_np", "RENAME_EXCL"} {
				assert.Contains(t, err.Error(), want, "the sentence does not name %s: %v", want, err)
			}
		})
	}
}

// TestNoreplaceOtherCoverRefusalLeavesTheDestinationUntouched covers the refusal's
// effect at the destination, the property the whole publish refuses for: create-exclusive
// never creates and never replaces. The caller's temporary has to survive too —
// publishNoReplaceWith unlinks it after this refusal — so the contract is to return
// without moving or removing anything.
func TestNoreplaceOtherCoverRefusalLeavesTheDestinationUntouched(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name      string
		toContent string
	}{
		{name: "refusal: an absent destination stays absent", toContent: ""},
		{name: "refusal: a destination present stays byte for byte", toContent: "the draft another reader published\n"},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			from := filepath.Join(dir, "from.md")
			to := filepath.Join(dir, "to.md")
			require.NoError(t, os.WriteFile(from, []byte("the new draft\n"), 0o644))
			if c.toContent != "" {
				require.NoError(t, os.WriteFile(to, []byte(c.toContent), 0o644))
			}
			err := noReplaceRename(from, to)
			require.Error(t, err, "noReplaceRename(%q, %q) returned no error", from, to)
			require.ErrorIs(t, err, errNoNoReplaceRename, "the refusal is not this build's own error")
			raw, rerr := os.ReadFile(from)
			assert.NoError(t, rerr, "the source was moved or removed: %v", rerr)
			assert.Equal(t, "the new draft\n", string(raw), "the source was touched: %q", raw)
			if c.toContent == "" {
				_, serr := os.Stat(to)
				assert.True(t, os.IsNotExist(serr), "the refusal created %q", to)
				assertOnlyFiles(t, dir, "from.md")
			} else {
				published, prerr := os.ReadFile(to)
				assert.NoError(t, prerr, "the destination was replaced: %v", prerr)
				assert.Equal(t, c.toContent, string(published), "the destination was replaced: %q", published)
				assertOnlyFiles(t, dir, "from.md", "to.md")
			}
		})
	}
}
