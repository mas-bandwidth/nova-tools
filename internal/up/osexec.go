package up

import (
	"bytes"
	"context"
	"crypto/rand"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/oneline"
	"github.com/mas-bandwidth/nova-tools/pkg/subproc"
)

// osExec runs programs on this machine, each under its subproc budget.
type osExec struct{}

func (osExec) LookPath(name string) (string, error) { return exec.LookPath(name) }

func (osExec) Run(ctx context.Context, c Cmd) (string, error) {
	b := subproc.Prepare(ctx, subproc.BudgetOf(c.Name, c.Args), c.Name, c.Args...)
	defer b.Cancel()
	cmd := b.Cmd
	cmd.Dir = c.Dir
	cmd.Env = append(os.Environ(), c.Env...)
	cmd.Stdin = strings.NewReader(c.Stdin)
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	if err := b.Wrap(c.Name, cmd.Run()); err != nil {
		return out.String(), fmt.Errorf("%w: %s", err, oneline.Cap(lastLine(errOut.String()+out.String()), oneline.TailBytes))
	}
	return out.String(), nil
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return lines[len(lines)-1]
}

// Local is this machine.
func Local() (Machine, error) {
	home, err := os.UserHomeDir()
	return Machine{GOOS: runtime.GOOS, Home: home, UID: os.Getuid(), Exec: osExec{}, Now: time.Now, Rand: rand.Reader}, err
}
