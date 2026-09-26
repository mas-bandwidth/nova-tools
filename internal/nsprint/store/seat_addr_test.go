package store_test

import (
	"context"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/seatcred"
)

// TestOpenFallsBackToTheSeatsAddress (nova-tools#4330): a verb that names no
// address dials the one its seat's profile row names, as that seat's user;
// with no seat it is refused as before. Open sends nothing, so no Redis runs.
func TestOpenFallsBackToTheSeatsAddress(t *testing.T) {
	t.Cleanup(func() { seatcred.Select("") })
	ctx := context.Background()
	if _, err := store.Open(ctx, ""); err == nil || !strings.Contains(err.Error(), "redis address is required") {
		t.Fatalf("Open(\"\") with no seat: %v; want the refusal", err)
	}
	seatcred.SelectWith("coordinator", "10.9.8.7:6380", func(s string) (seatcred.Cred, error) {
		return seatcred.Cred{Seat: s, User: "coordinator"}, nil
	})
	st, err := store.Open(ctx, "")
	if err != nil {
		t.Fatalf("Open(\"\") under a seat row: %v", err)
	}
	defer st.Close()
	if o := st.Client().Options(); o.Addr != "10.9.8.7:6380" || o.Username != "coordinator" {
		t.Fatalf("dials %s as %q; want the row's address as coordinator", o.Addr, o.Username)
	}
	st2, err := store.Open(ctx, "10.0.0.1:1")
	if err != nil {
		t.Fatal(err)
	}
	defer st2.Close()
	if a := st2.Client().Options().Addr; a != "10.0.0.1:1" {
		t.Fatalf("an address the verb names lost to the row: %s", a)
	}
}
