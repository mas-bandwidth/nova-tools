package ws_test

import (
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ws"
	"github.com/redis/go-redis/v9"
)

// TestEpochKeyRule (nova-tools#4238): the one naming rule, epoch 0 is the
// name every set had before the first clear and epoch e puts e in the
// name, so two epochs never share a set; the same rule as cm_ckey,
// cm_wskey and cm_skey in fn/lua/02_card_move.lua.
func TestEpochKeyRule(t *testing.T) {
	t.Parallel()
	for _, c := range []struct{ got, want string }{
		{ws.KeyAt(0, "swarm: cards", "ready"), "ws:swarm: cards:ready"},
		{ws.KeyAt(0, "swarm: cards", "ready"), ws.KeyAt(0, "swarm: cards", "ready")},
		{ws.KeyAt(1, "swarm: cards", "ready"), "ws:1:swarm: cards:ready"},
		{ws.KeyAt(42, "docs", "landed"), "ws:42:docs:landed"},
		{ws.ConsumerKeyAt(0, "bench:space", "done"), "bench:space:cards:done"},
		{ws.ConsumerKeyAt(1, "bench:space", "done"), "bench:space:1:cards:done"},
		{ws.ConsumerKeyAt(7, "friend:emma", "ok"), "friend:emma:7:cards:ok"},
		{ws.SprintListAt(0, "fix", "pool"), "s:fix:pool"},
		{ws.SprintListAt(3, "fix", "pool"), "s:fix:3:pool"},
		{ws.SprintListAt(3, "fix", "waiting"), "s:fix:3:waiting"},
	} {
		if c.got != c.want {
			t.Errorf("key %q, want %q", c.got, c.want)
		}
	}
	seen := map[string]bool{}
	for e := uint64(0); e < 4; e++ {
		for _, k := range []string{ws.KeyAt(e, "s", "ready"), ws.ConsumerKeyAt(e, "bench:b", "ready"), ws.SprintListAt(e, "S", "pool")} {
			if seen[k] {
				t.Fatalf("epoch %d shares a set name with another epoch: %s", e, k)
			}
			seen[k] = true
		}
	}
}

// TestParseEpoch: an absent field is 0; anything but a uint64 is an error
// (a reader must not show epoch 0's cells for a junk value).
func TestParseEpoch(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		in   string
		want uint64
		bad  bool
	}{
		{"", 0, false}, {"0", 0, false}, {"1", 1, false}, {"18446744073709551615", 18446744073709551615, false},
		{"-1", 0, true}, {"x", 0, true}, {"1.5", 0, true},
	} {
		got, err := ws.ParseEpoch(c.in)
		if (err != nil) != c.bad || got != c.want {
			t.Errorf("ParseEpoch(%q) = %d, %v; want %d, bad=%v", c.in, got, err, c.want, c.bad)
		}
	}
}

// TestZsReadsMemberScorePairs: the ns_ws_zrange withscores reply is a flat
// member, score list; an odd list or a junk score is an error, never a
// silently short slice.
func TestZsReadsMemberScorePairs(t *testing.T) {
	t.Parallel()
	cmd := redis.NewCmd(nil)
	cmd.SetVal([]any{"a", "1", "b", "2.5"})
	zs, err := ws.Zs(cmd)
	if err != nil || len(zs) != 2 || zs[0].Member != "a" || zs[0].Score != 1 || zs[1].Member != "b" || zs[1].Score != 2.5 {
		t.Fatalf("zs=%v err=%v", zs, err)
	}
	cmd = redis.NewCmd(nil)
	cmd.SetVal([]any{"a"})
	if _, err := ws.Zs(cmd); err == nil {
		t.Fatal("an odd list is not an error")
	}
	cmd = redis.NewCmd(nil)
	cmd.SetVal([]any{"a", "x"})
	if _, err := ws.Zs(cmd); err == nil {
		t.Fatal("a junk score is not an error")
	}
}
