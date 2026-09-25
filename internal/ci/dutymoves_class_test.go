package ci

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/reconcile"
)

// THE CLASS RULE: NO TWO RECONCILER DUTIES UNDO EACH OTHER (nova-tools #4059).
//
// Glenn, 2026-09-25 1:35 PM ET: "one card is oscillating between being in
// waiting, to ready, then back to waiting over and over." The waiting-resolve
// duty moved build-3041 waiting -> ready ("depends-on met") and the deal duty
// moved it ready -> waiting ("no-consumer"), every tick for an hour. Each rule
// was right alone; the pair was a loop, because neither guard said anything
// of the other's. His rulings: waiting -> ready is ONE WAY, and a ready card
// is dealt the same tick.
//
// The fix for the class: every move a duty in internal/nsprint/reconcile can
// make is a row of reconcile.DutyMoves with its guard, and this test fails
//
//   - any two rows that are each other's inverse (X -> Y and Y -> X) when
//     neither guard stops the other firing right after it;
//   - any ready -> waiting row not in the shrink-only allowlist;
//   - any move the code makes that no row declares, and any row the code
//     does not make (so the table cannot drift from the duties).

const dutyMovesAllowlistPath = "testdata/dutymoves-oneway-allowlist.txt"

func dutyMovesRows() []DutyMove {
	rows := make([]DutyMove, 0, len(reconcile.DutyMoves))
	for _, m := range reconcile.DutyMoves {
		rows = append(rows, DutyMove{Duty: m.Duty, File: m.File, Via: m.Via, From: m.From, To: m.To, Guard: m.Guard, Sets: m.Sets})
	}
	return rows
}

