package consume

// runner_test.go is control 33 of #2756 (10.7, 10.8.3) as nova-tools #3040
// rev 4 names it: the pr-to-read rule of `nova-sprint route` copies runner-only
// check rows from ev:github into the GID key ci:<repo>:<head>:<gid>, keeps the
// highest attempt key (gen, check_run_id, status rank, at) with rerequested
// as the rank -1 sentinel of a new generation, and adopts once every sprint PR
// no card produced. The store is a throwaway redis-server with the nova_sprint
// library loaded; no test opens a socket to any other host.

import (
	"context"
	"errors"
	"testing"
)

type fakeHalf struct {
	started bool
	n       int
	err     error
}

func (h *fakeHalf) Start(context.Context) error { h.started = true; return nil }

func (h *fakeHalf) Pass(context.Context) (int, error) { return h.n, h.err }

// The one pr-to-read slot runs both halves: every half starts, the counts
// add, and a hard error wins over a retryable one.
func TestJoinPRToRead(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	a, b := &fakeHalf{n: 2}, &fakeHalf{n: 3}
	j := JoinPRToRead(a, nil, b)
	must(t, j.Start(ctx))
	if !a.started || !b.started {
		t.Fatalf("started a=%v b=%v, want both", a.started, b.started)
	}
	if n, err := j.Pass(ctx); n != 5 || err != nil {
		t.Fatalf("pass = %d %v, want 5 nil", n, err)
	}
	hard := errors.New("hard")
	a.err, b.err = ErrNoReaders, hard
	if _, err := j.Pass(ctx); !errors.Is(err, hard) {
		t.Fatalf("pass err %v, want the hard error", err)
	}
	b.err = nil
	if _, err := j.Pass(ctx); !errors.Is(err, ErrNoReaders) {
		t.Fatalf("pass err %v, want ErrNoReaders", err)
	}
}
