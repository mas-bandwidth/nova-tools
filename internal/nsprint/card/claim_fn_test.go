package card_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/card"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/redis/go-redis/v9"
)

// The bench's Redis user is the fleet's own bench seat (#3551): the rule
// string is the bench line of internal/nsprint/sprint/testdata/plan/acl.txt,
// copied verbatim from the fleet ACL (redis.yml in rowan-tools). That seat may FCALL and
// may not EVAL, EVALSHA or SCRIPT, but its +@write lets FUNCTION LOAD through
// (-@dangerous does not cover it). So the card path must never load the
// library itself: only the owner (ns-deploy) loads, and the controls below
// count every FUNCTION call from any client.
const (
	fleetACL      = "../sprint/testdata/plan/acl.txt"
	benchPassword = "bench-fleet-pw"
)

// fleetBenchRule reads the bench line of the fleet ACL copy: its user name
// and its rule words, exactly as ACL SETUSER takes them.
func fleetBenchRule(t *testing.T) (string, []string) {
	t.Helper()
	body, err := os.ReadFile(fleetACL)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(body), "\n") {
		name, rules, ok := strings.Cut(line, "\t")
		if ok && name == "bench" {
			return name, strings.Fields(rules)
		}
	}
	t.Fatalf("%s has no bench line", fleetACL)
	return "", nil
}

// fcallOnlySprint starts a throwaway Redis, adds the fleet bench user from
// acl.txt, and returns a store dialled as that user plus an admin client for
// seeding and inspection. With loaded, the admin (the library's owner) loads
// the nova_sprint library first, the way ns-deploy converges the fleet's
// server; the bench never loads it. Command counters start from zero after
// the fixture's own probes.
func fcallOnlySprint(t *testing.T, loaded bool) (*store.Store, *redis.Client) {
	t.Helper()
	ctx := context.Background()
	addr := startRedis(t)
	admin := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = admin.Close() })
	benchUser, rules := fleetBenchRule(t)
	setuser := []any{"ACL", "SETUSER", benchUser, "reset", "on", ">" + benchPassword}
	for _, r := range rules {
		setuser = append(setuser, r)
	}
	if err := admin.Do(ctx, setuser...).Err(); err != nil {
		t.Fatal(err)
	}
	if loaded {
		if err := fn.Load(ctx, admin); err != nil {
			t.Fatal(err)
		}
	}
	bench := redis.NewClient(&redis.Options{Addr: addr, Username: benchUser, Password: benchPassword})
	t.Cleanup(func() { _ = bench.Close() })
	for _, probe := range [][]any{
		{"EVAL", "return 1", "0"},
		{"EVALSHA", "0000000000000000000000000000000000000000", "0"},
		{"SCRIPT", "LOAD", "return 1"},
	} {
		if err := bench.Do(ctx, probe...).Err(); err == nil || !strings.Contains(err.Error(), "NOPERM") {
			t.Fatalf("fleet bench seat ran %v (err %v); the control needs a seat with no EVAL and no SCRIPT", probe[:2], err)
		}
	}
	// Whether the seat may FUNCTION LOAD is the fleet ACL's business
	// (rowan-tools: add -function to the bench rule); the card path must not
	// load either way, so this is logged, not required.
	dry, err := admin.Do(ctx, "ACL", "DRYRUN", benchUser, "FUNCTION", "LOAD", "REPLACE", "#!lua name=probe\nredis.register_function('p', function() return 1 end)").Text()
	t.Logf("ACL DRYRUN %s FUNCTION LOAD REPLACE = %q, %v", benchUser, dry, err)
	// The probes above are counted; start the counters from zero.
	if err := admin.ConfigResetStat(ctx).Err(); err != nil {
		t.Fatal(err)
	}
	return store.New(bench), admin
}

