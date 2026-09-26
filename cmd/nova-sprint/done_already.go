// The done-already duty of `nova-sprint reconcile` (nova-tools#3919): a card
// ended `ABSTAIN done-already <sha>` has its sha checked on its base in this
// host's mirror and its origin issue closed with the evidence comment
// (internal/nsprint/reconcile DoneAlready).
package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/gh"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/card"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/reconcile"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/redis/go-redis/v9"
)

// doneAlreadyForge is the duty's forge seam, swapped in tests (CI-NET: no
// host in a test). The store counts the calls (#4343).
var doneAlreadyForge = func(st *store.Store) reconcile.IssueCloser {
	return ghIssueCloser{verb: "reconcile done-already", rdb: st.Client()}
}

func init() {
	registerReconcileDuty("done-already", func(st *store.Store) (reconcileDuty, error) {
		return &doneAlreadyDuty{d: &reconcile.DoneAlready{
			Client: st.Client(), Forge: doneAlreadyForge(st), Mirror: card.MirrorPath,
		}}, nil
	})
}

// doneAlreadyDuty prints one receipt line per card the pass acted on.
type doneAlreadyDuty struct{ d *reconcile.DoneAlready }

func (dd *doneAlreadyDuty) Run(ctx context.Context, l *reconcile.Lease) (reconcile.Counts, error) {
	if l == nil {
		return reconcile.Counts{}, fmt.Errorf("done-already: no lease")
	}
	out, err := dd.d.Pass(ctx, l.Token())
	c := reconcile.Counts{}
	for _, o := range out {
		if o.Action == "CLOSED" || o.Action == "REFUSED" {
			c.Routed++
			fmt.Fprintln(os.Stdout, o.Line())
		}
	}
	return c, err
}

// ghIssueCloser closes an issue through the one GitHub client
// (internal/gh, #4343): state closed (completed), then the evidence
// comment. Closing a closed issue is a no-op, so a retry after a failed
// comment posts the comment once. Two writes, paced and counted under verb.
type ghIssueCloser struct {
	verb string
	rdb  redis.Cmdable
}

func (g ghIssueCloser) CloseIssue(ctx context.Context, repo string, number int, comment string) error {
	if !strings.Contains(repo, "/") {
		repo = devRedEnv("NOVA_GH_OWNER", devRedOwner) + "/" + repo
	}
	tok, err := gh.Token()
	if err != nil {
		return err
	}
	c := &gh.Client{Token: tok, Verb: g.verb, Redis: g.rdb, Log: os.Stderr}
	if err := c.CloseIssue(ctx, repo, number); err != nil {
		return err
	}
	_, err = c.Comment(ctx, repo, number, comment)
	return err
}
