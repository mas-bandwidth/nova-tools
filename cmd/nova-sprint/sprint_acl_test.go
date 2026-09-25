package main

import (
	"context"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/redis/go-redis/v9"
)

// seatAs makes an ACL user on the loopback redis and points the verb's
// store.Open at it (NOVA_SPRINT_REDIS_USER plus a password variable).
func seatAs(t *testing.T, c *redis.Client, user string, rules ...string) {
	t.Helper()
	ctx := context.Background()
	args := []any{"ACL", "SETUSER", user, "reset", "on", ">seat-pw"}
	for _, r := range rules {
		args = append(args, r)
	}
	if err := c.Do(ctx, args...).Err(); err != nil {
		t.Fatalf("ACL SETUSER %s: %v", user, err)
	}
	t.Setenv(store.UserEnv, user)
	t.Setenv(store.PasswordEnvEnv, "SPRINT_ACL_TEST_PW")
	t.Setenv("SPRINT_ACL_TEST_PW", "seat-pw")
}

// TestSprintOpenRestrictedSeat is #3570's DONE-WHEN: `sprint open --from` run
// as a seat whose ACL grants SISMEMBER and SCARD but not SMISMEMBER (the
// command set of every ns-* seat) opens for a registered owner. Before the
// fix the owner read was one SMISMEMBER, which such a seat is refused.
func TestSprintOpenRestrictedSeat(t *testing.T) {
	addr, c := openFixture(t)
	const s = "acl-3570"
	seatAs(t, c, "seat", "~*", "&*", "+@all", "-@dangerous", "-smismember")
	from := writeWorkSet(t, s, unit("u1", fxFriend, ""))
	expect(t, 0, "OPEN "+s+" units=1 pushed=1 existed=0 closed=0 skipped_done=0\n", openArgs(addr, s, from)...)
	if st := hget(t, c, "s:"+s, "status"); st != "open" {
		t.Fatalf("s:%s status=%q; want open", s, st)
	}
	// A name the registry does not hold is still refused, from the same seat.
	other := writeWorkSet(t, s+"-b", unit("u9", "ghost", ""))
	expect(t, 1, "OWNER u9 ghost not in friends\n", openArgs(addr, s+"-b", other)...)
}

// TestSprintOpenOwnerRefusalNamesTheCause is #3570's second DONE-WHEN arm:
// the refusal says which it hit, an empty registry or an ACL that hides it,
// and writes nothing either way.
func TestSprintOpenOwnerRefusalNamesTheCause(t *testing.T) {
	addr, c := openFixture(t)
	ctx := context.Background()
	const s = "acl-3570-cause"
	from := writeWorkSet(t, s, unit("u1", fxFriend, ""))

	// The seat may read s:* and friend:* but not the bare friends set.
	seatAs(t, c, "hidden", "~s:*", "~friend:*", "+@all", "-@dangerous")
	before := dbsize(t, c)
	code, out, errOut := runSprint(openArgs(addr, s, from)...)
	if code != 1 || !strings.HasPrefix(out, "OWNERS "+s+" unchecked: the friends registry read was refused by ACL (NOPERM") ||
		strings.Count(out, "\n") != 1 {
		t.Fatalf("hidden registry: code=%d out=%q stderr=%q; want exit 1 and one OWNERS ... refused by ACL line", code, out, errOut)
	}
	if after := dbsize(t, c); after != before || exists(t, c, "s:"+s) {
		t.Fatalf("hidden registry: DBSIZE %d -> %d or s:%s written; want nothing written", before, after, s)
	}

	// An empty registry, read by a seat that may read it.
	t.Setenv(store.UserEnv, "")
	if err := c.Del(ctx, "friends").Err(); err != nil {
		t.Fatal(err)
	}
	expect(t, 1, "OWNER u1 "+fxFriend+" not in friends: no friends registered yet (friends is empty on this store)\n",
		openArgs(addr, s, from)...)
}

// TestSprintCloseRefusesWhatIsNotOpen is #3571's DONE-WHEN: close of a name
// never opened exits 1 naming the sprint and writes nothing; close of an open
// sprint exits 0 once and is refused the second time.
func TestSprintCloseRefusesWhatIsNotOpen(t *testing.T) {
	addr, c := openFixture(t)
	const never = "never-opened-3571"
	before := dbsize(t, c)
	code, out, errOut := runSprint("sprint", "close", "--redis", addr, "--sprint", never)
	if code != 1 || !strings.HasPrefix(out, "REFUSED "+never+" no such sprint") || strings.Count(out, "\n") != 1 {
		t.Fatalf("close never-opened: code=%d out=%q stderr=%q; want exit 1 REFUSED %s no such sprint", code, out, errOut, never)
	}
	if after := dbsize(t, c); after != before || exists(t, c, "s:"+never) {
		t.Fatalf("close never-opened: DBSIZE %d -> %d or s:%s written; want nothing written", before, after, never)
	}

	const s = "close-3571"
	expect(t, 0, "OPEN "+s+" units=1 pushed=1 existed=0 closed=0 skipped_done=0\n",
		openArgs(addr, s, writeWorkSet(t, s, unit("u1", fxFriend, "")))...)
	expect(t, 0, s+" status=closed\n", "sprint", "close", "--redis", addr, "--sprint", s, "--now", fxLater)
	closedAt := hget(t, c, "s:"+s, "closed_at")
	code, out, errOut = runSprint("sprint", "close", "--redis", addr, "--sprint", s)
	if code != 1 || out != "REFUSED "+s+" already closed (closed_at "+closedAt+"); nothing written; there is no reopen\n" {
		t.Fatalf("second close: code=%d out=%q stderr=%q; want exit 1 REFUSED already closed at %s", code, out, errOut, closedAt)
	}
	if got := hget(t, c, "s:"+s, "closed_at"); got != closedAt {
		t.Fatalf("second close moved closed_at %s -> %s", closedAt, got)
	}
}
