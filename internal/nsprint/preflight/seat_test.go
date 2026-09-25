package preflight

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/redis/go-redis/v9"
)

// #3320: preflight opens through store.Open, so it authenticates as the seat
// named by NOVA_SPRINT_REDIS_USER with the password in the variable
// NOVA_SPRINT_REDIS_PASSWORD_ENV names, like every other nova-sprint verb.
func TestPreflightUsesSeatUser(t *testing.T) {
	ctx := context.Background()
	mr := miniredis.RunT(t)
	mr.RequireUserAuth("coordinator", "seat-secret")

	t.Run("named seat gets past AUTH", func(t *testing.T) {
		t.Setenv(store.UserEnv, "coordinator")
		t.Setenv(store.PasswordEnvEnv, "NOVA_TEST_PREFLIGHT_SEAT_PASSWORD")
		t.Setenv("NOVA_TEST_PREFLIGHT_SEAT_PASSWORD", "seat-secret")
		t.Setenv(store.DefaultPasswordEnv, "")
		c, err := Open(ctx, mr.Addr())
		if err != nil {
			t.Fatalf("open as the seat: %v", err)
		}
		defer c.Close()
		if got := c.Options().Username; got != "coordinator" {
			t.Fatalf("preflight authenticated as %q, want the seat coordinator", got)
		}
		if err := c.Set(ctx, "k", "v", 0).Err(); err != nil {
			t.Fatalf("command as the seat: %v", err)
		}
	})

	t.Run("no seat refuses and names the coordinator seat", func(t *testing.T) {
		t.Setenv(store.UserEnv, "")
		t.Setenv(store.DefaultPasswordEnv, "seat-secret")
		c, err := Open(ctx, mr.Addr())
		if err == nil {
			c.Close()
			t.Fatal("open with no seat user must refuse against an ACL Redis")
		}
		for _, want := range []string{store.UserEnv, "coordinator", "default user"} {
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("refusal %q does not name %q", err, want)
			}
		}
	})

	t.Run("seat user with an empty password refuses", func(t *testing.T) {
		t.Setenv(store.UserEnv, "coordinator")
		t.Setenv(store.PasswordEnvEnv, "NOVA_TEST_PREFLIGHT_SEAT_PASSWORD")
		t.Setenv("NOVA_TEST_PREFLIGHT_SEAT_PASSWORD", "")
		if c, err := Open(ctx, mr.Addr()); err == nil {
			c.Close()
			t.Fatal("empty seat password must refuse")
		}
	})
}

// denyHook answers INFO and FUNCTION the way the fleet's bench ACL does.
type denyHook struct{}

func (denyHook) DialHook(next redis.DialHook) redis.DialHook { return next }

func (denyHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		if denied(cmd) {
			cmd.SetErr(noperm(cmd))
			return cmd.Err()
		}
		return next(ctx, cmd)
	}
}

func (denyHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error {
		var first error
		for _, cmd := range cmds {
			if denied(cmd) {
				cmd.SetErr(noperm(cmd))
				if first == nil {
					first = cmd.Err()
				}
			}
		}
		if first != nil {
			return first
		}
		return next(ctx, cmds)
	}
}

func denied(cmd redis.Cmder) bool {
	name := strings.ToLower(cmd.Name())
	return name == "info" || name == "function"
}

func noperm(cmd redis.Cmder) error {
	return errors.New("NOPERM User bench has no permissions to run the '" + strings.ToLower(cmd.Name()) + "' command")
}

// #3320 second wall: as the bench seat, INFO and FUNCTION LIST are refused
// NOPERM. 7.1 says it needs the preflight ACL user, once, instead of three
// raw refusals, and every other check still runs.
func TestPreflightInfoNeedsPreflightUser(t *testing.T) {
	ctx := context.Background()
	mr := miniredis.RunT(t)
	mr.SetTime(t0)
	c := redis.NewClient(&redis.Options{Addr: mr.Addr(), Username: "bench"})
	defer c.Close()
	c.AddHook(denyHook{})

	l := checkRedis(ctx, c)
	if !l.Red || l.N != "7.1" {
		t.Fatalf("7.1 as the bench seat must be RED: %s", l)
	}
	if !strings.Contains(l.Why, "needs the preflight ACL user") || !strings.Contains(l.Why, store.UserEnv+"=coordinator") {
		t.Fatalf("7.1 does not say it needs the preflight ACL user: %s", l)
	}
	if strings.Count(l.Why, "NOPERM") > 1 || strings.Contains(l.Why, "cannot read INFO") {
		t.Fatalf("7.1 repeats the raw refusals: %s", l)
	}

	lines := StoreChecks(ctx, c, Options{Sprint: "control-00003320"})
	var got []string
	for _, line := range lines {
		got = append(got, line.N)
	}
	if strings.Join(got, " ") != "7.1 7.2 7.3 7.7 7.10 7.15" {
		t.Fatalf("a NOPERM 7.1 must not stop the other checks: %v", got)
	}
	for _, line := range lines {
		if line.N != "7.1" && strings.Contains(line.Why, "NOPERM") {
			t.Fatalf("check %s saw NOPERM: %s", line.N, line)
		}
	}
}

// DONE-WHEN test for #3190: a fake store answering NOPERM to INFO, FUNCTION LIST or TIME
// makes 7.1 print RED 7.1 with MISSING and the refused command, never GREEN, and exit 1.
func TestStoreCheckNoPermIsRedNamingTheCommand(t *testing.T) {
	ctx := context.Background()
	mr := miniredis.RunT(t)
	mr.SetTime(t0)

	for _, deniedCmd := range []string{"info", "function", "time"} {
		t.Run(deniedCmd, func(t *testing.T) {
			c := redis.NewClient(&redis.Options{Addr: mr.Addr(), Username: "bench"})
			defer c.Close()
			c.AddHook(specificDenyHook{cmd: deniedCmd})

			l := checkRedis(ctx, c)
			if !l.Red || l.N != "7.1" {
				t.Fatalf("7.1 with %s denied must be RED: %s", deniedCmd, l)
			}
			if !strings.Contains(l.Why, "MISSING") {
				t.Fatalf("7.1 Why must contain MISSING: %s", l)
			}
			if !strings.Contains(strings.ToLower(l.Why), strings.ToLower(deniedCmd)) {
				t.Fatalf("7.1 Why must name the refused command %q: %s", deniedCmd, l)
			}
			if ExitCode([]Line{l}) != 1 {
				t.Fatal("RED 7.1 must exit 1")
			}
			if strings.Contains(l.String(), "GREEN") {
				t.Fatal("7.1 must never be GREEN when denied")
			}
		})
	}
}

type specificDenyHook struct {
	cmd string
}

func (specificDenyHook) DialHook(next redis.DialHook) redis.DialHook { return next }

func (d specificDenyHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		if d.denied(cmd) {
			cmd.SetErr(noperm(cmd))
			return cmd.Err()
		}
		return next(ctx, cmd)
	}
}

func (d specificDenyHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error {
		var first error
		for _, cmd := range cmds {
			if d.denied(cmd) {
				cmd.SetErr(noperm(cmd))
				if first == nil {
					first = cmd.Err()
				}
			}
		}
		if first != nil {
			return first
		}
		return next(ctx, cmds)
	}
}

func (d specificDenyHook) denied(cmd redis.Cmder) bool {
	name := strings.ToLower(cmd.Name())
	return name == d.cmd || (d.cmd == "function|list" && name == "function")
}
