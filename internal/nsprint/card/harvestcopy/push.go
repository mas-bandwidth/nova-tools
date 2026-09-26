package harvestcopy

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/testguard"
)

// pushBranch is the push script of internal/nsprint/harvest/remote.go in Go,
// in the local repo dir: the branch is under nova/, the commit exists, a
// remote branch already at the sha is "already", a remote branch at another
// sha is ErrBranchMoved, else one `git push <remote> <sha>:refs/heads/<branch>`
// (never force) and the tip read back, which must be the sha.
func pushBranch(ctx context.Context, req Request, remote string) (string, error) {
	if !strings.HasPrefix(req.Branch, "nova/") {
		return "", fmt.Errorf("%w: branch %q is not under nova/", ErrPushRefused, req.Branch)
	}
	if req.RepoDir == "" {
		return "", fmt.Errorf("%w: no repo dir holds %s", ErrPushRefused, short(req.SHA))
	}
	if fi, err := os.Stat(req.RepoDir); err != nil || !fi.IsDir() {
		return "", fmt.Errorf("%w: repo dir %s: not a directory", ErrPushRefused, req.RepoDir)
	}
	git := req.Git
	if git == "" {
		git = "git"
	}
	network := networkRemote(remote)
	if network {
		// A network push reaches the forge: the guard refuses it from a test
		// (a local bare repository is what a test pushes to).
		testguard.RefuseHosts(git, "push", remote)
	}
	env, err := gitEnv(os.Environ(), req, network)
	if err != nil {
		return "", err
	}
	run := func(args ...string) (string, error) {
		cmd := exec.CommandContext(ctx, git, append([]string{"-c", "core.hooksPath=/dev/null"}, args...)...)
		cmd.Dir = req.RepoDir
		cmd.Env = env
		var out, errb bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &errb
		if err := cmd.Run(); err != nil {
			return "", fmt.Errorf("git %s: %w: %s", args[0], err, oneLine(errb.String()))
		}
		return strings.TrimSpace(out.String()), nil
	}
	if _, err := run("cat-file", "-e", req.SHA+"^{commit}"); err != nil {
		return "", fmt.Errorf("%w: no commit %s in %s: %v", ErrPushRefused, short(req.SHA), req.RepoDir, err)
	}
	tip := func() (string, error) {
		out, err := run("ls-remote", remote, "refs/heads/"+req.Branch)
		if err != nil {
			return "", err
		}
		sha, _, _ := strings.Cut(out, "\t")
		return strings.TrimSpace(sha), nil
	}
	before, err := tip()
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrPushRefused, err)
	}
	switch {
	case before == req.SHA:
		return "already", nil
	case before != "":
		return "", fmt.Errorf("%w: %w: %s at %s, not %s", ErrPushRefused, ErrBranchMoved, req.Branch, short(before), short(req.SHA))
	}
	if _, err := run("push", "-q", remote, req.SHA+":refs/heads/"+req.Branch); err != nil {
		return "", fmt.Errorf("%w: %v", ErrPushRefused, err)
	}
	after, err := tip()
	if err != nil {
		return "", fmt.Errorf("%w: after the push: %v", ErrPushRefused, err)
	}
	if after != req.SHA {
		got := after
		if got == "" {
			got = "nothing"
		}
		return "", fmt.Errorf("%w: unverified: %s at %s after the push, not %s", ErrPushRefused, req.Branch, short(got), short(req.SHA))
	}
	return "pushed", nil
}
