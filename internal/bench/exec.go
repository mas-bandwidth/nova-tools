package bench

import (
	"context"
	"errors"
	"io"
	"os/exec"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/subproc"
	"github.com/mas-bandwidth/nova-tools/internal/testguard"
)

// SSHOptions are on every ssh this package starts, and on rsync's: BatchMode
// so a missing key is a refusal rather than a prompt nobody answers,
// ConnectTimeout so a sleeping bench costs seconds and the fallback is tried,
// and no agent forwarded to a bench.
var SSHOptions = []string{"-o", "BatchMode=yes", "-o", "ConnectTimeout=10", "-o", "ForwardAgent=no"}

// Exec is the production transport: the system ssh and rsync.
type Exec struct{}

// Shell runs line on host through ssh.
func (Exec) Shell(ctx context.Context, host, line string, stdout, stderr io.Writer) (int, error) {
	args := append(append(append([]string(nil), SSHOptions...), host), line)
	testguard.RefuseHosts("ssh", args...)
	cmd := subproc.Long(ctx, "ssh", args...)
	cmd.Stdout, cmd.Stderr = stdout, stderr
	err := cmd.Run()
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return exit.ExitCode(), nil
	}
	if err != nil {
		return -1, err
	}
	return 0, nil
}

// Copy copies src's contents to host:dst with rsync over the same ssh options.
func (Exec) Copy(ctx context.Context, host, src, dst string, withGit bool, stderr io.Writer) error {
	args := []string{"-a", "-e", "ssh " + strings.Join(SSHOptions, " ")}
	if !withGit {
		args = append(args, "--exclude=/.git")
	}
	args = append(args, strings.TrimRight(src, "/")+"/", host+":"+dst+"/")
	testguard.RefuseHosts("rsync", args...)
	cmd := subproc.Long(ctx, "rsync", args...)
	cmd.Stderr = stderr
	return cmd.Run()
}
