package ci

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// THE CLASS RULE: PUSH, NOT POLL (Glenn, 2026-10-05: "Take a look across all
// communications tools between friends, and all tools for coordination, and make
// sure that they are all PUSH NOTIFICATION not polling!").
//
// Every loop in the tree that waits on a clock is named. A timer loop is a
// function, outside a _test.go and outside testdata, that holds a for loop and
// reaches a clock: time.Sleep, time.After, time.Tick, time.NewTicker or
// time.NewTimer, or a clock seam the tree injects in their place (a call to a
// func-valued Pause, pause, after, sleep or ticker given a duration, or a
// parameter that is a channel of time.Time: a ticker made by the caller). Each
// one is a line of testdata/push-loops.txt, `<file> <func> <row>`, and each row
// it names is a row of the table in docs/PUSH-NOT-POLL.md, section "Push, not
// poll". The table says each loop's direction, its mechanism (blocking read,
// delivery into a session, beat, timer poll, a wait that moves nothing between
// parties, or removed), its cadence, and,
// for a timer poll, the card that makes it push or the measured reason it stays.
// A new timer loop with no line fails; a line whose loop is gone fails; a line
// whose row is not in the table fails; a timer poll with neither a card nor a
// reason fails. The ledger is the inventory, not an allowlist: a loop is never
// excused by being in it, only named.

const (
	pushLoopsPath = "testdata/push-loops.txt"
	pushTableDoc  = "docs/PUSH-NOT-POLL.md"
	// pushTableHeading is the section the table lives under.
	pushTableHeading = "### Push, not poll"
)

// pushLoopDirs are the trees whose loops ship.
var pushLoopDirs = []string{"cmd", "internal"}

// pushClockTime are the clocks of package time a loop may wait on.
var pushClockTime = map[string]bool{"Sleep": true, "After": true, "Tick": true, "NewTicker": true, "NewTimer": true}

// pushClockSeams are the injected clocks the tree calls in their place: a
// Daemon's Pause, a member loop's after, a pusher's sleep, a stream's ticker.
// True is a seam whose name is a clock whatever it is given (sleep, ticker);
// false one that is a clock only given a duration (durationArg): a room's
// after(1), a store's after(ctx, step, res) and a lander's pause(ctx, s, repo,
// base) are no waits.
var pushClockSeams = map[string]bool{"Pause": false, "pause": false, "after": false, "sleep": true, "ticker": true}

// durationName is a name a duration goes by in the tree: BeatEvery, *every,
// WaitPongEvery, pushWaits[i], lr.every, TmuxPoll, w.pause, d.
var durationName = regexp.MustCompile(`(?i)(every|wait|delay|backoff|interval|period|poll|pause|^d$)`)

// durationArg is whether a call's argument is a duration: it reaches package
// time, or it names one (durationName).
func durationArg(args []ast.Expr, timeName string) bool {
	found := false
	for _, a := range args {
		ast.Inspect(a, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.SelectorExpr:
				if id, ok := x.X.(*ast.Ident); ok && timeName != "" && id.Name == timeName {
					found = true
				}
				found = found || durationName.MatchString(x.Sel.Name)
			case *ast.Ident:
				found = found || durationName.MatchString(x.Name)
			}
			return !found
		})
	}
	return found
}

// isTimeChan is whether t is a channel of time.Time (a ticker's C handed in).
func isTimeChan(t ast.Expr, timeName string) bool {
	c, ok := t.(*ast.ChanType)
	if !ok {
		return false
	}
	sel, ok := c.Value.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	id, ok := sel.X.(*ast.Ident)
	return ok && timeName != "" && id.Name == timeName && sel.Sel.Name == "Time"
}

