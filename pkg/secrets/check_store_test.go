package secrets

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// checkStore is a store as check meets it: a git working copy on main, committed,
// with main tracking origin/main at the same commit (set by hand: no remote, no
// network), one seat rowan whose key sits outside it, and the fake sops.
type checkStore struct {
	dir, key, sops string
}

// newCheckStore commits files (relative path to body) as the store's only commit.
// Nil files is the green store: recovery.pub, a rule for rowan and rowan.yaml.
func newCheckStore(t *testing.T, files map[string]string) *checkStore {
	t.Helper()
	f := newSeatFixture(t)
	if files == nil {
		files = map[string]string{
			"recovery.pub": pubRecovery + "\n",
			".sops.yaml":   "creation_rules:\n  - path_regex: ^rowan\\.yaml$\n    age: " + pubRowan + "," + pubRecovery + "\n",
			"rowan.yaml":   sealedFor([]string{pubRowan, pubRecovery}),
		}
	}
	dir := filepath.Join(t.TempDir(), "store")
	require.NoError(t, os.Mkdir(dir, 0o755))
	for name, body := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		require.NoError(t, os.WriteFile(p, []byte(body), 0o644))
	}
	gitC(t, dir, "init", "-q", "-b", "main")
	gitC(t, dir, "add", "-A")
	gitC(t, dir, "-c", "user.name=check-test", "-c", "user.email=check-test@example.com", "-c", "commit.gpgsign=false", "commit", "-q", "-m", "store")
	gitC(t, dir, "config", "branch.main.remote", "origin")
	gitC(t, dir, "config", "branch.main.merge", "refs/heads/main")
	gitC(t, dir, "update-ref", "refs/remotes/origin/main", "HEAD")
	return &checkStore{dir: dir, key: f.rowanKey, sops: f.sopsPath}
}

func (s *checkStore) write(t *testing.T, name, body string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(s.dir, name), []byte(body), 0o644))
}

// run is `check --max max` on the store: the OK line on a pass, else every line it prints.
func (s *checkStore) run(t *testing.T, max int) (out string, code int) {
	t.Helper()
	ok, fails, more, summary, code, err := RunCheck(s.dir, "rowan", s.key, s.sops, max)
	if err != nil {
		return err.Error(), code
	}
	if code == 0 {
		return ok, 0
	}
	return strings.Join(append(append(fails, more...), summary), "\n"), code
}

func TestCheckPassesTheGreenStore(t *testing.T) {
	t.Parallel()
	s := newCheckStore(t, nil)
	out, code := s.run(t, 20)
	assert.Equal(t, 0, code, out)
	assert.Regexp(t, `^SECRETS CHECK OK as=rowan recipients=2 files=1 sealed=1 mine=1 foreign=0 clear=0 head=[0-9a-f]{7}$`, out)
}

// The recovery key is checked where check reads it: a rule without it, and a store
// without a recovery.pub to check against, are both red.
func TestCheckIsRedWithoutTheRecoveryKey(t *testing.T) {
	t.Parallel()
	t.Run("a rule that lacks it", func(t *testing.T) {
		t.Parallel()
		s := newCheckStore(t, map[string]string{
			"recovery.pub": pubRecovery + "\n",
			".sops.yaml":   "creation_rules:\n  - path_regex: ^rowan\\.yaml$\n    age: " + pubRowan + "," + pubStranger + "\n",
			"rowan.yaml":   sealedFor([]string{pubRowan, pubStranger}),
		})
		out, code := s.run(t, 20)
		assert.Equal(t, 1, code, out)
		assert.Contains(t, out, `SECRETS CHECK FAIL .sops.yaml: rule for ^rowan\.yaml$ does not contain declared recovery key `+pubRecovery)
	})
	t.Run("no recovery.pub to hold the rule to", func(t *testing.T) {
		t.Parallel()
		s := newCheckStore(t, map[string]string{
			".sops.yaml": "creation_rules:\n  - path_regex: ^rowan\\.yaml$\n    age: " + pubRowan + "," + pubStranger + "\n",
			"rowan.yaml": sealedFor([]string{pubRowan, pubStranger}),
		})
		out, code := s.run(t, 20)
		assert.Equal(t, 1, code, out)
		assert.Contains(t, out, "SECRETS CHECK FAIL recovery.pub: recovery.pub is absent")
	})
}

