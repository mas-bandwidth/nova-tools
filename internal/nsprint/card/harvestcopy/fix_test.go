package harvestcopy_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/card/harvestcopy"
)

// TestHarvestFixMovesThePRBranchForward (#4270): a fix copy's harvest names
// the head it built on (Onto); the PR's branch, at that head on the remote,
// moves forward to the fix commit (never force), the open PR is found
// (already), and the result carries the new head.
func TestHarvestFixMovesThePRBranchForward(t *testing.T) {
	t.Parallel()
	bare, work, head := repos(t)
	git(t, work, "push", "-q", bare, head+":refs/heads/"+branch) // the PR's branch at its head
	if err := os.WriteFile(filepath.Join(work, "x.go"), []byte("package x\n\nfunc Fixed() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, work, "add", "x.go")
	git(t, work, "commit", "-q", "-m", "the fix")
	fix := git(t, work, "rev-parse", "HEAD")
	f, srv := newForge(t)
	f.open["mas-bandwidth:"+branch] = 4321
	req := request(bare, work, fix, srv.URL)
	req.Onto = head
	res, err := harvestcopy.Harvest(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if res.Push != "pushed" || res.Open != "already" || res.PR != 4321 || res.Head != fix || res.Branch != branch {
		t.Fatalf("result %+v", res)
	}
	if tip := git(t, work, "ls-remote", bare, "refs/heads/"+branch); tip != fix+"\trefs/heads/"+branch {
		t.Fatalf("remote tip %q, want the fix", tip)
	}
	if len(f.bodies) != 0 {
		t.Fatalf("a fix opened %d PRs, want none (the PR is open)", len(f.bodies))
	}
	// the same harvest again is idempotent
	if res, err := harvestcopy.Harvest(context.Background(), req); err != nil || res.Push != "already" {
		t.Fatalf("second harvest %+v %v", res, err)
	}
}

// TestHarvestFixRefusesABranchNotAtOnto: the PR's branch at any sha but the
// one the fix built on is ErrBranchMoved (a work copy's rule, unchanged:
// with no Onto the branch must not exist), and nothing is pushed.
func TestHarvestFixRefusesABranchNotAtOnto(t *testing.T) {
	t.Parallel()
	bare, work, head := repos(t)
	_, srv := newForge(t)
	for _, tc := range []struct{ name, onto, at string }{
		{"moved past the head", "0000000000000000000000000000000000000000", head},
		{"gone", head, ""},
		{"work copy, branch exists", "", head},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.at != "" {
				git(t, work, "push", "-q", "-f", bare, tc.at+":refs/heads/"+branch)
			} else {
				git(t, work, "push", "-q", bare, ":refs/heads/"+branch)
			}
			req := request(bare, work, head, srv.URL)
			req.SHA = git(t, work, "rev-parse", "HEAD")
			req.Onto = tc.onto
			if tc.at == req.SHA {
				// make the pushed sha differ from the remote tip
				if err := os.WriteFile(filepath.Join(work, "y.go"), []byte("package x\n"), 0o644); err != nil {
					t.Fatal(err)
				}
				git(t, work, "add", "y.go")
				git(t, work, "commit", "-q", "-m", "another")
				req.SHA = git(t, work, "rev-parse", "HEAD")
			}
			_, err := harvestcopy.Harvest(context.Background(), req)
			if !errors.Is(err, harvestcopy.ErrBranchMoved) {
				t.Fatalf("err=%v, want ErrBranchMoved", err)
			}
			if harvestcopy.Reason(err) != "push-refused" {
				t.Fatalf("reason %q", harvestcopy.Reason(err))
			}
		})
	}
}
