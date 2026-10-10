package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A completed merge must enter the serial landing without waiting for an
// unrelated gate. The fourth gate only returns when this test releases it.
func TestThreeGreenStreamsLandWhileFourthGateHangs(t *testing.T) {
	t.Parallel()
	r := newLandRig(t)
	r.git(r.worker, "switch", "-q", "--detach", "origin/main")
	r.files("the module", goModule)
	r.git(r.worker, "push", "-q", "origin", "HEAD:refs/heads/main", "HEAD:refs/heads/base2", "HEAD:refs/heads/base3", "HEAD:refs/heads/base4")
	r.git(r.worker, "fetch", "-q", "origin")
	for _, base := range []string{"base2", "base3", "base4"} {
		r.moveBase(base, base+".txt")
	}
	r.git(r.worker, "fetch", "-q", "origin")
	heads := map[string]string{}
	for _, row := range []struct{ stream, base string }{{"s1", "main"}, {"s2", "base2"}, {"s3", "base3"}, {"s4", "base4"}} {
		r.promotionStream(row.stream)
		id := row.stream + "-1"
		brief := filepath.Join(t.TempDir(), id+".md")
		require.NoError(t, os.WriteFile(brief, []byte(passingBrief("REPO: "+r.remote+"\nBASE: "+row.base+"\n\nWrite "+id+".")), 0o600))
		r.ok("add --stream " + row.stream + " --one --brief-file " + brief)
		heads[id] = r.head(id, row.base, row.stream+".go", "package main\n\nfunc "+row.stream+"() {}\n")
	}
	r.queued(heads, "s1-1", "s2-1", "s3-1", "s4-1")
	blocked, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	b := r.a.landState()
	b.mu.Lock()
	b.gateBench = func(ctx context.Context, _, dir string, _ [][]string, _ bool) (string, int, error) {
		if strings.HasSuffix(dir, "@s1") {
			once.Do(func() { close(blocked) })
			select {
			case <-release:
				return "", 0, nil
			case <-ctx.Done():
				return "", 1, ctx.Err()
			}
		}
		return "", 0, nil
	}
	b.mu.Unlock()
	done := make(chan struct{})
	var code int
	var out, errs string
	go func() { code, out, errs = r.do("land --land-parallel 4"); close(done) }()
	select {
	case <-blocked:
	case <-t.Context().Done():
		t.Fatal("the first gate never began")
	}
	// The later green streams must be committed while s1 still holds its gate.
	// places reads the coordinator's committed state, so it also checks the
	// report phase rather than merely seeing a pre-push hook.
	for {
		places := r.places("s2-1", "s3-1", "s4-1")
		if places["s2-1"] == "landed/merged" && places["s3-1"] == "landed/merged" && places["s4-1"] == "landed/merged" {
			break
		}
		select {
		case <-t.Context().Done():
			t.Fatalf("three green streams did not land while the fourth gate hung: %v", places)
		default:
		}
	}
	close(release)
	select {
	case <-done:
	case <-t.Context().Done():
		t.Fatal("land did not finish")
	}
	assert.Zero(t, code, "%s%s", out, errs)
	assert.Contains(t, out, "landed_at=")
	r.clean()
}

func TestGateBoundCancelsOnlyItsGate(t *testing.T) {
	t.Parallel()
	tick := make(chan time.Time, 1)
	l := &lander{gateBound: 3 * time.Minute, a: &app{after: func(time.Duration) <-chan time.Time { return tick }}}
	ctx, cancel := l.boundGate(context.Background())
	defer cancel(nil)
	assert.Equal(t, 3*time.Minute, l.gateBoundValue())
	tick <- time.Time{}
	select {
	case <-ctx.Done():
	case <-t.Context().Done():
		t.Fatal("gate bound did not cancel gate context")
	}
	assert.ErrorIs(t, context.Cause(ctx), errGateBound)
}
