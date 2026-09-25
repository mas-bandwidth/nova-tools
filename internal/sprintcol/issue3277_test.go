package sprintcol

import (
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
)

// #3277: Open sends nothing; the column's first read dials.
func TestOpenSendsNoCommand3277(t *testing.T) {
	addr, count := testutil.CommandCounter(t)
	r, err := Open(addr)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer r.Close()
	if n := count(); n != 0 {
		t.Fatalf("Open sent %d commands before the caller's first batch; want 0", n)
	}
}
