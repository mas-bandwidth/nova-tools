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

// The pool identity remedy names only what any adopter has: the flag, and the file with the
// shape it holds. One fleet's own tooling is no remedy for anybody else's.
func TestPoolIdentityRemedyNamesWhatAnyAdopterHas(t *testing.T) {
	t.Parallel()
	for _, want := range []string{"--identity <owner>,<name>,<email>", "<root>/identity.tsv", "owner, name and email"} {
		assert.Contains(t, PoolIdentityRemedy, want)
	}
	for _, not := range []string{"rowan-tools", "make -C fleet converge"} {
		assert.NotContains(t, PoolIdentityRemedy, not)
	}
	_, err := LoadPoolIdentity(t.TempDir())
	require.ErrorContains(t, err, PoolIdentityRemedy)
}

// THE STAGING BRANCH IS THE OWNER'S. A card staged with no frame branch commits on
// <owner>/<label>, the owner being the identity's first field, so one fleet's checkouts are
// rowan/<label> and another adopter's carry their own name; never a prefix built in here.
func TestStageCardBranchIsTheIdentitysOwner(t *testing.T) {
	t.Parallel()
	origin, sha := baseRepo(t)
	for _, owner := range []string{"ada", "Ada-Bench_2"} {
		t.Run(owner, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			target := filepath.Join(root, "job", "repo")
			res, err := StageCard(StageOptions{
				Card:      []byte("RESULT: c1 sha=aaaaaaaaaaaa\nbase-repo: " + origin + "\nbase-sha: " + sha + "\n"),
				TargetDir: target, JobDir: filepath.Join(root, "job"),
				BenchHome: filepath.Join(root, "home"), BenchName: "testhost", Timeout: 30 * time.Second,
				Identity: StagingIdentity{Owner: owner, Name: "Ada Bench", Email: "ada@example.com"},
			})
			require.NoError(t, err)
			assert.Equal(t, owner+"/c1", res.Branch, "the owner is used as written, never folded")
		})
	}
}

// An owner is one ref component git takes as it is: a letter or digit, then letters,
// digits, `-` and `_`. Empty, a slash, a dot (`..`, `.lock`), a space or anything git
// refuses in a ref is refused before the clone, as a missing name or email is.
func TestStageCardRefusesAnOwnerThatIsNoRefComponent(t *testing.T) {
	t.Parallel()
	origin, sha := baseRepo(t)
	for _, owner := range []string{"", "ada/bench", "ada..b", "ada.lock", "-ada", "ada bench", "ada~1", "ada@{0}"} {
		t.Run(owner, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			target := filepath.Join(root, "job", "repo")
			_, err := StageCard(StageOptions{
				Base:      &CardBase{Repo: origin, Sha: sha},
				TargetDir: target, JobDir: filepath.Join(root, "job"),
				BenchHome: filepath.Join(root, "home"), BenchName: "testhost", Timeout: 30 * time.Second,
				Identity: StagingIdentity{Owner: owner, Name: "Ada Bench", Email: "ada@example.com"},
			})
			require.ErrorContains(t, err, "staging refused: the identity's owner")
			assert.NoDirExists(t, target, "a stage refused for its owner cloned anyway")
		})
	}
}

// The same owner rule holds where an identity is first read: the flag and identity.tsv.
func TestParseIdentityAndThePoolFileRefuseAnOwnerThatIsNoRefComponent(t *testing.T) {
	t.Parallel()
	_, err := ParseIdentity("ada/bench,Ada Bench,ada@example.com")
	require.ErrorContains(t, err, "owner")
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "identity.tsv"), []byte("owner\tname\temail\nada bench\tAda Bench\tada@example.com\n"), 0o644))
	_, err = LoadPoolIdentity(dir)
	require.ErrorContains(t, err, "owner")
	id, err := ParseIdentity("Ada-Bench_2,Ada Bench,ada@example.com")
	require.NoError(t, err)
	assert.Equal(t, "Ada-Bench_2", id.Owner)
}
