package land_test

import (
	"errors"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/capacity"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/land"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
)

// newGateWorker gives bench a land budget and returns a one-slot worker on mirror.
func newGateWorker(t *testing.T, f *landTestFixture, bench, mirror string) *land.Worker {
	t.Helper()
	st, err := store.Open(f.ctx, f.client.Options().Addr)
	if err != nil {
		t.Fatalf("store open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	_ = capacity.SetBudget(f.ctx, st, bench, 4000, 8192, "operator", "")
	_ = f.client.HSet(f.ctx, "bench:"+bench+":desired", "machine", bench).Err()
	_ = f.client.HSet(f.ctx, "bench:"+bench+":land", "cores_go", "2000", "mem_go", "4096").Err()
	return land.NewWorker(land.WorkerConfig{
		Client:    f.client,
		Store:     st,
		Bench:     bench,
		Repos:     []string{f.repo},
		Slots:     1,
		MirrorDir: mirror,
	})
}

// TestDefect7_GateReceiptRepliesAreErrors verifies that the worker treats a receipt the
// function did not write (STALE, NOTFOUND, a transport error) as an error, never drops it.
func TestDefect7_GateReceiptRepliesAreErrors(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		reply string
		err   error
		fails bool
	}{
		{"OK", nil, false},
		{"ALREADY", nil, false},
		{"STALE", nil, true},
		{"NOTFOUND", nil, true},
		{"", errors.New("connection refused"), true},
	} {
		if got := land.GateReceiptErr(tc.reply, tc.err); (got != nil) != tc.fails {
			t.Fatalf("GateReceiptErr(%q, %v) = %v, want error=%v", tc.reply, tc.err, got, tc.fails)
		}
	}
}
