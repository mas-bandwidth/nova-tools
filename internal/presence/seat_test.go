package presence_test

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
	"github.com/mas-bandwidth/nova-tools/internal/presence"
	"github.com/mas-bandwidth/nova-tools/internal/seatcred"
	"github.com/mas-bandwidth/nova-tools/internal/seatcred/seattest"
)

// TestOpenLogsInAsTheSeat is #4052 on nova-wake: with a seat selected, Open
// dials the fleet shape (default user off) as the seat's Redis user, with no
// NOVA_REDIS_BENCH_PASSWORD in the environment and none left there after.
func TestOpenLogsInAsTheSeat(t *testing.T) {
	const pw = "wake-seat-test-pw-4052"
	home := seattest.Home(t, "swarm-y", map[string]string{"NOVA_REDIS_BENCH_PASSWORD": pw})
	addr := testutil.Start(t, "--user", "default", "off", "--user", "bench", "on", ">"+pw, "~*", "&*", "+@all")
	seattest.Env(t, home)
	t.Setenv(presence.PasswordEnv, "")
	ctx := context.Background()

	// Open sends nothing (#3277, #4026): the first command carries the NOAUTH
	// refusal, and it names --seat.
	r0, err := presence.Open(ctx, addr, "")
	if err != nil {
		t.Fatalf("Open with no seat and no password: %v; want no error before the first command", err)
	}
	if _, err := r0.Members(ctx, "friends"); err == nil || !strings.Contains(err.Error(), "--seat") {
		t.Fatalf("first command with no seat and no password = %v; want a NOAUTH refusal naming --seat", err)
	}
	_ = r0.Close()
	seatcred.Select("swarm-y")
	r, err := presence.Open(ctx, addr, "")
	if err != nil {
		t.Fatalf("Open as seat swarm-y: %v", err)
	}
	_ = r.Close()
	if u, _, err := presence.Login("ignored"); err != nil || u != "bench" {
		t.Fatalf("Login under the seat = %q, %v; want bench", u, err)
	}
	for _, kv := range os.Environ() {
		if strings.Contains(kv, pw) {
			t.Fatal("the password entered this process's environment")
		}
	}
}
