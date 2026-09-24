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
// every command but EVAL, EVALSHA and SCRIPT, so FCALL is the only way to
// run server-side code. A card path that reaches for EVALSHA gets NOPERM.
const (
	benchUser     = "bench-fcall-only"
	benchPassword = "bench-fcall-only-pw"
)

// fcallOnlySprint starts a throwaway Redis, adds the FCALL-only bench user,
// and returns a store dialled as that user plus an admin client for seeding
// and inspection.
func fcallOnlySprint(t *testing.T) (*store.Store, *redis.Client) {
	t.Helper()
	ctx := context.Background()
	addr := startRedis(t)
	admin := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = admin.Close() })
	if err := admin.Do(ctx, "ACL", "SETUSER", benchUser, "reset", "on", ">"+benchPassword, "~*", "&*",
		"+@all", "-eval", "-evalsha", "-eval_ro", "-evalsha_ro", "-script").Err(); err != nil {
		t.Fatal(err)
	}
	bench := redis.NewClient(&redis.Options{Addr: addr, Username: benchUser, Password: benchPassword})
	t.Cleanup(func() { _ = bench.Close() })
	if err := bench.Do(ctx, "EVAL", "return 1", "0").Err(); err == nil || !strings.Contains(err.Error(), "NOPERM") {
		t.Fatalf("bench user ran EVAL (err %v); the control needs FCALL only", err)
	}
	// The refused probe above is counted; start the counters from zero.
	if err := admin.ConfigResetStat(ctx).Err(); err != nil {
		t.Fatal(err)
	}
	return store.New(bench), admin
}

// evalCalls reads the server's own command counters, so any client on any
// path that sent EVAL, EVALSHA or SCRIPT is counted, refused or not.
func evalCalls(t *testing.T, ctx context.Context, admin *redis.Client) []string {
	t.Helper()
	info, err := admin.Info(ctx, "commandstats").Result()
	if err != nil {
		t.Fatal(err)
	}
	var hits []string
	for _, line := range strings.Split(info, "\n") {
		name, _, _ := strings.Cut(strings.TrimSpace(line), ":")
		name = strings.TrimPrefix(name, "cmdstat_")
		if strings.HasPrefix(name, "eval") || strings.HasPrefix(name, "script") {
			hits = append(hits, strings.TrimSpace(line))
		}
	}
	return hits
}

// TestCardClaimIsAFunction is the first DONE-WHEN control of #3551: the
// attempt claim is ns_card_claim in the nova_sprint library, and RedisLedger
// claims through it as a bench user that may FCALL but not EVAL/EVALSHA.
func TestCardClaimIsAFunction(t *testing.T) {
	ctx := context.Background()
	st, admin := fcallOnlySprint(t)
	if err := fn.Load(ctx, admin); err != nil {
		t.Fatal(err)
	}
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
	if hits := evalCalls(t, ctx, admin); len(hits) != 0 {
		t.Fatalf("the claim reached for scripting: %v", hits)
	}
}

// TestCardPathMakesNoEvalCall is the second DONE-WHEN control of #3551: a
// card dealt to a bench whose Redis user may FCALL but not EVAL/EVALSHA runs
// through the whole wrapper path (claim, launched, beat, end) to DONE, and the
// server counts no EVAL, EVALSHA or SCRIPT call from any client.
func TestCardPathMakesNoEvalCall(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	st, admin := fcallOnlySprint(t)
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
	if hits := evalCalls(t, ctx, admin); len(hits) != 0 {
		t.Fatalf("the card path sent scripting commands: %v", hits)
	}
}
