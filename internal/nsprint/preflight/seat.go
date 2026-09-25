package preflight

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/seatcred"
	"github.com/redis/go-redis/v9"
)

// PreflightSeat is the fleet ACL user preflight runs as. 7.1 reads INFO and
// FUNCTION LIST, which the bench seat is refused (NOPERM) by design (#3320).
const PreflightSeat = "coordinator"

// SeatHint says how to run preflight as the seat that may read everything.
var SeatHint = fmt.Sprintf("preflight runs as the %s seat: %s=%s %s=<the variable holding its password>, under nova-secrets exec --only",
	PreflightSeat, store.UserEnv, PreflightSeat, store.PasswordEnvEnv)

// Open connects through store.Open, the one auth path every nova-sprint verb
// uses (#3195): the ACL user from NOVA_SPRINT_REDIS_USER, its password from
// the variable NOVA_SPRINT_REDIS_PASSWORD_ENV names (NOVA_REDIS_BENCH_PASSWORD
// when unset). A refusal names the seat preflight needs. store.Open sends
// nothing (#3277); preflight is the one verb whose job is the probe, so it
// sends the PING itself and a refused login is refused here, with the seat.
func Open(ctx context.Context, addr string) (*redis.Client, error) {
	st, err := store.Open(ctx, addr)
	if err == nil {
		if err = st.Client().Ping(ctx).Err(); err != nil {
			_ = st.Close()
			err = fmt.Errorf("redis at %s: %w", addr, err)
		}
	}
	if err != nil {
		who := "the default user, because " + store.UserEnv + " is unset"
		if u := os.Getenv(store.UserEnv); u != "" {
			who = "seat " + u
		}
		if s := seatcred.Selected(); s != "" {
			who = "--seat " + s
		}
		return nil, fmt.Errorf("%w (connected as %s); %s", err, who, SeatHint)
	}
	return st.Client(), nil
}

// seatOf names the ACL user a client authenticates as.
func seatOf(c *redis.Client) string {
	if u := c.Options().Username; u != "" {
		return u
	}
	return "default"
}

// isNoPerm reports an ACL refusal of the command itself.
func isNoPerm(err error) bool {
	return err != nil && strings.HasPrefix(strings.TrimPrefix(err.Error(), "ERR "), "NOPERM")
}

// needsSeat is 7.1's line when the ACL refuses INFO or FUNCTION LIST: one
// reason naming the seat, never three raw refusals.
func needsSeat(n, name string, c *redis.Client, err error) Line {
	return Line{N: n, Name: name, Red: true, Why: fmt.Sprintf(
		"needs the preflight ACL user: seat %s may not read INFO or FUNCTION LIST (%s); %s",
		seatOf(c), firstLine(err.Error()), SeatHint)}
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
