//go:build functional

package cardcontract

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestReview4964SSHURIWithPortMatchesTheCardRepository pins ssh:// host:port
// normalization against the card URL (docs/SPEC-CARD-CONTRACT.md, section 5).
func TestReview4964SSHURIWithPortMatchesTheCardRepository(t *testing.T) {
	t.Parallel()
	r := newRig(t, "claude", "work")
	url := "ssh://git@example.com:22/Example-Owner/example-repo.git"
	dir := filepath.Join(r.job, "port-clone")
	code, _, errb := r.sh(r.job, "git clone --depth 1 -b main "+url+" "+dir)
	require.Equal(t, 0, code, "%s", errb)
	assert.Contains(t, errb, "staged checkout")
	got, err := filepath.EvalSymlinks(dir)
	require.NoError(t, err)
	want, err := filepath.EvalSymlinks(r.repo)
	require.NoError(t, err)
	assert.Equal(t, want, got)
}

// TestReview4964UnsupportedPushOptionDoesNotPretendToPush pins a truthful
// refusal when the shim cannot carry a push option to the member's real push.
func TestReview4964UnsupportedPushOptionDoesNotPretendToPush(t *testing.T) {
	t.Parallel()
	r := newRig(t, "claude", "work")
	r.commit(r.repo, "option target")
	code, _, errb := r.sh(r.repo, "git push -o ci.skip origin HEAD")
	assert.NotEqual(t, 0, code)
	assert.Contains(t, errb, "REFUSED")
	_, head := LastPushed(r.job)
	assert.Empty(t, head, "an unsupported push option must not record a different source commit")
	_, err := os.Stat(filepath.Join(r.job, PushedName))
	assert.True(t, os.IsNotExist(err), "a refusal leaves no ordinary push record")
}

// TestReview4964DeletePushOptionsAreRefused pins both Git spellings of a
// remote-branch deletion; the member only pushes a commit to the packet branch.
func TestReview4964DeletePushOptionsAreRefused(t *testing.T) {
	t.Parallel()
	for _, flag := range []string{"--delete", "-d"} {
		t.Run(flag, func(t *testing.T) {
			r := newRig(t, "claude", "work")
			r.commit(r.repo, "delete target")
			git(t, r.repo, "branch", "feature")
			code, _, errb := r.sh(r.repo, "git push "+flag+" origin feature")
			assert.NotEqual(t, 0, code)
			assert.Contains(t, errb, "REFUSED")
			_, head := LastPushed(r.job)
			assert.Empty(t, head, "a refused delete must not be recorded as an ordinary push")
			_, err := os.Stat(filepath.Join(r.job, PushedName))
			assert.True(t, os.IsNotExist(err))
		})
	}
}

// TestReview4964GhCommandIsScopedToCardKind pins the work/read command split
// in docs/SPEC-CARD-CONTRACT.md, section 5.
func TestReview4964GhCommandIsScopedToCardKind(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ kind, command string }{
		{"read", `gh pr create --title "T" --body "x"`},
		{"work", `gh pr review --approve --body "x"`},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			r := newRig(t, "claude", tc.kind)
			code, _, errb := r.sh(r.repo, tc.command)
			assert.NotEqual(t, 0, code)
			assert.Contains(t, errb, "REFUSED")
			_, err := os.Stat(filepath.Join(r.job, FinishName))
			assert.True(t, os.IsNotExist(err), "a cross-kind gh command must not write the hidden finish")
			_, err = os.Stat(filepath.Join(r.job, "RESULT.md"))
			assert.True(t, os.IsNotExist(err), "a cross-kind gh command must not write a result")
		})
	}
}
