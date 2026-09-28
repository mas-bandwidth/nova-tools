package redisconn

import (
	"context"
	"testing"
)

func TestReviewUnsentCallsDoNotAddWireTrips(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"cancelled command", "cancelled pipeline", "closed client", "refused redial", "failed setup"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			reply := accepting
			if mode == "refused redial" {
				reply = dropOn(1, "ping")
			}
			if mode == "failed setup" {
				reply = func(n int, cmd []string) string {
					if n == 2 && cmd[0] == "hello" {
						return hangUp
					}
					return dropOn(1, "ping")(n, cmd)
				}
			}
			store := newFakeStore(t, reply)
			dial := store.dial
			if mode == "refused redial" {
				dial = thenRefused(store)
			}
			conn, err := open(context.Background(), Options{Addr: storeAddr}, nothing, dial)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := conn.Close(); err != nil {
					t.Error(err)
				}
			})
			ctx := context.Background()
			switch mode {
			case "cancelled command", "cancelled pipeline":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			case "closed client":
				if err := conn.Close(); err != nil {
					t.Fatal(err)
				}
			case "refused redial", "failed setup":
				if err := conn.Client().Ping(ctx).Err(); err == nil {
					t.Fatal("fixture did not drop connection")
				}
			}
			store.commands()
			before := conn.Trips().Total()
			if mode == "cancelled pipeline" {
				p := conn.Client().Pipeline()
				p.Get(ctx, "key")
				_, err = p.Exec(ctx)
			} else {
				err = conn.Client().Get(ctx, "key").Err()
			}
			if err == nil {
				t.Fatal("expected failure")
			}
			sent := store.commands()
			want := int64(0)
			if mode == "failed setup" {
				want = 1
				if len(sent) != 1 {
					t.Fatalf("want only HELLO, got %v", sent)
				}
			} else if len(sent) != 0 {
				t.Fatalf("unsent command reached store: %v", sent)
			}
			if got := conn.Trips().Total() - before; got != want {
				t.Errorf("wire trip delta=%d, want %d; store received %q", got, want, sent)
			}
		})
	}
}
