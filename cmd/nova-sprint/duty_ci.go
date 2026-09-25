package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/reconcile"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/testguard"
)

type ghJobForge struct{}

func (ghJobForge) JobLog(ctx context.Context, repo string, jobID int64) (string, error) {
	if !strings.Contains(repo, "/") {
		repo = devRedEnv("NOVA_GH_OWNER", devRedOwner) + "/" + repo
	}
	path := "repos/" + repo + "/actions/jobs/" + strconv.FormatInt(jobID, 10) + "/logs"
	// Similar to failed_forge.go
	args := []string{"api", "--allow-escape-sequences", path}
	testguard.RefuseHosts("gh", args...)
	out, err := exec.CommandContext(ctx, "gh", args...).CombinedOutput()
	if err != nil {
		args = []string{"api", path}
		testguard.RefuseHosts("gh", args...)
		out2, err2 := exec.CommandContext(ctx, "gh", args...).CombinedOutput()
		if err2 != nil {
			return "", fmt.Errorf("gh api logs: %v", err2)
		}
		out = out2
	}
	return string(out), nil
}

func (ghJobForge) RerunFailedJob(ctx context.Context, repo string, jobID int64) error {
	if !strings.Contains(repo, "/") {
		repo = devRedEnv("NOVA_GH_OWNER", devRedOwner) + "/" + repo
	}
	path := "repos/" + repo + "/actions/jobs/" + strconv.FormatInt(jobID, 10) + "/rerun"
	args := []string{"api", "-X", "POST", path, "--silent"}
	testguard.RefuseHosts("gh", args...)
	out, err := exec.CommandContext(ctx, "gh", args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("gh api rerun: %v %s", err, out)
	}
	return nil
}

type ciDutyWrapper struct {
	d *reconcile.CIDuty
}

func (w *ciDutyWrapper) Run(ctx context.Context, l *reconcile.Lease) (reconcile.Counts, error) {
	counts, lines, err := w.d.Pass(ctx, l.Instance())
	for _, line := range lines {
		fmt.Fprintln(os.Stdout, line)
	}
	return counts, err
}

func init() {
	registerReconcileDuty("ci", func(st *store.Store) (reconcileDuty, error) {
		return &ciDutyWrapper{d: &reconcile.CIDuty{
			Store:  st,
			GitHub: ghJobForge{},
		}}, nil
	})
}
