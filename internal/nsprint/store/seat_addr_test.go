package store

import (
	"context"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/seatcred"
)

// TestOpenFallsBackToTheSeatsAddress (nova-tools#4330): a verb that names no
// address dials the one its seat's profile row names, as that seat's user;
// with no seat it is refused as before, and an address the verb names wins.
// open sends nothing, so no Redis runs.
func TestOpenFallsBackToTheSeatsAddress(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	var sel seatcred.Selection
	if _, err := open(ctx, "", 0, &sel); err == nil || !strings.Contains(err.Error(), "redis address is required") {
		t.Fatalf("open with no address and no seat: %v; want the refusal", err)
	}
	sel.SelectWith("coordinator", "seat.invalid:6380", func(s string) (seatcred.Cred, error) {
		return seatcred.Cred{Seat: s, User: "coordinator"}, nil
	})
	st, err := open(ctx, "", 0, &sel)
	if err != nil {
		t.Fatalf("open with no address under a seat row: %v", err)
	}
	defer st.Close()
	if o := st.Client().Options(); o.Addr != "seat.invalid:6380" || o.Username != "coordinator" {
		t.Fatalf("dials %s as %q; want the row's address as coordinator", o.Addr, o.Username)
	}
	st2, err := open(ctx, "named.invalid:1", 0, &sel)
	if err != nil {
		t.Fatal(err)
	}
	defer st2.Close()
	if a := st2.Client().Options().Addr; a != "named.invalid:1" {
		t.Fatalf("an address the verb names lost to the row: %s", a)
	}
}
