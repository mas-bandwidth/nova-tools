package swarm

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/testgit"

	"github.com/mas-bandwidth/nova-tools/pkg/cardcontract"
	"github.com/mas-bandwidth/nova-tools/pkg/config"
	"github.com/mas-bandwidth/nova-tools/pkg/gitrun"
)

// stageTestGit runs git in dir with a clean, fixed environment (no host global config),
// so the test's setup commits never depend on the machine's own identity.
func stageTestGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	env := testgit.Environ("GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1")
	res, err := gitrun.Run(context.Background(), gitrun.Options{C: dir, Env: env, OwnRepo: dir != ""}, args...)
	require.NoError(t, err, "git %v: %s", args, res.Stderr)
	return strings.TrimSpace(string(res.Stdout))
}

// TestReworkAtTipCarriesTheConfiguredIdentity proves the rework-at-tip commit
// (restageAtTip), the machine's own commit, carries the configured identity even when the
// host's global git config names the owner (docs/SPEC-SPRINT.md, the machine's commit
// identity). The staging git runs with a planted global config naming the owner; the commit
// must still be the configured identity.
func TestReworkAtTipCarriesTheConfiguredIdentity(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	home := filepath.Join(root, "home")
	require.NoError(t, os.Mkdir(home, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(home, ".gitconfig"),
		[]byte("[user]\n\tname = Owner\n\temail = owner@example.com\n"), 0o600))
	// The staging git runs with a HOME naming the owner and a different author in its own
	// environment (ownerGit), so the configured identity is the only thing that can make the
	// commit the machine's.
	git := ownerGit(home)

	bare := filepath.Join(root, "origin.git")
	work := filepath.Join(root, "work")
	require.NoError(t, os.Mkdir(work, 0o755))
	stageTestGit(t, "", "init", "-q", "--bare", "-b", "main", bare)
	stageTestGit(t, work, "init", "-q", "-b", "main")
	stageTestGit(t, work, "remote", "add", "origin", bare)
	require.NoError(t, os.WriteFile(filepath.Join(work, "base"), []byte("base\n"), 0o600))
	stageTestGit(t, work, "add", "base")
	stageTestGit(t, work, "commit", "-q", "-m", "base")
	stageTestGit(t, work, "push", "-q", "origin", "HEAD:refs/heads/main")

	// The previous attempt's head, on its own branch, pushed so a rework can carry it.
	stageTestGit(t, work, "switch", "-q", "-c", "feat")
	require.NoError(t, os.WriteFile(filepath.Join(work, "carried"), []byte("carried\n"), 0o600))
	stageTestGit(t, work, "add", "carried")
	stageTestGit(t, work, "commit", "-q", "-m", "the work of attempt 1")
	prev := stageTestGit(t, work, "rev-parse", "HEAD")
	stageTestGit(t, work, "push", "-q", "origin", "HEAD:refs/heads/feat")

	// The base branch moves on: the rework is staged at its tip.
	stageTestGit(t, work, "switch", "-q", "main")
	require.NoError(t, os.WriteFile(filepath.Join(work, "tip"), []byte("tip\n"), 0o600))
	stageTestGit(t, work, "add", "tip")
	stageTestGit(t, work, "commit", "-q", "-m", "the base moved")
	stageTestGit(t, work, "push", "-q", "origin", "HEAD:refs/heads/base")

	// A clone, as a worker's is: no local identity set, so only the identity passed to the
	// rework-at-tip commit can name it.
	clone := filepath.Join(root, "clone")
	stageTestGit(t, root, "clone", "-q", bare, clone)
	identity := config.GitIdentity{Name: "nova-sprint", Email: "nova-sprint@example.invalid"}
	var fetchTime, checkoutTime time.Duration
	carry, what, out, err := restageAtTip(context.Background(), git, clone, "sprint/card", "base",
		Rework{Prev: prev, From: 1}, identity.Env(), &fetchTime, &checkoutTime)
	require.NoError(t, err, "%s: %s", what, out)
	require.NotNil(t, carry)
	require.Equal(t, cardcontract.CarryOK, carry.State)

	got := stageTestGit(t, clone, "log", "-1", "--format=%an <%ae>|%cn <%ce>")
	require.Equal(t, "nova-sprint <nova-sprint@example.invalid>|nova-sprint <nova-sprint@example.invalid>", got)
}

