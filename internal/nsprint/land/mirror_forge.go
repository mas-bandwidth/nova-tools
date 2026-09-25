package land

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// MirrorMerge reads mergeability from a local mirror. GitHub's lazy REST
// mergeable field is deliberately not part of this adapter.
type MirrorMerge struct {
	GitDir string
	Remote string
	Base   string
	Fetch  func(context.Context, string, int) error
}

func (f MirrorMerge) git(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", f.GitDir}, args...)...)
	b, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(b)), err
}

func (f MirrorMerge) Mergeable(ctx context.Context, repo string, number int) (string, error) {
	if f.GitDir == "" || number <= 0 {
		return "", errors.New("land: mirror and positive PR number are required")
	}
	remote := f.Remote
	if remote == "" {
		remote = "origin"
	}
	base := f.Base
	if base == "" {
		base = "dev"
	}
	headRef := "refs/nova-sprint/pull/" + strconv.Itoa(number) + "/head"
	var fetchErr error
	if f.Fetch != nil {
		fetchErr = f.Fetch(ctx, repo, number)
	} else {
		_, fetchErr = f.git(ctx, "fetch", "--quiet", remote, "refs/pull/"+strconv.Itoa(number)+"/head:"+headRef)
	}
	if fetchErr != nil {
		return "UNKNOWN", nil
	}
	if _, err := f.git(ctx, "rev-parse", "--verify", headRef+"^{commit}"); err != nil {
		return "UNKNOWN", nil
	}
	baseRef := "refs/remotes/" + remote + "/" + base
	if _, err := f.git(ctx, "rev-parse", "--verify", baseRef+"^{commit}"); err != nil {
		baseRef = base
	}
	out, err := f.git(ctx, "merge-tree", "--write-tree", baseRef, headRef)
	if err == nil && len(strings.Fields(out)) > 0 {
		return "MERGEABLE", nil
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) && ee.ExitCode() == 1 {
		return "CONFLICTING", nil
	}
	if err != nil {
		return "", fmt.Errorf("land: git merge-tree: %w: %s", err, out)
	}
	return "UNKNOWN", nil
}
