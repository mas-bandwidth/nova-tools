package pulse

import (
	"context"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
)

// #3277: DialStore sends nothing; the caller's first command dials and
// authenticates.
func TestDialStoreSendsNoCommand3277(t *testing.T) {
	addr, count := testutil.CommandCounter(t)
	rdb, err := DialStore(context.Background(), StoreOptions{Addr: addr, PasswordEnv: "NOVA_TEST_3277_UNSET"})
	if err != nil {
		t.Fatalf("DialStore: %v", err)
	}
	defer rdb.Close()
	if n := count(); n != 0 {
		t.Fatalf("DialStore sent %d commands before the caller's first batch; want 0", n)
	}
}
