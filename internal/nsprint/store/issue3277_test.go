package store_test

import (
	"context"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
	"github.com/mas-bandwidth/nova-tools/internal/seatcred"
)

// #3277: Open and OpenSingle send nothing; the caller's first batch is the
// probe, so a one-operation verb pays one round trip, not two.
func TestOpenSendsNoCommand3277(t *testing.T) {
	t.Parallel()
	addr, count := testutil.CommandCounter(t)
	anon := seatcred.Anonymous()
	for name, open := range map[string]func(context.Context, string) (*store.Store, error){
		"Open": func(ctx context.Context, addr string) (*store.Store, error) {
			return store.OpenSeat(ctx, addr, anon)
		},
		"OpenSingle": func(ctx context.Context, addr string) (*store.Store, error) {
			return store.OpenSingleSeat(ctx, addr, anon)
		},
	} {
		s, err := open(context.Background(), addr)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if n := count(); n != 0 {
			t.Fatalf("%s sent %d commands before the caller's first batch; want 0", name, n)
		}
		_ = s.Close()
	}
}
