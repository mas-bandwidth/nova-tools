package ci

// The machine of the `dutymoves` class test (nova-tools #4059): no two
// reconciler duties undo each other. The table it reads is
// internal/nsprint/reconcile's DutyMoves (moves.go); the class test
// (dutymoves_class_test.go) hands it the table, the reconcile package's Go
// files and the Lua library's sources.

import (
	"fmt"
	"go/ast"
	"go/token"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// DutyMove is one row of the reconciler's move graph: a duty, the file that
// calls the Lua function (Via) that writes the move, the places (From may be
// a|b), the atoms that hold when it fires (Guard; "!x" is not-x) and the
// atoms it makes true (Sets).
type DutyMove struct {
	Duty, File, Via, From, To string
	Guard, Sets               []string
}

func (m DutyMove) String() string {
	return fmt.Sprintf("%s %s %s->%s", m.Duty, m.Via, m.From, m.To)
}

func dmFrom(m DutyMove) []string { return strings.Split(m.From, "|") }

func dmHas(xs []string, x string) bool {
	for _, y := range xs {
		if y == x {
			return true
		}
	}
	return false
}

func dmNeg(a string) string {
	if n, ok := strings.CutPrefix(a, "!"); ok {
		return n
	}
	return "!" + a
}

// dmBlocked is whether b cannot fire right after a: b's guard names the
// negation of an atom a's guard requires or a's move sets.
func dmBlocked(a, b DutyMove) bool {
	for _, x := range append(append([]string{}, a.Guard...), a.Sets...) {
		if dmHas(b.Guard, dmNeg(x)) {
			return true
		}
	}
	return false
}

// dmInverse is whether b undoes a: a takes a card X -> Y and b takes it Y -> X.
func dmInverse(a, b DutyMove) bool {
	if a.To == "" || b.To == "" || dmHas(dmFrom(a), a.To) || dmHas(dmFrom(b), b.To) {
		return false
	}
	return dmHas(dmFrom(a), b.To) && dmHas(dmFrom(b), a.To)
}

// InverseDutyMoves is one finding per pair of rows that are each other's
// inverse and can each fire right after the other: the shape that moved one
// card waiting -> ready -> waiting every tick for an hour.
func InverseDutyMoves(rows []DutyMove) []string {
	var out []string
	for i := range rows {
		for j := i + 1; j < len(rows); j++ {
			a, b := rows[i], rows[j]
			if !dmInverse(a, b) || dmBlocked(a, b) || dmBlocked(b, a) {
				continue
			}
			out = append(out, fmt.Sprintf("%s and %s are each other's inverse and neither guard stops the other (%s after %v, %s after %v): a card can move back and forth every tick",
				a, b, b.Via, a.Guard, a.Via, b.Guard))
		}
	}
	return out
}

// ReadyToWaitingMoves is the key "<duty> <via> ready->waiting" of every row
// that moves a card back from ready to waiting (waiting -> ready is one way).
func ReadyToWaitingMoves(rows []DutyMove) []string {
	var out []string
	for _, m := range rows {
		if m.To == "waiting" && dmHas(dmFrom(m), "ready") {
			out = append(out, m.Duty+" "+m.Via+" ready->waiting")
		}
	}
	return out
}

var (
	dmLuaComment = regexp.MustCompile(`--[^\n]*`)
	dmLuaReg     = regexp.MustCompile(`register_function\s*\(\s*'(ns_[a-z0-9_]+)'|function_name\s*=\s*'(ns_[a-z0-9_]+)'`)
	// A move names its destination: the card model's one move, the task
	// card's (NS.task.move, and NS.task.create's where, #3778), the ws
	// index's, the task events', or a ZADD straight into a ws set.
	dmLuaDest = []*regexp.Regexp{
		regexp.MustCompile(`\b(?:CARD|NS\.card|card|NS\.task)\.move\(\s*[^,()]+,\s*'([a-z]+)'`),
		regexp.MustCompile(`\bNS\.task\.create\([^()]*?\bwhere\s*=\s*'([a-z]+)'`),
		regexp.MustCompile(`\bcard_move\(\s*[^,()]+,\s*'([a-z]+)'`),
		regexp.MustCompile(`\bmove_one\(\s*[^,()]+,\s*'([a-z]+)'`),
		regexp.MustCompile(`\bTE\.(?:move|apply)\(\s*[^,()]+,\s*'([a-z]+)'`),
		regexp.MustCompile(`'ZADD',\s*'ws:'\s*\.\.\s*[A-Za-z_]+\s*\.\.\s*':([a-z]+)'`),
		regexp.MustCompile(`'ZADD',\s*[A-Za-z_]+\.key\(\s*[^,()]+,\s*'([a-z]+)'\s*\)`),
	}
	dmLuaNamed = map[*regexp.Regexp]string{
		regexp.MustCompile(`\bTE\.landed\(`): "landed",
		regexp.MustCompile(`\bTE\.opened\(`): "merging",
	}
	dmFnName = regexp.MustCompile(`^ns_[a-z0-9_]+$`)
)

// LuaRegistered is the functions a Lua source registers.
func LuaRegistered(src string) []string {
	var out []string
	for _, m := range dmLuaReg.FindAllStringSubmatch(dmLuaComment.ReplaceAllString(src, ""), -1) {
		out = append(out, m[1]+m[2])
	}
	return out
}

// LuaMoveDests is every place a Lua source moves a card to, sorted.
func LuaMoveDests(src string) []string {
	code := dmLuaComment.ReplaceAllString(src, "")
	seen := map[string]bool{}
	for _, re := range dmLuaDest {
		for _, m := range re.FindAllStringSubmatch(code, -1) {
			seen[m[1]] = true
		}
	}
	for re, d := range dmLuaNamed {
		if re.MatchString(code) {
			seen[d] = true
		}
	}
	out := make([]string, 0, len(seen))
	for d := range seen {
		out = append(out, d)
	}
	sort.Strings(out)
	return out
}

// GoFnCalls is every Lua function name (an "ns_..." string literal) a Go file
// names.
func GoFnCalls(f *ast.File) []string {
	var out []string
	ast.Inspect(f, func(n ast.Node) bool {
		if lit, ok := n.(*ast.BasicLit); ok && lit.Kind == token.STRING {
			if s, err := strconv.Unquote(lit.Value); err == nil && dmFnName.MatchString(s) {
				out = append(out, s)
			}
		}
		return true
	})
	return out
}

// GoWSMoves is the destination of every ws.Move(ctx, c, id, "<to>", ...) and
// ws.MoveMany(ctx, c, "<to>", ...) call in a Go file, as "ns_ws_move <to>" or
// "ns_ws_move_many <to>"; a destination that is not a literal is "?".
func GoWSMoves(f *ast.File) []string {
	var out []string
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if x, ok := sel.X.(*ast.Ident); !ok || x.Name != "ws" {
			return true
		}
		at, fn := -1, ""
		switch sel.Sel.Name {
		case "Move":
			at, fn = 3, "ns_ws_move"
		case "MoveMany":
			at, fn = 2, "ns_ws_move_many"
		default:
			return true
		}
		to := "?"
		if at < len(call.Args) {
			if lit, ok := call.Args[at].(*ast.BasicLit); ok && lit.Kind == token.STRING {
				to, _ = strconv.Unquote(lit.Value)
			}
		}
		out = append(out, fn+" "+to)
		return true
	})
	return out
}

