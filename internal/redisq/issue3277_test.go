package redisq_test

import (
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
	"github.com/mas-bandwidth/nova-tools/internal/redisq"
)

// #3277: Open sends nothing; the first queue operation dials.
func TestOpenSendsNoCommand3277(t *testing.T) {
	t.Parallel()

	addr, count := testutil.CommandCounter(t)
	q, err := redisq.Open(addr)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer q.Close()
	if n := count(); n != 0 {
		t.Fatalf("Open sent %d commands before the caller's first batch; want 0", n)
	}
}
