package adopt_test

import (
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/adopt"
)

// TestReceiptCheck is the usage gate: each bad receipt names its flag.
func TestReceiptCheck(t *testing.T) {
	t.Parallel()

	good := adopt.Receipt{Verb: "x", Who: "rowan", POV: "bench", State: "adopted"}
	if err := good.Check(); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []struct {
		r    adopt.Receipt
		want string
	}{
		{adopt.Receipt{Who: "rowan", POV: "bench", State: "adopted"}, "--verb"},
		{adopt.Receipt{Verb: "x", POV: "bench", State: "adopted"}, "--as"},
		{adopt.Receipt{Verb: "x", Who: "rowan", POV: "user", State: "adopted"}, "--pov"},
		{adopt.Receipt{Verb: "x", Who: "rowan", POV: "bench", State: "done"}, "--state"},
		{adopt.Receipt{Verb: "x", Who: "rowan", POV: "bench", State: "hack", Gap: "3929"}, "--gap"},
		{adopt.Receipt{Verb: "x\ny", Who: "rowan", POV: "bench", State: "adopted"}, "control"},
	} {
		if err := bad.r.Check(); err == nil || !strings.Contains(err.Error(), bad.want) {
			t.Fatalf("%+v: want %q, got %v", bad.r, bad.want, err)
		}
	}
}
