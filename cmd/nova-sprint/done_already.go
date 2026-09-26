// The done-already duty of `nova-sprint reconcile` (nova-tools#3919): a card
// ended `ABSTAIN done-already <sha>` has its sha checked on its base in this
// host's mirror and its origin issue closed with the evidence comment
// (internal/nsprint/reconcile DoneAlready).
package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/card"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/reconcile"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/testguard"
)

// doneAlreadyForge is the duty's forge seam, swapped in tests (CI-NET: no
// host in a test).
var doneAlreadyForge = func() reconcile.IssueCloser { return ghIssueCloser{} }

func init() {
	registerReconcileDuty("done-already", func(st *store.Store) (reconcileDuty, error) {
		return &doneAlreadyDuty{d: &reconcile.DoneAlready{
			Client: st.Client(), Forge: doneAlreadyForge(), Mirror: card.MirrorPath,
		}}, nil
	})
}

// doneAlreadyDuty prints one receipt line per card the pass acted on, and
// one WAIT line per card when its reason first appears or changes (a forge
// that refused the close, a mirror this host lacks, a git error): a card
// the duty retries every DoneAlreadyEvery with no line was the silent
// shape (Glenn 2026-09-26), so a WAIT is on stdout once per reason.
type doneAlreadyDuty struct {
	d    *reconcile.DoneAlready
	last map[string]string // card -> the WAIT why last printed
}

func (dd *doneAlreadyDuty) Run(ctx context.Context, l *reconcile.Lease) (reconcile.Counts, error) {
	if l == nil {
		return reconcile.Counts{}, fmt.Errorf("done-already: no lease")
	}
	if dd.last == nil {
		dd.last = map[string]string{}
	}
	out, err := dd.d.Pass(ctx, l.Token())
	c := reconcile.Counts{}
	for _, o := range out {
		key := o.Sprint + "/" + o.Label
		switch o.Action {
		case "CLOSED", "REFUSED":
			c.Routed++
			delete(dd.last, key)
			fmt.Fprintln(os.Stdout, o.Line())
		case "WAIT":
			if dd.last[key] != o.Why {
				dd.last[key] = o.Why
				fmt.Fprintln(os.Stdout, o.Line())
			}
		}
	}
	return c, err
}

// ghIssueCloser closes an issue by REST (`gh api`): state closed
// (completed), then the evidence comment. Closing a closed issue is a no-op,
// so a retry after a failed comment posts the comment once.
type ghIssueCloser struct{}

func (ghIssueCloser) CloseIssue(ctx context.Context, repo string, number int, comment string) error {
	if !strings.Contains(repo, "/") {
		repo = devRedEnv("NOVA_GH_OWNER", devRedOwner) + "/" + repo
	}
	path := "repos/" + repo + "/issues/" + strconv.Itoa(number)
	for _, args := range [][]string{
		{"api", "-X", "PATCH", path, "-f", "state=closed", "-f", "state_reason=completed", "--silent"},
		{"api", "-X", "POST", path + "/comments", "-f", "body=" + comment, "--silent"},
	} {
		testguard.RefuseHosts("gh", args...)
		if out, err := exec.CommandContext(ctx, "gh", args...).CombinedOutput(); err != nil {
			return fmt.Errorf("gh api %s %s: %w: %s", args[2], args[3], err, strings.TrimSpace(string(out)))
		}
	}
	return nil
}
