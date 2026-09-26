// The land duty (nova-tools #3898): every `nova-sprint reconcile` pass starts
// one worker per landed repo, through registerReconcileDuty; each cfg:land
// tick (default 300 s) the worker takes lease:land:<repo> and lands every
// stream with a landable member in one batch (the land sequence of
// `nova-sprint land`, #3886), so no coordinator builds a stream branch by
// hand. The duty is reconcile.LandDuty; its LAND-DUTY lines go to the
// reconciler's stdout. Under NOVA_TEST_NO_HOST=1 it is switched off: it
// would clone, push and call the forge.
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ci"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/land/stream"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/prkey"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/reconcile"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/testguard"
)

// landDutyOut receives the duty's LAND-DUTY receipt lines.
var landDutyOut io.Writer = os.Stdout

// landDutyBudget caps the REST calls of one repo pass (the stream PRs, the
// merges, the members' and issues' closes).
const landDutyBudget = 64

func init() {
	registerReconcileDuty("land", func(st *store.Store) (reconcileDuty, error) {
		if testguard.Refusing() {
			return nil, nil
		}
		return landDuty(st), nil
	})
}

// landDuty builds the duty over a store with the production seams: the
// GitHub token, our own CI pool, and the bench mirror when there is one.
func landDuty(st *store.Store) *reconcile.LandDuty {
	host, _ := os.Hostname()
	api := os.Getenv("GITHUB_API_URL")
	return &reconcile.LandDuty{
		Client: st.Client(), Host: host, Out: landDutyOut,
		GitHub: func() (*stream.GitHub, error) { return landGitHub("reconcile land", api, landDutyBudget, st.Client()) },
		Request: func(ctx context.Context, repo, sha string, pr int, url string) (string, error) {
			r, err := ci.Request(ctx, st, ci.RequestRequest{Repo: bareRepo(repo), SHA: sha, PR: pr, URL: url})
			if err != nil {
				return "", err
			}
			if r.Status == "REFUSED" {
				return "", fmt.Errorf("REFUSED %s", r.Detail)
			}
			return r.Status, nil
		},
		Mirror: func(repo string) string {
			home, _ := os.UserHomeDir()
			if d := filepath.Join(home, "nova-bench", "mirror", prkey.Name(repo)+".git"); isDir(d) {
				return d
			}
			return ""
		},
	}
}
