package converge

// git.go is the seam between this verb and a checkout. CLASSES needs one thing
// from history -- what `docs/SPEC-CI.md` said at --since -- and it asks for it
// in two questions a fake can answer: which commit was current then, and what
// that commit's copy of a file says. The checkout is READ: nothing here
// checks out, fetches, or writes.

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// Git is what CLASSES needs from a checkout, and all of it.
type Git interface {
	// RevBefore is the last commit on the current branch at or before t.
	RevBefore(ctx context.Context, t time.Time) (string, error)
	// Show is one path's content at one revision.
	Show(ctx context.Context, rev, path string) (string, error)
}

// RealGit runs git in one directory, bounded.
type RealGit struct {
	Dir     string
	Timeout time.Duration
	// Bin is the git executable, "git" unless a caller names another.
	Bin string
}

func (g RealGit) run(ctx context.Context, args ...string) (string, error) {
	bin := g.Bin
	if strings.TrimSpace(bin) == "" {
		bin = "git"
	}
	ctx, cancel := context.WithTimeout(ctx, g.Timeout)
	defer cancel()
	full := append([]string{"-C", g.Dir}, args...)
	cmd := exec.CommandContext(ctx, bin, full...)
	var out, errb strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = &errb
	err := cmd.Run()
	if ctx.Err() == context.DeadlineExceeded {
		return "", fmt.Errorf("git %s ran past --timeout %s and was killed", oneline.Field(args[0]), g.Timeout)
	}
	if err != nil {
		return "", fmt.Errorf("git %s in %s: %s: %s", oneline.Field(args[0]), oneline.Field(g.Dir), oneline.Err(err),
			oneline.Cap(oneline.Escape(strings.TrimSpace(errb.String())), oneline.TailBytes))
	}
	return out.String(), nil
}

// RevBefore asks for the last commit at or before an instant.
func (g RealGit) RevBefore(ctx context.Context, t time.Time) (string, error) {
	out, err := g.run(ctx, "rev-list", "-1", "--before="+t.UTC().Format(time.RFC3339), "HEAD")
	if err != nil {
		return "", err
	}
	rev := strings.TrimSpace(out)
	if rev == "" {
		return "", fmt.Errorf("no commit in %s at or before %s; the checkout does not reach back that far",
			oneline.Field(g.Dir), t.UTC().Format(time.RFC3339))
	}
	return rev, nil
}

// Show reads one path at one revision.
func (g RealGit) Show(ctx context.Context, rev, path string) (string, error) {
	return g.run(ctx, "show", rev+":"+path)
}

// SpecCIPath is the document CLASSES counts, relative to the checkout root.
const SpecCIPath = "docs/SPEC-CI.md"
