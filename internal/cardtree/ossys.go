package cardtree

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/subproc"
)

// StepBudget bounds one program or one POST command of a script step; the card's own
// deadline bounds the whole run.
const StepBudget = 10 * time.Minute

// OSSys is the machine's Sys: programs run as child processes with no shell, and the commit
// is git's, in the checkout. work is where the programs are written.
func OSSys(work string) Sys {
	return Sys{Run: osRun, Commit: gitCommit, Work: work}
}

func osRun(dir string, argv ...string) error {
	if len(argv) == 0 {
		return errors.New("no command")
	}
	cmd, cancel := subproc.CommandFor(context.Background(), StepBudget, argv[0], argv[1:]...)
	defer cancel()
	cmd.Dir = dir
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%v: %s", err, oneline.Cap(lastLine(out.String()), 300))
	}
	return nil
}

func lastLine(s string) string {
	l := strings.Split(strings.TrimSpace(s), "\n")
	return strings.TrimSpace(l[len(l)-1])
}

// gitCommit stages the step's paths and commits them with the step's commit line; "" when
// the program changed nothing under them.
func gitCommit(dir string, paths []string, message string) (string, error) {
	if err := osRun(dir, append([]string{"git", "add", "-A", "--"}, paths...)...); err != nil {
		return "", err
	}
	cmd, cancel := subproc.Command(context.Background(), subproc.Git, "git", "diff", "--cached", "--quiet")
	defer cancel()
	cmd.Dir = dir
	err := cmd.Run()
	var exit *exec.ExitError
	switch {
	case err == nil:
		return "", nil // nothing staged: the step changed nothing
	case !errors.As(err, &exit) || exit.ExitCode() != 1:
		return "", fmt.Errorf("git diff --cached: %v", err)
	}
	if err := osRun(dir, "git", "commit", "-q", "-m", message); err != nil {
		return "", err
	}
	head, cancel2 := subproc.Command(context.Background(), subproc.Git, "git", "rev-parse", "HEAD")
	defer cancel2()
	head.Dir = dir
	out, err := head.Output()
	if err != nil {
		return "", fmt.Errorf("git rev-parse HEAD: %v", err)
	}
	return strings.TrimSpace(string(out)), nil
}
