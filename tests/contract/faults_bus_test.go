//go:build functional

package contract

import (
	"bufio"
	"fmt"
	"net"
)

var busSend = []string{"send", "--as", "a", "--to", "b", "subject", "body"}

func busRedis(e *Env, addr string) { e.Set("NOVA_BUS_REDIS", addr) }

func busDead(e *Env)   { busRedis(e, e.Dead()) }
func busFrozen(e *Env) { busRedis(e, e.Frozen()) }
func busAuth(e *Env)   { busRedis(e, e.Redis("--requirepass", "pw")) }
func busWrongUser(e *Env) {
	busRedis(e, e.Redis("--user", "alice", "on", ">good", "~*", "&*", "+@all", "--user", "default", "off"))
	e.Set("NOVA_BUS_USER", "alice")
	e.Set("NOVA_BUS_PASSWORD", "bad")
}
func busUp(e *Env) { busRedis(e, e.Redis()) }

// busSenderHooked is a Redis where the sender a holds its name as a live hook does (the
// presence key, bus guide), so a send gets past the sender check to the recipient's.
func busSenderHooked(e *Env) {
	addr := e.Redis()
	busRedis(e, addr)
	c, err := net.Dial("tcp", addr)
	if err != nil {
		e.t.Fatal(err)
	}
	defer c.Close()
	key, val := "bus:presence:a", "2026-11-08T12:00:00Z\tplanted"
	fmt.Fprintf(c, "*5\r\n$3\r\nSET\r\n$%d\r\n%s\r\n$%d\r\n%s\r\n$2\r\nPX\r\n$5\r\n60000\r\n", len(key), key, len(val), val)
	if r, err := bufio.NewReader(c).ReadString('\n'); err != nil || r != "+OK\r\n" {
		e.t.Fatalf("SET %s: %q %v", key, r, err)
	}
}

func init() {
	cannot := Want{Reason: []string{"redis"}}
	auth := Want{Reason: []string{"NOVA_BUS_PASSWORD|NOVA_BUS_USER"}}
	register(
		Fault{Tool: "bus", Name: "redis address unset", Argv: busSend,
			Want: Want{Reason: []string{"NOVA_BUS_REDIS"}}},
		Fault{Tool: "bus", Name: "user without password", Setup: func(e *Env) { busDead(e); e.Set("NOVA_BUS_USER", "u") }, Argv: []string{"who"},
			Want: Want{Reason: []string{"NOVA_BUS_PASSWORD"}}},
		Fault{Tool: "bus", Name: "send: redis stopped", Setup: busDead, Argv: busSend, Want: cannot, Known: "bus-redis-refusal"},
		Fault{Tool: "bus", Name: "who: redis stopped", Setup: busDead, Argv: []string{"who"}, Want: cannot, Known: "bus-redis-refusal"},
		Fault{Tool: "bus", Name: "pending: redis stopped", Setup: busDead, Argv: []string{"pending", "--as", "a"}, Want: cannot, Known: "bus-redis-refusal"},
		Fault{Tool: "bus", Name: "send: redis frozen", Setup: busFrozen, Argv: busSend, Want: cannot, Known: "bus-redis-refusal"},
		Fault{Tool: "bus", Name: "who: redis wants a password", Setup: busAuth, Argv: []string{"who"}, Want: auth, Known: "bus-redis-refusal"},
		Fault{Tool: "bus", Name: "send: redis wants a password", Setup: busAuth, Argv: busSend, Want: auth, Known: "bus-redis-refusal"},
		Fault{Tool: "bus", Name: "who: wrong password", Setup: busWrongUser, Argv: []string{"who"}, Want: auth, Known: "bus-redis-refusal"},
		Fault{Tool: "bus", Name: "send: wrong password", Setup: busWrongUser, Argv: busSend, Want: auth, Known: "bus-redis-refusal"},
		Fault{Tool: "bus", Name: "hook: wrong password", Setup: busWrongUser, Argv: []string{"hook", "--as", "a"},
			Want: Want{Reason: []string{"refused the login", "NOVA_BUS_PASSWORD"}, Remedy: Runs}},
		Fault{Tool: "bus", Name: "send: sender never hooked in", Setup: busUp, Argv: busSend,
			Want: Want{Reason: []string{"hook"}}},
		Fault{Tool: "bus", Name: "pending: name never hooked in", Setup: busUp, Argv: []string{"pending", "--as", "ghost"},
			Want: Want{Reason: []string{"hook"}}},
		Fault{Tool: "bus", Name: "send: recipient never read", Setup: busSenderHooked, Argv: busSend,
			Want: Want{Reason: []string{"no hook has ever held"}}, Known: "bus-never-held"},
		Fault{Tool: "bus", Name: "send: nothing named", Setup: busUp, Argv: []string{"send"},
			Want: Want{Reason: []string{"--as", "--to"}}},
		Fault{Tool: "bus", Name: "hook: no name", Setup: busUp, Argv: []string{"hook"}, Want: Want{Reason: []string{"--as"}}},
		Fault{Tool: "bus", Name: "hook: unknown harness", Setup: busUp, Argv: []string{"hook", "--as", "a", "--harness", "nope"},
			Want: Want{Reason: []string{"harness"}}},
		Fault{Tool: "bus", Name: "hook: grok flags missing", Setup: busUp, Argv: []string{"hook", "--as", "a", "--harness", "grok"},
			Want: Want{Reason: []string{"--grok-home"}}},
		Fault{Tool: "bus", Name: "hook: stdout is /dev/null", Setup: func(e *Env) { busUp(e); e.DevNull() }, Argv: []string{"hook", "--as", "a"},
			Want: Want{Reason: []string{"/dev/null"}}},
		Fault{Tool: "bus", Name: "hook: codex thread with no rollout", Setup: busUp, Argv: []string{"hook", "--as", "a", "--codex-thread", "019a5c1e-0000-7000-8000-000000000000"},
			Want: Want{Reason: []string{"no rollout for thread", "--codex-thread"}}},
	)
}