// TestStageJobCloneCarriesTheConfiguredIdentity proves the live launcher entry points
// (StageJob, StageCloneIdentity and their As variants) write the machine's identity into
// the worker's clone's local git config at clone time — not the pool's owner and not the
// host's global config — so a commit the worker makes in the clone carries the machine
// (docs/SPEC-SPRINT.md, the machine's commit identity). StageCloneIdentityAs is the step
// that writes the local config; StageJobAs is the launcher's staging that calls it, and
// StageJob is the live wrapper that passes the machine's own default.
func TestStageJobCloneCarriesTheConfiguredIdentity(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	home := filepath.Join(root, "home")
	require.NoError(t, os.Mkdir(home, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(home, ".gitconfig"),
		[]byte("[user]\n\tname = Owner\n\temail = owner@example.com\n"), 0o600))

	pool := filepath.Join(root, "pool")
	job := filepath.Join(root, "job")
	require.NoError(t, os.MkdirAll(pool, 0o755))
	require.NoError(t, os.MkdirAll(job, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(pool, "identity.tsv"),
		[]byte("owner\tname\temail\nowner\tbob\tbob@example.com\n"), 0o644))

	work := filepath.Join(root, "work")
	require.NoError(t, os.Mkdir(work, 0o755))
	stageTestGit(t, work, "init", "-q", "-b", "main")
	require.NoError(t, os.WriteFile(filepath.Join(work, "README"), []byte("base\n"), 0o600))
	stageTestGit(t, work, "add", "README")
	stageTestGit(t, work, "commit", "-q", "-m", "base")

	require.NoError(t, StageJobAs(pool, job, work, config.DefaultGitIdentity))

	require.Equal(t, "nova-sprint", stageTestGit(t, work, "config", "user.name"))
	require.Equal(t, "nova-sprint@example.invalid", stageTestGit(t, work, "config", "user.email"))

	require.NoError(t, StageCloneIdentityAs(work, StagingIdentity{Owner: "owner", Name: "bob", Email: "bob@example.com"}, config.DefaultGitIdentity))
	require.Equal(t, "nova-sprint", stageTestGit(t, work, "config", "user.name"))
	require.Equal(t, "nova-sprint@example.invalid", stageTestGit(t, work, "config", "user.email"))

	require.NoError(t, StageJob(pool, job, work))
	require.Equal(t, "nova-sprint", stageTestGit(t, work, "config", "user.name"))
	require.Equal(t, "nova-sprint@example.invalid", stageTestGit(t, work, "config", "user.email"))
}

// TestStageCardReworkCarriesTheConfiguredIdentity proves the live staging entry point
// (StageCard) writes the machine's default identity into the clone's local config and that
// the rework-at-tip carry commit it stages carries the machine — even when the host's global
// config names the owner and no identity is passed (StageOptions.GitIdentity empty, so the
// machine's own default applies). This is the live path a launcher triggers, not restageAtTip
// or a *As variant alone (docs/SPEC-SPRINT.md, the machine's commit identity).
func TestStageCardReworkCarriesTheConfiguredIdentity(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	home := filepath.Join(root, "home")
	require.NoError(t, os.Mkdir(home, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(home, ".gitconfig"),
		[]byte("[user]\n\tname = Owner\n\temail = owner@example.com\n"), 0o600))
	git := ownerGit(home)

	remote := filepath.Join(root, "origin.git")
	work := filepath.Join(root, "work")
	require.NoError(t, os.Mkdir(work, 0o755))
	stageTestGit(t, "", "init", "-q", "--bare", "-b", "main", remote)
	stageTestGit(t, work, "init", "-q", "-b", "main")
	stageTestGit(t, work, "remote", "add", "origin", remote)
	require.NoError(t, os.WriteFile(filepath.Join(work, "base"), []byte("base\n"), 0o600))
	stageTestGit(t, work, "add", "base")
	stageTestGit(t, work, "commit", "-q", "-m", "base")
	stageTestGit(t, work, "push", "-q", "origin", "HEAD:refs/heads/main")

	stageTestGit(t, work, "switch", "-q", "-c", "feat")
	require.NoError(t, os.WriteFile(filepath.Join(work, "carried"), []byte("carried\n"), 0o600))
	stageTestGit(t, work, "add", "carried")
	stageTestGit(t, work, "commit", "-q", "-m", "the work of attempt 1")
	prev := stageTestGit(t, work, "rev-parse", "HEAD")
	stageTestGit(t, work, "push", "-q", "origin", "HEAD:refs/heads/feat")

	stageTestGit(t, work, "switch", "-q", "main")
	require.NoError(t, os.WriteFile(filepath.Join(work, "tip"), []byte("tip\n"), 0o600))
	stageTestGit(t, work, "add", "tip")
	stageTestGit(t, work, "commit", "-q", "-m", "the base moved")
	stageTestGit(t, work, "push", "-q", "origin", "HEAD:refs/heads/main")

	clone := filepath.Join(root, "clone")
	res, err := StageCard(StageOptions{
		Card:      nil,
		Base:      &CardBase{Repo: remote, Ref: "main"},
		TargetDir: clone,
		JobDir:    filepath.Join(root, "job"),
		Rework:    &Rework{Prev: prev, From: 1},
		git:       git,
	})
	require.NoError(t, err)
	require.True(t, res.Staged)

	require.Equal(t, "nova-sprint", stageTestGit(t, clone, "config", "user.name"))
	require.Equal(t, "nova-sprint@example.invalid", stageTestGit(t, clone, "config", "user.email"))
	require.NotNil(t, res.Carry)
	require.Equal(t, cardcontract.CarryOK, res.Carry.State)
	got := stageTestGit(t, clone, "log", "-1", "--format=%an <%ae>|%cn <%ce>", res.Carry.Staged)
	require.Equal(t, "nova-sprint <nova-sprint@example.invalid>|nova-sprint <nova-sprint@example.invalid>", got)
}

// ownerGit is stageGit with the owner's HOME and author in the command's own environment,
// the host a worker might run on: a global config and an author that are not the machine's.
// The staging steps append the configured identity after it, so only that identity can
// name a commit the machine makes.
func ownerGit(home string) func(context.Context, ...string) *exec.Cmd {
	return func(ctx context.Context, args ...string) *exec.Cmd {
		cmd := stageGit(ctx, args...)
		cmd.Env = append(cmd.Env, "HOME="+home, "GIT_CONFIG_GLOBAL="+filepath.Join(home, ".gitconfig"),
			"GIT_AUTHOR_NAME=owner", "GIT_AUTHOR_EMAIL=owner@example.com",
			"GIT_COMMITTER_NAME=owner", "GIT_COMMITTER_EMAIL=owner@example.com")
		return cmd
	}
}
