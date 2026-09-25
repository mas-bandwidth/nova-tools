package merge

import (
	"context"
	"strings"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

// The lander reads CI from the ci:<repo>:<sha> redis key, never from the forge's
// check-runs (nova-tools #2924). This is the red contract: a fixture PR whose head has a
// GREEN check-run and no ci key is refused `ci: MISSING`, and with ci:<repo>:<head> = OK
// it batches -- the green check-run never stands in for the ci key, whichever side of it
// is present. The test drives a miniredis and a FakeHost, so it reaches no network and
// starts no redis server of its own.

// newCISource starts a miniredis and returns a CI source over it, plus the server the test
// writes keys into.
func newCISource(t *testing.T) (*miniredis.Miniredis, CISource, context.Context) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	return mr, NewRedisCI(rdb), context.Background()
}

func TestLanderReadsCIFromRedisNeverCheckRuns(t *testing.T) {
	ctx := context.Background()
	repo := "mas-bandwidth/nova-tools"
	head := strings.Repeat("a", 40)

	// The fixture PR, and the green check-run the old lander read and admitted on. The new
	// lander must not read it: it is set here only to prove the premise, that a green
	// check-run is present and is not what the lander decides on.
	host := NewFakeHost()
	host.PRs[7] = PR{Number: 7, HeadRef: "rowan/integration-6", HeadOID: head, Mergeable: "MERGEABLE"}
	host.SetCheckRuns(head, CheckDetail{Name: "ci-ok", Conclusion: "success", SHA: head})
	checks, err := host.Checks(head)
	if err != nil {
		t.Fatalf("fixture: the fake host could not answer checks: %v", err)
	}
	if checks.Verdict() != "GREEN" {
		t.Fatalf("fixture: want a green check-run to prove the lander ignores it, got %s", checks.Verdict())
	}

	mr, src, _ := newCISource(t)

	// No ci key: refused ci: MISSING, whatever the check-run said.
	v, err := ReadLanderCI(ctx, src, repo, head)
	if err != nil {
		t.Fatalf("ReadLanderCI: %v", err)
	}
	if !v.Missing() || v.Batches() {
		t.Fatalf("no ci key => ci: MISSING (refused), got state=%q raw=%q", v.State, v.Raw)
	}

	// ci:<repo>:<head> = OK: it batches.
	if err := mr.Set("ci:"+repo+":"+head, "OK"); err != nil {
		t.Fatalf("miniredis set: %v", err)
	}
	v, err = ReadLanderCI(ctx, src, repo, head)
	if err != nil {
		t.Fatalf("ReadLanderCI: %v", err)
	}
	if !v.Batches() || v.State != "OK" {
		t.Fatalf("ci:<repo>:<head> = OK => OK (batches), got state=%q raw=%q", v.State, v.Raw)
	}

	// A ci key that is not OK is a refusal, never a fallback to the green check-run.
	if err := mr.Set("ci:"+repo+":"+head, "FAILURE"); err != nil {
		t.Fatalf("miniredis set: %v", err)
	}
	v, err = ReadLanderCI(ctx, src, repo, head)
	if err != nil {
		t.Fatalf("ReadLanderCI: %v", err)
	}
	if v.Batches() || v.State != "FAILURE" {
		t.Fatalf("ci:<repo>:<head> = FAILURE => refusal, got state=%q raw=%q", v.State, v.Raw)
	}
}
