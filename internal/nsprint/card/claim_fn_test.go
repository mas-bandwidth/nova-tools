package card_test

import (
	"context"
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

// benchUser is the bench's Redis permission shape for scripting (#3551):
// every command but the @scripting category, with FCALL and FCALL_RO added
// back. EVAL, EVALSHA, SCRIPT and every FUNCTION subcommand (LOAD, LIST,
// DELETE, ...) are NOPERM, so FCALL of a function the owner already loaded is
// the only way this user runs server-side code. A card path that reaches for
// EVALSHA, or loads the library from the bench, gets NOPERM.
const (
	benchUser     = "bench-fcall-only"
	benchPassword = "bench-fcall-only-pw"
)

// fcallOnlySprint starts a throwaway Redis, adds the FCALL-only bench user,
// and returns a store dialled as that user plus an admin client for seeding
// and inspection. With loaded, the admin (the library's owner) loads the
// nova_sprint library first, the way the coordinator converges the fleet's
// server; the bench never loads it. Command counters start from zero after
// the fixture's own probes.
func fcallOnlySprint(t *testing.T, loaded bool) (*store.Store, *redis.Client) {
	t.Helper()
	ctx := context.Background()
	addr := startRedis(t)
	admin := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = admin.Close() })
	if err := admin.Do(ctx, "ACL", "SETUSER", benchUser, "reset", "on", ">"+benchPassword, "~*", "&*",
		"+@all", "-@scripting", "+fcall", "+fcall_ro").Err(); err != nil {
		t.Fatal(err)
	}
	if loaded {
		if err := fn.Load(ctx, admin); err != nil {
			t.Fatal(err)
		}
	}
	bench := redis.NewClient(&redis.Options{Addr: addr, Username: benchUser, Password: benchPassword})
	t.Cleanup(func() { _ = bench.Close() })
	source, err := fn.Source()
	if err != nil {
		t.Fatal(err)
	}
	for _, probe := range [][]any{
		{"EVAL", "return 1", "0"},
		{"EVALSHA", "0000000000000000000000000000000000000000", "0"},
		{"SCRIPT", "LOAD", "return 1"},
		{"FUNCTION", "LOAD", "REPLACE", source},
		{"FUNCTION", "LIST"},
	} {
		if err := bench.Do(ctx, probe...).Err(); err == nil || !strings.Contains(err.Error(), "NOPERM") {
			t.Fatalf("bench user ran %v (err %v); the control needs FCALL only, no EVAL and no FUNCTION", probe[:2], err)
		}
	}
	// The refused probes above are counted; start the counters from zero.
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
// claims through it as a bench user that may FCALL but not EVAL/EVALSHA and
// not FUNCTION (LOAD included): the owner loaded the library, the bench only
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

	// The same bench seat on a server whose owner never loaded the library
	// cannot load it either: the claim is an error naming the function and
	// the refused load, never a claim. This is what makes the controls above
	// prove the bench used the owner's library without loading its own.
	bare, bareAdmin := fcallOnlySprint(t, false)
	seedCard(t, ctx, bareAdmin, id, "dealt", token)
	if _, err := (&card.RedisLedger{Store: bare, Sprint: id.Sprint, Label: id.Label, Token: token}).Claim(ctx, "nonce-f"); err == nil ||
		!strings.Contains(err.Error(), "ns_card_claim is not loaded") || !strings.Contains(err.Error(), "NOPERM") {
		t.Fatalf("claim on a server without the library = %v; want an error naming ns_card_claim and the refused load", err)
	}
}

// TestCardPathMakesNoEvalCall is the second DONE-WHEN control of #3551: a
// card dealt to a bench whose Redis user may FCALL but not EVAL/EVALSHA and
// not FUNCTION runs through the whole wrapper path (claim, launched, beat,
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
