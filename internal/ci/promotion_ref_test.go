package ci

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/goenv"
)

// TestAbsentPromotionBranchDeletesTheStaleLocalRefAndExcusesNothing pins the
// classtests rule's promotion read (docs/SPEC-CI.md, `classtests`): when origin
// has no promotion branch, `ci fetch-ancestry --promotion` deletes a retained
// refs/remotes/origin/<branch> and exits 0 saying there is no promotion to
// read, so the landing run finds no ref and excuses no deletion. The fixture is
// the dev landing's repository with its origin an empty bare repository and the
// stale tracking ref still present; before the verb the stale ref excuses
// gone_test.go, after it the deletion is a finding.
func TestAbsentPromotionBranchDeletesTheStaleLocalRefAndExcusesNothing(t *testing.T) {
	t.Parallel()
	const staleRef = "refs/remotes/origin/sprint/foundation"
	r := buildFoundationRepo(t)
	merge := r.land("promotion-shaped merge, promotion branch gone from origin", true, nil)

	got, note := r.run(merge, "push", "refs/heads/dev", "")
	require.Empty(t, got, "with the stale ref the landing excuses its deletions: findings = %q, note = %q; the fixture is wrong", got, note)

	origin := t.TempDir()
	r.git("init", "-q", "--bare", origin)
	r.git("remote", "add", "origin", "file://"+origin)

	bin := filepath.Join(t.TempDir(), "ci")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	build := exec.CommandContext(ctx, "go", "build", "-o", bin, "./tools/ci")
	build.Dir = repoRoot(t)
	build.Env = goenv.Clean(os.Environ())
	out, err := build.CombinedOutput()
	require.NoError(t, err, "go build ./tools/ci: %s", out)

	run, stop := context.WithTimeout(context.Background(), time.Minute)
	defer stop()
	cmd := exec.CommandContext(run, bin, "fetch-ancestry", "--promotion", "sprint/foundation")
	cmd.Dir = r.root
	cmd.Env = append(os.Environ(), "GITHUB_EVENT_NAME=pull_request")
	var stderr strings.Builder
	cmd.Stderr = &stderr
	stdout, err := cmd.Output()
	require.NoError(t, err, "fetch-ancestry exits 0 when origin has no branch: stderr %q", stderr.String())
	assert.Equal(t, "origin has no branch sprint/foundation: no promotion to read\n", string(stdout))

	_, err = gitEnvOut(r.root, nil, "rev-parse", "--verify", "-q", staleRef)
	assert.Error(t, err, "%s survives the verb: a retained ref origin no longer has", staleRef)

	got, note = r.run(merge, "push", "refs/heads/dev", "")
	exactlyOne(t, "the landing after the verb removed the stale ref", got, "gone_test.go")
	assert.Contains(t, note, "origin has no sprint/foundation", "note = %q; want the absence named", note)
}
