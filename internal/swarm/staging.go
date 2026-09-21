package swarm

// Staging: what the launcher puts in a job directory before the worker starts
// (docs/SPEC-TOOLWORK.md §3 rules 1-2, issue #1665).
//
//  1. Staging sets the identity; the worker never does. The launcher reads the
//     pool's identity.tsv (owner, name, email), writes the job clone's LOCAL
//     git config -- user.name, user.email, commit.gpgsign=false,
//     core.hooksPath=/dev/null -- from that row, and exports
//     GIT_CONFIG_GLOBAL=/dev/null and GIT_CONFIG_NOSYSTEM=1 so a bench's own
//     config cannot leak in. A pool with no identity row is refused at launch.
//  2. Staging leaves no way out of the job root. No absolute symlink and no
//     symlink resolving outside the job root exists in a staged tree (#1557's
//     repo/dist pointer cost four legs their toolchains); the launcher checks
//     this before the first worker starts and refuses by path.

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// StagingIdentity is the pool's one identity row: the name and email every
// commit the worker makes must carry, and the owner that row belongs to.
type StagingIdentity struct {
	Owner string
	Name  string
	Email string
}

// LoadPoolIdentity reads the pool's identity.tsv: a header
// `owner\tname\temail` plus the pool's identity row. A pool with no identity
// row is refused: launching a job under nobody's name is how a commit ends up
// carrying whatever the bench's git config held.
func LoadPoolIdentity(poolDir string) (StagingIdentity, error) {
	path := filepath.Join(poolDir, "identity.tsv")
	raw, err := os.ReadFile(path)
	if err != nil {
		return StagingIdentity{}, fmt.Errorf("pool %s has no identity row in identity.tsv: %v; refusing to launch under nobody's name", poolDir, err)
	}
	var id StagingIdentity
	header := true
	for i, line := range strings.Split(string(raw), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		fields := strings.Split(line, "\t")
		if header && strings.EqualFold(strings.TrimSpace(fields[0]), "owner") {
			header = false
			continue
		}
		header = false
		if len(fields) != 3 {
			return StagingIdentity{}, fmt.Errorf("pool %s identity.tsv line %d: wants owner, name and email tab-separated", poolDir, i+1)
		}
		owner, name, email := strings.TrimSpace(fields[0]), strings.TrimSpace(fields[1]), strings.TrimSpace(fields[2])
		if owner == "" || name == "" || email == "" {
			return StagingIdentity{}, fmt.Errorf("pool %s identity.tsv line %d: owner, name and email are all required", poolDir, i+1)
		}
		id = StagingIdentity{Owner: owner, Name: name, Email: email}
		break
	}
	if id.Owner == "" {
		return StagingIdentity{}, fmt.Errorf("pool %s has no identity row in identity.tsv; refusing to launch under nobody's name", poolDir)
	}
	return id, nil
}

// StagingGitEnv is the environment the launcher exports with every job: the
// bench's own git config cannot leak into what the worker commits or what the
// machinery reads.
func StagingGitEnv() []string {
	return []string{"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1"}
}

// StageCloneIdentity writes the pool's identity into the job clone's LOCAL
// git config, so the commit the worker makes carries the pool's name whatever
// the bench's config held. The write itself runs with the bench config
// blanked, so a hostile bench config cannot redirect where local lands.
func StageCloneIdentity(repoDir string, id StagingIdentity) error {
	for _, kv := range [][2]string{
		{"user.name", id.Name},
		{"user.email", id.Email},
		{"commit.gpgsign", "false"},
		{"core.hooksPath", "/dev/null"},
	} {
		cmd := exec.Command("git", "config", "--local", kv[0], kv[1])
		cmd.Dir = repoDir
		cmd.Env = append(os.Environ(), StagingGitEnv()...)
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("staging %s local %s: %v: %s", repoDir, kv[0], err, strings.TrimSpace(string(out)))
		}
	}
	return nil
}

// CheckStagedTree refuses a staged tree that holds a way out of itself: an
// absolute symlink, or a symlink resolving outside the job root. The error
// names the link, because the launcher refuses by path.
func CheckStagedTree(jobRoot string) error {
	root, err := filepath.Abs(jobRoot)
	if err != nil {
		return fmt.Errorf("staged tree %s: %v", jobRoot, err)
	}
	rootResolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		rootResolved = root
	}
	var refused error
	walkErr := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || refused != nil {
			return err
		}
		if d.Type()&os.ModeSymlink == 0 {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		if rel == "." {
			rel = filepath.Base(path)
		}
		target, rerr := os.Readlink(path)
		if rerr != nil {
			return nil
		}
		if filepath.IsAbs(target) {
			refused = fmt.Errorf("staged tree holds symlink %s to absolute %s; refusing %s", rel, target, path)
			return nil
		}
		if escapes(filepath.Join(filepath.Dir(path), target), root) {
			refused = fmt.Errorf("staged tree holds symlink %s resolving outside the job root; refusing %s", rel, path)
			return nil
		}
		// A chain of inside links can still walk out: ask the kernel where the
		// whole chain lands, where it lands anywhere at all.
		if resolved, verr := filepath.EvalSymlinks(path); verr == nil && escapes(resolved, rootResolved) {
			refused = fmt.Errorf("staged tree holds symlink %s resolving outside the job root; refusing %s", rel, path)
		}
		return nil
	})
	if walkErr != nil {
		return walkErr
	}
	return refused
}

// escapes reports whether cleaned path p lies outside root. Both are absolute
// and clean on entry.
func escapes(p, root string) bool {
	p = filepath.Clean(p)
	rel, err := filepath.Rel(root, p)
	if err != nil {
		return true
	}
	return rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// StageJob is the launcher's staging: the pool's identity, the clone's local
// git config, and the no-way-out check, in that order. A pool with no
// identity row is refused before anything is written; a tree with a way out
// is refused by path. repoDir is the job clone and may be empty where the
// worker has not cloned yet.
func StageJob(poolDir, jobRoot, repoDir string) error {
	id, err := LoadPoolIdentity(poolDir)
	if err != nil {
		return err
	}
	if strings.TrimSpace(repoDir) != "" {
		if _, serr := os.Stat(filepath.Join(repoDir, ".git")); serr == nil {
			if err := StageCloneIdentity(repoDir, id); err != nil {
				return err
			}
		}
	}
	if err := CheckStagedTree(jobRoot); err != nil {
		return err
	}
	return nil
}