// TestNoInverseDutyMovesInTheReconciler is the class test over the reconciler.
func TestNoInverseDutyMovesInTheReconciler(t *testing.T) {
	t.Parallel()
	tree := repoTree(t)
	rows := dutyMovesRows()

	for _, f := range InverseDutyMoves(rows) {
		t.Errorf("internal/nsprint/reconcile/moves.go: %s; remedy=\"guard one move on the negation of the other's (the resolve moves to ready only with a consumer), or delete the reverse move\"", f)
	}

	allow := readAllowlist(t, dutyMovesAllowlistPath)
	seen := map[string]bool{}
	for _, k := range ReadyToWaitingMoves(rows) {
		seen[k] = true
		if !allow[k] {
			t.Errorf("internal/nsprint/reconcile/moves.go: %s: waiting -> ready is one way (Glenn 2026-09-25); remedy=\"leave the card in waiting with its why, never move it back\"", k)
		}
	}
	for k := range allow {
		if !seen[k] {
			t.Errorf("%s: %q names no ready -> waiting move any more; delete the row (the allowlist only shrinks)", dutyMovesAllowlistPath, k)
		}
	}

	goFiles := map[string]*ast.File{}
	for _, f := range tree.GoFilesUnder(false, "internal/nsprint/reconcile") {
		if f.ParseErr != nil {
			t.Fatal(f.ParseErr)
		}
		if filepath.Dir(f.Rel) != "internal/nsprint/reconcile" {
			continue
		}
		goFiles[filepath.Base(f.Rel)] = f.AST
	}
	luaDir := filepath.Join(tree.Root, "internal", "nsprint", "fn", "lua")
	ents, err := os.ReadDir(luaDir)
	if err != nil {
		t.Fatal(err)
	}
	luaFiles := map[string]string{}
	for _, e := range ents {
		if !strings.HasSuffix(e.Name(), ".lua") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(luaDir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		luaFiles[e.Name()] = string(b)
	}
	if len(goFiles) == 0 || len(luaFiles) == 0 {
		t.Fatalf("read %d reconcile files and %d Lua files; the walk found nothing to hold the table to", len(goFiles), len(luaFiles))
	}
	findings := CheckDutyMoves(rows, goFiles, luaFiles)
	sort.Strings(findings)
	for _, f := range findings {
		t.Errorf("internal/nsprint/reconcile/moves.go: %s", f)
	}
}

// TestDutyMovesSeesTheOscillation is the rule's own control: the two rows
// the reconciler had before #4059 (resolve on deps-met, return on
// no-consumer) are an inverse pair neither guard stops, and the return is a
// ready -> waiting move; the fixed resolve (deps-met AND consumer) against
// the same return is stopped; the swarm gate's pair (the same predicate's two
// sides) is stopped.
func TestDutyMovesSeesTheOscillation(t *testing.T) {
	resolve := DutyMove{Duty: "waiting-resolve", Via: "ns_ws_move_many", From: "waiting", To: "ready", Guard: []string{"deps-met"}}
	back := DutyMove{Duty: "deal", Via: "ns_deal_return", From: "ready", To: "waiting", Guard: []string{"!consumer"}}
	if got := InverseDutyMoves([]DutyMove{resolve, back}); len(got) != 1 {
		t.Fatalf("the #4059 pair: %d findings %v, want 1", len(got), got)
	}
	if got := ReadyToWaitingMoves([]DutyMove{resolve, back}); strings.Join(got, ",") != "deal ns_deal_return ready->waiting" {
		t.Fatalf("one-way findings %v, want the return", got)
	}
	resolve.Guard = []string{"deps-met", "consumer"}
	if got := InverseDutyMoves([]DutyMove{resolve, back}); len(got) != 0 {
		t.Fatalf("the guarded pair: findings %v, want none", got)
	}
	wait := DutyMove{Duty: "refill", Via: "ns_card_gate", From: "ready", To: "waiting", Guard: []string{"!deps-met"}}
	release := DutyMove{Duty: "refill", Via: "ns_card_gate", From: "waiting", To: "ready", Guard: []string{"deps-met"}}
	deal := DutyMove{Duty: "deal", Via: "ns_deal_friend", From: "ready", To: "working", Sets: []string{"attempt-live"}}
	reclaim := DutyMove{Duty: "expire", Via: "ns_card_reclaim", From: "working", To: "ready", Guard: []string{"!attempt-live"}}
	if got := InverseDutyMoves([]DutyMove{wait, release, deal, reclaim}); len(got) != 0 {
		t.Fatalf("exclusive pairs: findings %v, want none", got)
	}
	reclaim.Guard = nil
	if got := InverseDutyMoves([]DutyMove{deal, reclaim}); len(got) != 1 {
		t.Fatalf("an unguarded reclaim: findings %v, want the deal/reclaim pair", got)
	}
}

// TestDutyMovesHoldsTheTableToTheCode is the honesty half's control: a Lua
// move no row declares, a row whose file never calls its function, and a row
// to a place the function never moves to are each red.
func TestDutyMovesHoldsTheTableToTheCode(t *testing.T) {
	lua := map[string]string{"x.lua": "local function f(k, a)\n  CARD.move(a[1], 'ready', {})\n  CARD.move(a[1], 'waiting', {}) -- a comment 'done'\nend\nredis.register_function('ns_x', f)\n"}
	goFiles := map[string]*ast.File{"x.go": dutyMovesParse(t, "package p\nfunc g() { _ = \"ns_x\"; ws.MoveMany(ctx, c, \"ready\", \"a\", \"b\", ids) }\n")}
	rows := []DutyMove{
		{Duty: "d", File: "x.go", Via: "ns_x", From: "waiting", To: "ready"},
		{Duty: "d", File: "x.go", Via: "ns_ws_move_many", From: "waiting", To: "ready"},
	}
	got := strings.Join(CheckDutyMoves(rows, goFiles, lua), "\n")
	if !strings.Contains(got, "x.lua moves a card to waiting") || strings.Contains(got, "to done") {
		t.Fatalf("an undeclared move: findings\n%s\nwant the waiting move named, the comment ignored", got)
	}
	rows = append(rows, DutyMove{Duty: "d", File: "y.go", Via: "ns_x", From: "ready", To: "waiting"},
		DutyMove{Duty: "d", File: "x.go", Via: "ns_x", From: "ready", To: "working"})
	got = strings.Join(CheckDutyMoves(rows, goFiles, lua), "\n")
	for _, want := range []string{"y.go never names ns_x", "moves no card to working"} {
		if !strings.Contains(got, want) {
			t.Fatalf("findings\n%s\nwant %q", got, want)
		}
	}
}

func dutyMovesParse(t *testing.T, src string) *ast.File {
	t.Helper()
	f, err := parser.ParseFile(token.NewFileSet(), "x.go", src, 0)
	if err != nil {
		t.Fatal(err)
	}
	return f
}
