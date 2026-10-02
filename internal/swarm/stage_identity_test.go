package swarm

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// THE STAGED CHECKOUT COMMITS UNDER THE CALLER'S IDENTITY, NEVER THE TOOL'S. StageCard
// wrote one person's name and address into every staged checkout's local git config; a
// tool other people adopt carries no one's identity. The identity is the caller's (native
// passes the pool identity it resolved: --identity, else <root>/identity.tsv), and a stage
// given none is refused before any git runs, naming the setting.

// testStageIdentity is the commit identity the other staging tests stage under.
var testStageIdentity = StagingIdentity{Owner: "test-owner", Name: "Pool Worker", Email: "pool@example.com"}

// stagedConfig reads one key of a staged checkout's LOCAL git config.
func stagedConfig(t *testing.T, repo, key string) string {
	t.Helper()
	cmd := exec.Command("git", "-C", repo, "config", "--local", "--get", key)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	out, err := cmd.Output()
	require.NoError(t, err, "git config --local --get %s in %s", key, repo)
	return strings.TrimSpace(string(out))
}

func TestStageCardWritesTheCallersIdentity(t *testing.T) {
	t.Parallel()
	origin, sha := baseRepo(t)
	root := t.TempDir()
	target := filepath.Join(root, "job", "repo")
	res, err := StageCard(StageOptions{
		Base:      &CardBase{Repo: origin, Sha: sha},
		TargetDir: target, JobDir: filepath.Join(root, "job"),
		BenchHome: filepath.Join(root, "home"), BenchName: "testhost", Timeout: 30 * time.Second,
		Identity: StagingIdentity{Owner: "pool", Name: "Ada Bench", Email: "ada@example.com"},
	})
	require.NoError(t, err)
	require.True(t, res.Staged)
	assert.Equal(t, "Ada Bench", stagedConfig(t, target, "user.name"))
	assert.Equal(t, "ada@example.com", stagedConfig(t, target, "user.email"))
}

func TestStageCardRefusesWithNoIdentity(t *testing.T) {
	t.Parallel()
	origin, sha := baseRepo(t)
	for _, c := range []struct {
		name string
		id   StagingIdentity
	}{
		{"none", StagingIdentity{}},
		{"no email", StagingIdentity{Owner: "pool", Name: "Ada Bench"}},
		{"no name", StagingIdentity{Owner: "pool", Email: "ada@example.com"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			target := filepath.Join(root, "job", "repo")
			res, err := StageCard(StageOptions{
				Base:      &CardBase{Repo: origin, Sha: sha},
				TargetDir: target, JobDir: filepath.Join(root, "job"),
				BenchHome: filepath.Join(root, "home"), BenchName: "testhost", Timeout: 30 * time.Second,
				Identity: c.id,
			})
			require.ErrorContains(t, err, "staging refused: no commit identity")
			require.ErrorContains(t, err, "--identity <owner>,<name>,<email>")
			assert.False(t, res.Staged)
			assert.NoDirExists(t, target, "a stage refused for its identity cloned anyway")
		})
	}
}
