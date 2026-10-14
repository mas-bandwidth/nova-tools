package doctor

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestFriendRowMismatchPrintsTheTwoWidthsAndNoWriteCommand pins the friend-row
// width mismatch (docs/SPEC-DOCTOR.md, "friend-row"): the row can be right and
// the daemon's --width stale, so the fix names both widths and the two ways to
// settle them, and never leads with the nova-config write a stale flag must not
// take.
func TestFriendRowMismatchPrintsTheTwoWidthsAndNoWriteCommand(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeFriendPlist(t, root, "ada", "--as", "ada", "--harness", "claude", "--width", "4")
	fake := friendEnv{goos: "darwin", fakeEnv: fakeEnv{env: map[string]string{"HOME": "home"}, root: root,
		exec: func(name string, _ ...string) (string, error) {
			if name == "nova-config" {
				return `{"mode":"one-shot","roles":["builder","reader"],"width":8}`, nil
			}
			return "", errors.New("unexpected " + name)
		}}}

	r := checkFriendRow(context.Background(), fake)
	assert.Equal(t, Fail, r.Status, "%+v", r)
	assert.Contains(t, r.Fix, "8", "the fix does not name the row width: %q", r.Fix)
	assert.Contains(t, r.Fix, "4", "the fix does not name the daemon width: %q", r.Fix)
	assert.False(t, strings.HasPrefix(r.Fix, "nova-config"),
		"the mismatch fix leads with a write: %q", r.Fix)
}