func TestCheckRefusesAKeyFileOthersCanRead(t *testing.T) {
	t.Parallel()
	s := newCheckStore(t, nil)
	require.NoError(t, os.Chmod(s.key, 0o644))
	out, code := s.run(t, 20)
	assert.Equal(t, 2, code, out)
	assert.Contains(t, out, "expected 0600; run: chmod 600")
}

func TestCheckRefusesAStoreItCannotReadAsCommitted(t *testing.T) {
	t.Parallel()
	s := newCheckStore(t, nil)
	gitC(t, s.dir, "checkout", "-q", "--detach")
	out, code := s.run(t, 20)
	assert.Equal(t, 2, code, out)
	assert.Contains(t, out, "detached HEAD")
}

// Order is output: failures of one kind are listed by path, so two runs over the same
// store print the same lines, and --max shows the same first ones. The files come out of
// maps (the git index, the HEAD tree), so sixteen of them make an unsorted listing all
// but certain to show.
func TestCheckListsFailuresInPathOrderAndCapsTheSameOnes(t *testing.T) {
	t.Parallel()
	rules := "creation_rules:\n  - path_regex: ^rowan\\.yaml$\n    age: " + pubRowan + "," + pubRecovery + "\n"
	files := map[string]string{"recovery.pub": pubRecovery + "\n", "rowan.yaml": sealedFor([]string{pubRowan, pubRecovery})}
	var drifted []string
	for i := 16; i >= 1; i-- {
		seat := fmt.Sprintf("s%02d", i)
		rules += "  - path_regex: ^" + seat + "\\.yaml$\n    age: " + pubRowan + "," + pubRecovery + "\n"
		// sealed to a stranger, not the rule's seat: recipients-drift
		files[seat+".yaml"] = sealedFor([]string{pubStranger, pubRecovery})
		drifted = append([]string{seat + ".yaml"}, drifted...)
	}
	files[".sops.yaml"] = rules
	s := newCheckStore(t, files)

	listed := func(out, word string) []string {
		var got []string
		for _, l := range strings.Split(out, "\n") {
			if strings.Contains(l, word) {
				got = append(got, strings.TrimSuffix(strings.Fields(l)[3], ":"))
			}
		}
		return got
	}
	out, code := s.run(t, 0)
	require.Equal(t, 1, code, out)
	assert.Equal(t, drifted, listed(out, "recipients differ"))

	out, code = s.run(t, 1)
	require.Equal(t, 1, code, out)
	assert.Equal(t, drifted[:1], listed(out, "recipients differ"))
	assert.Contains(t, out, "SECRETS CHECK MORE kind=recipients-drift shown=1 total=16")

	// The working copy's own drift (the stale-working-copy candidates) is in path order too.
	for _, f := range drifted {
		s.write(t, f, sealedFor([]string{pubStranger, pubRecovery})+"# edited\n")
	}
	_, _, _, failures, refusal := ValidateAdmissibleStore(s.dir)
	require.NoError(t, refusal)
	var stale []string
	for _, f := range failures {
		if len(stale) == 0 || stale[len(stale)-1] != f.File {
			stale = append(stale, f.File)
		}
	}
	assert.Equal(t, drifted, stale)
}