// scriptingCalls reads the server's own command counters, so any client on
// any path that sent EVAL, EVALSHA, SCRIPT or FUNCTION (LOAD included) is
// counted, refused or not. FCALL is not in the list.
func scriptingCalls(t *testing.T, ctx context.Context, admin *redis.Client) []string {
	t.Helper()
	info, err := admin.Info(ctx, "commandstats").Result()
	if err != nil {
		t.Fatal(err)
	}
	var hits []string
	for _, line := range strings.Split(info, "\n") {
		name, _, _ := strings.Cut(strings.TrimSpace(line), ":")
		name = strings.TrimPrefix(name, "cmdstat_")
		if strings.HasPrefix(name, "eval") || strings.HasPrefix(name, "script") || strings.HasPrefix(name, "function") {
			hits = append(hits, strings.TrimSpace(line))
		}
	}
	return hits
}

// TestCardClaimIsAFunction is the first DONE-WHEN control of #3551: the
// attempt claim is ns_card_claim in the nova_sprint library, and RedisLedger
// claims through it as the fleet bench seat, which may FCALL but not
// EVAL/EVALSHA, and sends no FUNCTION call: the owner loaded the library, the bench only
// calls it.
func TestCardClaimIsAFunction(t *testing.T) {
	ctx := context.Background()
	st, admin := fcallOnlySprint(t, true)
	libs, err := admin.FunctionList(ctx, redis.FunctionListQuery{LibraryNamePattern: fn.Library}).Result()
	if err != nil {
		t.Fatal(err)
	}
	registered := false
	for _, lib := range libs {
		for _, f := range lib.Functions {
			registered = registered || f.Name == "ns_card_claim"
		}
	}
	if !registered {
		t.Fatalf("ns_card_claim is not a registered function of %s", fn.Library)
	}
	// The admin's FUNCTION LIST above is counted; the bench's calls start at zero.
	if err := admin.ConfigResetStat(ctx).Err(); err != nil {
		t.Fatal(err)
	}

	id := card.Identity{Sprint: "control-claimfn", Label: "card-claim", BaseSHA: "0123abcd", Bench: "wrap-bench", Attempt: 1}
	token := attemptToken(1, fmt.Sprintf("%032x", 3551))
	seedCard(t, ctx, admin, id, "dealt", token)
	ledger := &card.RedisLedger{Store: st, Sprint: id.Sprint, Label: id.Label, Token: token}

	cases := []struct {
		name  string
		l     *card.RedisLedger
		nonce string
		want  int
	}{
		{"claim", ledger, "nonce-a", 0},
		{"own retry", ledger, "nonce-a", 0},
		{"another wrapper", ledger, "nonce-b", 4},
		{"fenced", &card.RedisLedger{Store: st, Sprint: id.Sprint, Label: id.Label, Token: attemptToken(1, fmt.Sprintf("%032x", 9))}, "nonce-c", 3},
		{"no card", &card.RedisLedger{Store: st, Sprint: id.Sprint, Label: "card-none", Token: token}, "nonce-d", 5},
	}
	for _, tc := range cases {
		code, err := tc.l.Claim(ctx, tc.nonce)
		if err != nil || code != tc.want {
			t.Fatalf("%s: claim = %d, %v; want %d", tc.name, code, err, tc.want)
		}
	}
	hash := hashOf(t, ctx, admin, id.Sprint, id.Label)
	if hash["claim"] != card.TokenSHA(token)+":nonce-a" || hash["claim_at"] == "" || hash["state"] != "dealt" {
		t.Fatalf("card hash after claims %v; want claim %s:nonce-a with claim_at, still dealt", hash, card.TokenSHA(token))
	}

	// A card no longer dealt refuses a fresh claim with 2.
	other := card.Identity{Sprint: id.Sprint, Label: "card-queued", BaseSHA: id.BaseSHA, Bench: id.Bench, Attempt: 1}
	seedCard(t, ctx, admin, other, "queued", token)
	if code, err := (&card.RedisLedger{Store: st, Sprint: other.Sprint, Label: other.Label, Token: token}).Claim(ctx, "nonce-e"); err != nil || code != 2 {
		t.Fatalf("claim on a queued card = %d, %v; want 2", code, err)
	}
	if hits := scriptingCalls(t, ctx, admin); len(hits) != 0 {
		t.Fatalf("the claim reached for scripting or FUNCTION: %v", hits)
	}

	// The same fleet bench seat on a server whose owner never loaded the
	// library: the claim is ErrFunctionNotLoaded naming ns_card_claim, never a
	// claim, and the card path sends no FUNCTION call even though this seat's
	// ACL would let FUNCTION LOAD through. The library stays unloaded.
	bare, bareAdmin := fcallOnlySprint(t, false)
	seedCard(t, ctx, bareAdmin, id, "dealt", token)
	if _, err := (&card.RedisLedger{Store: bare, Sprint: id.Sprint, Label: id.Label, Token: token}).Claim(ctx, "nonce-f"); !errors.Is(err, card.ErrFunctionNotLoaded) ||
		!strings.Contains(err.Error(), "ns_card_claim") {
		t.Fatalf("claim on a server without the library = %v; want ErrFunctionNotLoaded naming ns_card_claim", err)
	}
	if hits := scriptingCalls(t, ctx, bareAdmin); len(hits) != 0 {
		t.Fatalf("the claim on a server without the library reached for scripting or FUNCTION: %v", hits)
	}
	libs, err = bareAdmin.FunctionList(ctx, redis.FunctionListQuery{LibraryNamePattern: fn.Library}).Result()
	if err != nil || len(libs) != 0 {
		t.Fatalf("library after a bench claim on a bare server = %v, %v; want none loaded", libs, err)
	}
	if hash := hashOf(t, ctx, bareAdmin, id.Sprint, id.Label); hash["claim"] != "" {
		t.Fatalf("bare server card hash %v; want no claim", hash)
	}
}