// timerLoopsIn is every timer loop of one Go source, as `<func>` names: a
// function's own name, or `Recv.Name` for a method (the pointer dropped).
func timerLoopsIn(name string, src []byte) ([]string, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, name, src, parser.SkipObjectResolution)
	if err != nil {
		return nil, err
	}
	timeName := ""
	for _, imp := range f.Imports {
		if strings.Trim(imp.Path.Value, `"`) != "time" {
			continue
		}
		timeName = "time"
		if imp.Name != nil {
			timeName = imp.Name.Name
		}
	}
	var out []string
	for _, d := range f.Decls {
		fn, ok := d.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		var loops, clock bool
		for _, p := range fn.Type.Params.List {
			clock = clock || isTimeChan(p.Type, timeName)
		}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.ForStmt, *ast.RangeStmt:
				loops = true
			case *ast.SelectorExpr:
				if id, ok := x.X.(*ast.Ident); ok && timeName != "" && id.Name == timeName && pushClockTime[x.Sel.Name] {
					clock = true
				}
			case *ast.CallExpr:
				name := ""
				switch fun := x.Fun.(type) {
				case *ast.Ident:
					name = fun.Name
				case *ast.SelectorExpr:
					switch fun.X.(type) {
					case *ast.Ident, *ast.SelectorExpr: // d.Pause, s.a.sleep; never room[x].after
						name = fun.Sel.Name
					}
				}
				if always, ok := pushClockSeams[name]; ok {
					clock = clock || always || durationArg(x.Args, timeName)
				}
			}
			return true
		})
		if loops && clock {
			out = append(out, funcName(fn))
		}
	}
	return out, nil
}

// funcName is fn's name as a ledger line says it.
func funcName(fn *ast.FuncDecl) string {
	if fn.Recv == nil || len(fn.Recv.List) == 0 {
		return fn.Name.Name
	}
	t := fn.Recv.List[0].Type
	if s, ok := t.(*ast.StarExpr); ok {
		t = s.X
	}
	switch x := t.(type) {
	case *ast.IndexExpr:
		t = x.X
	case *ast.IndexListExpr:
		t = x.X
	}
	if id, ok := t.(*ast.Ident); ok {
		return id.Name + "." + fn.Name.Name
	}
	return fn.Name.Name
}