// CheckDutyMoves holds the table to the code. goFiles are the reconcile
// package's non-test files by base name; luaFiles the library's sources by
// base name. Every place a Lua function the package calls moves a card to
// needs a row whose Via that file registers; every ws.Move/MoveMany call
// needs a row; and every row's Via must be called from its File and move to
// its To.
func CheckDutyMoves(rows []DutyMove, goFiles map[string]*ast.File, luaFiles map[string]string) []string {
	var out []string
	calls := map[string]map[string]bool{} // fn -> files
	wsDest := map[string]bool{}           // file + " " + fn + " " + to
	for name, f := range goFiles {
		for _, fn := range GoFnCalls(f) {
			if calls[fn] == nil {
				calls[fn] = map[string]bool{}
			}
			calls[fn][name] = true
		}
		for _, mv := range GoWSMoves(f) {
			wsDest[name+" "+mv] = true
		}
	}
	luaOf := map[string]string{}
	dests := map[string][]string{}
	for name, src := range luaFiles {
		for _, fn := range LuaRegistered(src) {
			luaOf[fn] = name
		}
		dests[name] = LuaMoveDests(src)
	}

	declared := map[string]bool{} // lua file + " " + to, and go file + " " + fn + " " + to
	for _, m := range rows {
		if m.Via == "ns_ws_move" || m.Via == "ns_ws_move_many" {
			k := m.File + " " + m.Via + " " + m.To
			declared[k] = true
			if !wsDest[k] {
				out = append(out, fmt.Sprintf("row %s: %s makes no %s call to %s; delete the row or name the file that does", m, m.File, m.Via, m.To))
			}
			continue
		}
		if !calls[m.Via][m.File] {
			out = append(out, fmt.Sprintf("row %s: %s never names %s; a row describes a call the duty makes", m, m.File, m.Via))
		}
		lf := luaOf[m.Via]
		if lf == "" {
			out = append(out, fmt.Sprintf("row %s: no Lua file registers %s", m, m.Via))
			continue
		}
		declared[lf+" "+m.To] = true
		if !dmHas(dests[lf], m.To) {
			out = append(out, fmt.Sprintf("row %s: %s moves no card to %s (it moves to %v); delete the row", m, lf, m.To, dests[lf]))
		}
	}
	var lfs []string
	for fn := range calls {
		if fn == "ns_ws_move" || fn == "ns_ws_move_many" {
			// Their destinations are the Go call's literal (wsDest below),
			// not the ws index file's other functions' (park, rename).
			continue
		}
		if lf := luaOf[fn]; lf != "" && !dmHas(lfs, lf) {
			lfs = append(lfs, lf)
		}
	}
	sort.Strings(lfs)
	for _, lf := range lfs {
		for _, d := range dests[lf] {
			if !declared[lf+" "+d] {
				out = append(out, fmt.Sprintf("%s moves a card to %s and reconcile calls it, but no DutyMoves row declares a move to %s through a function it registers; add the row with its guard", lf, d, d))
			}
		}
	}
	var ks []string
	for k := range wsDest {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	for _, k := range ks {
		if !declared[k] {
			out = append(out, fmt.Sprintf("%s: no DutyMoves row declares this ws move; add the row with its guard", k))
		}
	}
	return out
}