// TestCardPathMakesNoEvalCall is the second DONE-WHEN control of #3551: a
// card dealt to the fleet bench seat (FCALL, no EVAL/EVALSHA, and a FUNCTION
// LOAD it must never send) runs through the whole wrapper path (claim, launched, beat,
// end) to DONE on the owner's loaded library, and the server counts no EVAL,
// EVALSHA, SCRIPT or FUNCTION call from any client.
func TestCardPathMakesNoEvalCall(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	st, admin := fcallOnlySprint(t, true)
	id := card.Identity{Sprint: "control-noeval", Label: "card-probe", BaseSHA: "0123abcd", Bench: "wrap-bench", Attempt: 1}
	token := attemptToken(1, fmt.Sprintf("%032x", 3552))
	seedCard(t, ctx, admin, id, "dealt", token)
	gate := filepath.Join(t.TempDir(), "gate")
	if err := os.WriteFile(gate, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv(fakeHarnessEnv, "done")
	t.Setenv(fakeGateEnv, gate)

	h := newHarnessRun(t, id, self)
	got := make(chan card.WrapperReport, 1)
	go func() {
		got <- card.RunWrapper(ctx, h.cfg, &card.RedisLedger{Store: st, Sprint: id.Sprint, Label: id.Label, Token: token})
	}()
	rep := h.report(got)
	if rep.Code != card.WrapperExitEnded || rep.Outcome != "DONE" {
		t.Fatalf("report %s why=%q; want the card ended DONE", rep.Line(), rep.Why)
	}
	hash := hashOf(t, ctx, admin, id.Sprint, id.Label)
	if hash["state"] != "ended" || hash["outcome"] != "DONE" || !strings.HasPrefix(hash["claim"], card.TokenSHA(token)+":") {
		t.Fatalf("card hash %v; want ended DONE with this attempt's claim", hash)
	}
	if hits := scriptingCalls(t, ctx, admin); len(hits) != 0 {
		t.Fatalf("the card path sent scripting or FUNCTION commands: %v", hits)
	}
}