// timerLoopsOfTree is every timer loop under root's pushLoopDirs, as
// `<file> <func>` keys, the file repo-relative with slashes.
func timerLoopsOfTree(root string) ([]string, error) {
	var out []string
	for _, dir := range pushLoopDirs {
		err := filepath.WalkDir(filepath.Join(root, dir), func(p string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				switch d.Name() {
				case "testdata", ".git", "vendor":
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
				return nil
			}
			src, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			rel, err := filepath.Rel(root, p)
			if err != nil {
				return err
			}
			rel = filepath.ToSlash(rel)
			names, err := timerLoopsIn(rel, src)
			if err != nil {
				return err
			}
			for _, n := range names {
				out = append(out, rel+" "+n)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	sort.Strings(out)
	return out, nil
}

// pushLoopLine is one line of the ledger: a loop and the table row naming it.
type pushLoopLine struct {
	key, row string
}

// parsePushLoops reads the ledger: `<file> <func> <row>`, the row being the rest
// of the line; blank lines and # lines skipped.
func parsePushLoops(text string) ([]pushLoopLine, []string) {
	var out []pushLoopLine
	var bad []string
	for i, l := range strings.Split(text, "\n") {
		l = strings.TrimSpace(l)
		if l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		f := strings.Fields(l)
		if len(f) < 3 {
			bad = append(bad, fmt.Sprintf("%s:%d: %q is not `<file> <func> <row>`", pushLoopsPath, i+1, l))
			continue
		}
		out = append(out, pushLoopLine{key: f[0] + " " + f[1], row: strings.Join(f[2:], " ")})
	}
	return out, bad
}

// pushRow is one row of the table: its name (the first cell, code spans
// unwrapped), its mechanism and its card-or-reason cell.
type pushRow struct {
	name, direction, mechanism, cadence, card string
}

// The mechanisms a row may name.
const (
	mechBlocking = "blocking read"
	mechDelivery = "delivery into a session"
	mechBeat     = "beat"
	mechPoll     = "timer poll"
	mechWait     = "not a channel"
	mechRemoved  = "removed"
)

var pushMechanisms = map[string]bool{mechBlocking: true, mechDelivery: true, mechBeat: true, mechPoll: true, mechWait: true, mechRemoved: true}

// parsePushTable reads the table under pushTableHeading in doc: a Markdown table
// whose columns are loop, direction, mechanism, cadence, card or reason. It
// returns the rows and the findings of a row that does not read.
func parsePushTable(doc string) ([]pushRow, []string) {
	_, after, ok := strings.Cut(doc, "\n"+pushTableHeading+"\n")
	if !ok {
		return nil, []string{fmt.Sprintf("%s has no section %q", pushTableDoc, pushTableHeading)}
	}
	if i := strings.Index(after, "\n#"); i >= 0 {
		after = after[:i]
	}
	var rows []pushRow
	var bad []string
	header := true
	for _, l := range strings.Split(after, "\n") {
		l = strings.TrimSpace(l)
		if !strings.HasPrefix(l, "|") {
			continue
		}
		cells := strings.Split(strings.Trim(l, "|"), "|")
		for i := range cells {
			cells[i] = strings.TrimSpace(cells[i])
		}
		if header {
			header = false
			continue
		}
		if strings.Trim(strings.Join(cells, ""), "-: ") == "" {
			continue
		}
		if len(cells) != 5 {
			bad = append(bad, fmt.Sprintf("%s %s: row %q has %d cells, want 5 (loop, direction, mechanism, cadence, card or reason)", pushTableDoc, pushTableHeading, l, len(cells)))
			continue
		}
		r := pushRow{name: strings.ReplaceAll(cells[0], "`", ""), direction: cells[1], mechanism: cells[2], cadence: cells[3], card: cells[4]}
		if !pushMechanisms[r.mechanism] {
			bad = append(bad, fmt.Sprintf("%s: row %q names mechanism %q; want one of %q, %q, %q, %q, %q, %q", pushTableDoc, r.name, r.mechanism, mechBlocking, mechDelivery, mechBeat, mechPoll, mechWait, mechRemoved))
		}
		if r.mechanism == mechPoll && (r.card == "" || r.card == "-") {
			bad = append(bad, fmt.Sprintf("%s: row %q is a timer poll with no card that makes it push and no measured reason it stays", pushTableDoc, r.name))
		}
		rows = append(rows, r)
	}
	if len(rows) == 0 {
		bad = append(bad, fmt.Sprintf("%s %s holds no table", pushTableDoc, pushTableHeading))
	}
	return rows, bad
}

// pushFindings is every disagreement between the loops of the tree, the ledger
// and the table, sorted.
func pushFindings(loops []string, ledger []pushLoopLine, rows []pushRow) []string {
	var out []string
	inTree := map[string]bool{}
	for _, k := range loops {
		inTree[k] = true
	}
	byRow := map[string]bool{}
	for _, r := range rows {
		byRow[r.name] = true
	}
	inLedger := map[string]bool{}
	for _, l := range ledger {
		if inLedger[l.key] {
			out = append(out, fmt.Sprintf("%s: %s is named twice", pushLoopsPath, l.key))
		}
		inLedger[l.key] = true
		if !inTree[l.key] {
			out = append(out, fmt.Sprintf("%s: %s is no timer loop of the tree (gone, renamed, or now pushed); delete or fix its line", pushLoopsPath, l.key))
		}
		if !byRow[l.row] {
			out = append(out, fmt.Sprintf("%s: %s names row %q, which the table in %s (%s) does not hold", pushLoopsPath, l.key, l.row, pushTableDoc, pushTableHeading))
		}
	}
	for _, k := range loops {
		if !inLedger[k] {
			out = append(out, fmt.Sprintf("%s is a timer loop with no line in %s: name it as `%s <row>` and give the row its direction, mechanism, cadence and the card that makes it push (or the measured reason it stays) in %s, section %q", k, pushLoopsPath, k, pushTableDoc, pushTableHeading))
		}
	}
	sort.Strings(out)
	return out
}

// TestEveryTimerLoopIsNamedInThePushTable walks the tree's timer loops and
// holds them against the ledger and the table.
func TestEveryTimerLoopIsNamedInThePushTable(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	loops, err := timerLoopsOfTree(root)
	require.NoError(t, err)
	require.NotEmpty(t, loops, "a tree with no timer loop is not a pass: the walk read nothing")

	ledger, bad := parsePushLoops(readFile(t, pushLoopsPath))
	rows, badRows := parsePushTable(readFile(t, filepath.Join(root, pushTableDoc)))
	bad = append(bad, badRows...)
	bad = append(bad, pushFindings(loops, ledger, rows)...)
	assert.Empty(t, bad, "push, not poll (%s, %s):\n%s", pushTableDoc, pushTableHeading, strings.Join(bad, "\n"))
}

// TestTimerLoopsInFindsEveryClock is the scanner's contract: a loop over each
// clock of package time and each clock seam is found, a method by Recv.Name,
// and a loop with no clock, a clock with no loop and a clock of another
// package named time-alike are not.
func TestTimerLoopsInFindsEveryClock(t *testing.T) {
	t.Parallel()

	src := `package p

import (
	"context"
	clock "time"
)

type d struct{ Pause func(context.Context, clock.Duration) }

func sleeps() { for { clock.Sleep(clock.Second) } }
func ticks() { t := clock.NewTicker(clock.Second); for range t.C {} }
func timer() { for { <-clock.After(clock.Second) } }
func (x *d) pauses(ctx context.Context) { for ctx.Err() == nil { x.Pause(ctx, clock.Second) } }
func seam(after func(clock.Duration) <-chan clock.Time) { for { <-after(clock.Second) } }
func plain(xs []int) int { n := 0; for _, x := range xs { n += x }; return n }
func once() { <-clock.After(clock.Second) }
func now() { for { _ = clock.Now() } }
func beats(every <-chan clock.Time) { for range every {} }
type room struct{}
func (room) after(n int) room { return room{} }
func rooms(r []room) { for i := range r { r[i] = r[i].after(1) } }
func (x *d) steps(st *d, ctx context.Context) { for { st.after(ctx, 1) } }
func (x *d) after(ctx context.Context, n int) {}
func (x *d) sleep(n clock.Duration) {}
func (x *d) steps2(n clock.Duration) { for n > 0 { x.sleep(n) ; n-- } }
`
	got, err := timerLoopsIn("p.go", []byte(src))
	require.NoError(t, err)
	assert.Equal(t, []string{"sleeps", "ticks", "timer", "d.pauses", "seam", "beats", "d.steps2"}, got)

	other := "package p\n\nimport time \"example.com/fake\"\n\nfunc f() { for { time.Sleep(1) } }\n"
	got, err = timerLoopsIn("q.go", []byte(other))
	require.NoError(t, err)
	assert.Empty(t, got, "a package named time that is not package time is no clock")
}

// TestPushFindingsRefuseEachDisagreement holds the rule's four refusals: a loop
// with no line, a line with no loop, a line with no row, and a timer poll with
// neither a card nor a reason.
func TestPushFindingsRefuseEachDisagreement(t *testing.T) {
	t.Parallel()

	doc := "# x\n\n" + pushTableHeading + "\n\n| loop | direction | mechanism | cadence | card or reason |\n|---|---|---|---|---|\n" +
		"| `a` | bus to friend | blocking read | 1 s | - |\n| `b` | server to member | timer poll | 3 s | - |\n| `c` | sprint to TV | timer poll | 1 s | a display pull |\n\n## next\n"
	rows, bad := parsePushTable(doc)
	require.Len(t, rows, 3)
	require.Len(t, bad, 1)
	assert.Contains(t, bad[0], `row "b" is a timer poll with no card`)

	ledger, badLines := parsePushLoops("# c\nx.go f a\ny.go g zz\nshort\n")
	require.Len(t, badLines, 1)
	got := pushFindings([]string{"x.go f", "w.go new"}, ledger, rows)
	require.Len(t, got, 3, "%s", strings.Join(got, "\n"))
	all := strings.Join(got, "\n")
	assert.Contains(t, all, "w.go new is a timer loop with no line")
	assert.Contains(t, all, "y.go g is no timer loop of the tree")
	assert.Contains(t, all, `names row "zz"`)
}