// ValidateAdmissibleStore holds the working copy to what is committed: each way a
// tracked store file can differ from HEAD and the index is named, and the ways the
// working copy is not the store's own are refusals.
func TestTheWorkingCopyMustBeTheCommittedStore(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		change  func(t *testing.T, s *checkStore)
		refusal string
		want    []string
	}{
		{"as committed: nothing", func(*testing.T, *checkStore) {}, "", nil},
		{"a tracked file deleted", func(t *testing.T, s *checkStore) {
			require.NoError(t, os.Remove(filepath.Join(s.dir, "rowan.yaml")))
		}, "", []string{"missing tracked file rowan.yaml"}},
		{"a tracked file replaced by a directory", func(t *testing.T, s *checkStore) {
			require.NoError(t, os.Remove(filepath.Join(s.dir, "rowan.yaml")))
			require.NoError(t, os.Mkdir(filepath.Join(s.dir, "rowan.yaml"), 0o755))
		}, "", []string{"tracked artifact rowan.yaml is not a regular file"}},
		{"a new seat file added but not committed", func(t *testing.T, s *checkStore) {
			s.write(t, "air.yaml", sealedFor([]string{pubAir, pubRecovery}))
			gitC(t, s.dir, "add", "air.yaml")
		}, "", []string{"uncommitted file in index air.yaml"}},
		{"a committed file dropped from the index", func(t *testing.T, s *checkStore) {
			gitC(t, s.dir, "rm", "-q", "--cached", "rowan.yaml")
		}, "", []string{"untracked file rowan.yaml"}},
		{"an edit staged and not committed", func(t *testing.T, s *checkStore) {
			s.write(t, "rowan.yaml", sealedFor([]string{pubRowan, pubRecovery})+"# edited\n")
			gitC(t, s.dir, "add", "rowan.yaml")
		}, "", []string{"uncommitted modifications in rowan.yaml"}},
		{"an edit not staged", func(t *testing.T, s *checkStore) {
			s.write(t, "rowan.yaml", sealedFor([]string{pubRowan, pubRecovery})+"# edited\n")
		}, "", []string{"uncommitted modifications in rowan.yaml", "differs from git index; uncommitted changes in rowan.yaml"}},
		{"HEAD apart from the ref it tracks", func(t *testing.T, s *checkStore) {
			gitC(t, s.dir, "-c", "user.name=t", "-c", "user.email=t@example.com", "-c", "commit.gpgsign=false", "commit", "-q", "--allow-empty", "-m", "ahead")
		}, "", []string{"differs from remote-tracking ref refs/remotes/origin/main"}},
		{"a detached HEAD", func(t *testing.T, s *checkStore) {
			gitC(t, s.dir, "checkout", "-q", "--detach")
		}, "detached HEAD", nil},
		{"a branch with no upstream", func(t *testing.T, s *checkStore) {
			gitC(t, s.dir, "config", "--unset", "branch.main.remote")
		}, "has no upstream tracking branch configured", nil},
		{"a .git that is a file", func(t *testing.T, s *checkStore) {
			require.NoError(t, os.Rename(filepath.Join(s.dir, ".git"), filepath.Join(s.dir, "..", "real-git")))
			s.write(t, ".git", "gitdir: ../real-git\n")
		}, "has a .git that is a file (a worktree or submodule)", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			s := newCheckStore(t, nil)
			c.change(t, s)
			_, _, _, failures, refusal := ValidateAdmissibleStore(s.dir)
			if c.refusal != "" {
				require.Error(t, refusal)
				assert.Contains(t, refusal.Error(), c.refusal)
				return
			}
			require.NoError(t, refusal)
			assertReasons(t, failures, c.want...)
			for _, f := range failures {
				assert.Equal(t, "stale-working-copy", f.Kind)
			}
		})
	}
}

// exec and the in-process --seat readers open a seat through OpenSeatFile, which holds
// the store to the same recovery rule check does before it decrypts anything.
func TestOpenSeatFileRefusesARuleWithoutTheRecoveryKey(t *testing.T) {
	t.Parallel()
	green := newCheckStore(t, nil)
	_, err := OpenSeatFile(green.dir, "rowan", green.key, green.sops)
	require.NoError(t, err)

	s := newCheckStore(t, map[string]string{
		"recovery.pub": pubRecovery + "\n",
		".sops.yaml":   "creation_rules:\n  - path_regex: ^rowan\\.yaml$\n    age: " + pubRowan + "," + pubStranger + "\n",
		"rowan.yaml":   sealedFor([]string{pubRowan, pubStranger}),
	})
	_, err = OpenSeatFile(s.dir, "rowan", s.key, s.sops)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "does not contain declared recovery key "+pubRecovery)
}
