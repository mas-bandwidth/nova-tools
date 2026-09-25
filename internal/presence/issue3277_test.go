package presence

import (
	"context"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
)

// #3277: Open sends nothing; the first beat or read dials and authenticates.
func TestOpenSendsNoCommand3277(t *testing.T) {
	t.Setenv(PasswordEnv, "")
	addr, count := testutil.CommandCounter(t)
	r, err := Open(context.Background(), addr, DefaultUser)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer r.Close()
	if n := count(); n != 0 {
		t.Fatalf("Open sent %d commands before the caller's first batch; want 0", n)
	}
}
