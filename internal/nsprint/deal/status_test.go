package deal

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
)

// The fleet ACL copy (rowan-tools fleet/redis.yml, as control 6 of #2380
// reads it): the bench line is the seat every bench holds, FCALL and
// FCALL_RO but no scripting.
const statusFleetACL = "../sprint/testdata/plan/acl.txt"

// benchSeat starts a throwaway redis-server, loads the nova_sprint library as
// its owner (the admin client), adds the fleet bench user from acl.txt, and
// returns a client dialled as that seat plus the admin client.
func benchSeat(t *testing.T) (bench, admin *redis.Client) {
	t.Helper()
	ctx := context.Background()
	addr := testutil.Start(t)
	admin = redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = admin.Close() })
	body, err := os.ReadFile(statusFleetACL)
	if err != nil {
		t.Fatal(err)
	}
	var rules []string
	for _, line := range strings.Split(string(body), "\n") {
		if name, r, ok := strings.Cut(line, "\t"); ok && name == "bench" {
			rules = strings.Fields(r)
		}
	}
	if len(rules) == 0 {
		t.Fatalf("%s has no bench line", statusFleetACL)
	}
	setuser := []any{"ACL", "SETUSER", "bench", "reset", "on", ">bench-status-pw"}
	for _, r := range rules {
		setuser = append(setuser, r)
	}
	if err := admin.Do(ctx, setuser...).Err(); err != nil {
		t.Fatal(err)
	}
	if err := fn.Load(ctx, admin); err != nil {
		t.Fatal(err)
	}
	bench = redis.NewClient(&redis.Options{Addr: addr, Username: "bench", Password: "bench-status-pw"})
	t.Cleanup(func() { _ = bench.Close() })
	// The control needs a seat that really has no scripting grant.
	if err := bench.Do(ctx, "eval", "return 1", "0").Err(); err == nil || !strings.Contains(err.Error(), "NOPERM") {
		t.Fatalf("fleet bench seat ran a script (err %v); the control needs a seat without one", err)
	}
	if err := admin.ConfigResetStat(ctx).Err(); err != nil {
		t.Fatal(err)
	}
	return bench, admin
}

// commandCounts reads the server's own command counters: calls per command
// name, from every client.
func commandCounts(t *testing.T, ctx context.Context, admin *redis.Client) map[string]string {
	t.Helper()
	info, err := admin.Info(ctx, "commandstats").Result()
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, line := range strings.Split(info, "\n") {
		name, rest, ok := strings.Cut(strings.TrimSpace(line), ":")
		if ok && strings.HasPrefix(name, "cmdstat_") {
			out[strings.TrimPrefix(name, "cmdstat_")] = rest
		}
	}
	return out
}

// TestDealStatusListUsesFcallOnly is the #3605 control: `deal status` list
// mode, run as the fleet bench seat (FCALL/FCALL_RO, no scripting), prints
// one row per registered bench in name order and the sprint line, through
// ns_deal_status_list called with FCALL_RO; the server counts no script,
// SCRIPT or FUNCTION call from any client. At dev 8d12fb19 list mode sent a
// script and the seat was refused NOPERM.
func TestDealStatusListUsesFcallOnly(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	bench, admin := benchSeat(t)
	const S = "control-3605"
	seed := admin.Pipeline()
	seed.SAdd(ctx, "benches", "studio", "hulk", "superman")
	seed.Set(ctx, "bench:studio:beat", "1", 0)
	seed.Set(ctx, "bench:hulk:beat", "1", 0)
	seed.HSet(ctx, "bench:studio:desired", "slots", "8", "paused", "0")
	seed.HSet(ctx, "bench:hulk:desired", "slots", "4", "paused", "1")
	seed.ZAdd(ctx, "bench:studio:living", redis.Z{Score: 1, Member: "c1"}, redis.Z{Score: 2, Member: "c2"})
	seed.ZAdd(ctx, "bench:studio:starting", redis.Z{Score: 1, Member: "c3"})
	seed.ZAdd(ctx, "s:"+S+":bench:studio:queue", redis.Z{Score: 1, Member: "q1"})
	seed.ZAdd(ctx, "s:"+S+":pool", redis.Z{Score: 1, Member: "p1"}, redis.Z{Score: 2, Member: "p2"})
	seed.SAdd(ctx, "s:"+S+":waiting", "w1")
	if _, err := seed.Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if err := admin.ConfigResetStat(ctx).Err(); err != nil {
		t.Fatal(err)
	}

	lines, err := Status(ctx, bench, S, "")
	if err != nil {
		t.Fatalf("list mode as the bench seat: %v", err)
	}
	want := []string{
		"bench hulk verdict=paused free=4 starting=0 living=0 queue=0 ssh=- ssh_age=-",
		"bench studio verdict=ok free=5 starting=1 living=2 queue=1 ssh=- ssh_age=-",
		"bench superman verdict=down free=0 starting=0 living=0 queue=0 ssh=- ssh_age=-",
		"sprint " + S + " pool=2 waiting=1 dealt=0 last_deal_age=-",
	}
	if strings.Join(lines, "\n") != strings.Join(want, "\n") {
		t.Fatalf("list mode lines:\n%s\nwant:\n%s", strings.Join(lines, "\n"), strings.Join(want, "\n"))
	}

	counts := commandCounts(t, ctx, admin)
	for name, stat := range counts {
		if strings.HasPrefix(name, "eval") || strings.HasPrefix(name, "script") || strings.HasPrefix(name, "function") {
			t.Fatalf("list mode sent %s (%s); want FCALL_RO only", name, stat)
		}
	}
	if !strings.HasPrefix(counts["fcall_ro"], "calls=1,") {
		t.Fatalf("fcall_ro stat %q; want exactly one FCALL_RO (ns_deal_status_list)", counts["fcall_ro"])
	}

	// Lookup mode needs no function and stays as it was.
	lines, err = Status(ctx, bench, S, "studio")
	if err != nil || len(lines) != 2 || lines[0] != want[1] {
		t.Fatalf("lookup mode = %q, %v; want %q first", lines, err, want[1])
	}
}

// TestDealStatusListNamesTheConvergeWhenUnloaded: on a server whose owner has
// not loaded the library, list mode refuses naming the function and the
// converge verb, and never loads the library itself.
func TestDealStatusListNamesTheConvergeWhenUnloaded(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	c := redis.NewClient(&redis.Options{Addr: testutil.Start(t)})
	t.Cleanup(func() { _ = c.Close() })
	_, err := Status(ctx, c, "control-3605", "")
	if err == nil || !strings.Contains(err.Error(), StatusFunction) || !strings.Contains(err.Error(), "nova-sprint fn load") {
		t.Fatalf("list mode without the library = %v; want a refusal naming %s and nova-sprint fn load", err, StatusFunction)
	}
	if libs, err := c.FunctionList(ctx, redis.FunctionListQuery{LibraryNamePattern: fn.Library}).Result(); err != nil || len(libs) != 0 {
		t.Fatalf("library after list mode on a bare server = %v, %v; want none loaded", libs, err)
	}
}

// TestDealStatusHelpNamesRedisUser: the verb's help names the environment
// variable that picks the Redis seat (#3605), so a bench operator is not left
// to find NOPERM/NOAUTH by trial.
func TestDealStatusHelpNamesRedisUser(t *testing.T) {
	t.Parallel()

	if !strings.Contains(StatusUsage, store.UserEnv+"=bench") {
		t.Fatalf("deal status help %q does not name %s=bench", StatusUsage, store.UserEnv)
	}
}
