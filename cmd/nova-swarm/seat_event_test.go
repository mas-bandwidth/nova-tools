package main

import (
	"os"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/events"
	"github.com/mas-bandwidth/nova-tools/internal/seatcred"
	"github.com/mas-bandwidth/nova-tools/internal/seatcred/seattest"
)

// TestSeatGivesTheCardEndWriterItsLogin is #4052 on nova-swarm: with --seat the
// card-end writer dials as the seat's Redis user, its password answered from
// the seat's file in memory and never set in the environment the harness
// inherits.
func TestSeatGivesTheCardEndWriterItsLogin(t *testing.T) {
	const pw = "swarm-seat-test-pw-4052"
	home := seattest.Home(t, "swarm-x", map[string]string{"NOVA_REDIS_BENCH_PASSWORD": pw})
	seattest.Env(t, home)
	t.Setenv(events.DefaultPasswordEnv, "")

	if opt := seatEventLogin(events.WriterOptions{Addr: "h:1"}); opt.Lookup != nil || opt.Username != "" {
		t.Fatal("with no seat the writer's options changed")
	}
	if _, err := seatcred.FromArgs([]string{"native", "--seat", "swarm-x"}, os.Getenv); err != nil {
		t.Fatal(err)
	}
	opt := seatEventLogin(events.WriterOptions{Addr: "h:1"})
	if opt.Username != "bench" || opt.Lookup == nil || opt.Lookup(events.DefaultPasswordEnv) != pw {
		t.Fatalf("seat login: user %q; want bench with the seat's password behind the lookup", opt.Username)
	}
	for _, kv := range os.Environ() {
		if strings.Contains(kv, pw) {
			t.Fatal("the password entered this process's environment")
		}
	}
}
