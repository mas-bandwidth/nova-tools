package sprint

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/cardhdr"
)

// A tier is served by an enabled fleet route or by a friend up whose row lists it (the
// owner, 2026-10-04: "Fleet flash only; pro to friends"). On 2026-10-06 the fleet's pro
// routes were disabled on purpose and stella was up with pro on her row, and
// `rework --tier pro` refused: no enabled route serves tier pro.

// proOffWorld is s1-1 in review after a failed attempt, two machines up, a flash route
// enabled and the pro route disabled.
func proOffWorld(t *testing.T) *world {
	t.Helper()
	w := setup(t, 1)
	finished(w, "s1-1", true)
	require.Equal(t, Review, w.state("s1-1"))
	w.s.Routes = []Route{
		{Name: "flash-a", Tier: cardhdr.RouteFlash, Provider: "p", Model: "f", Enabled: true},
		{Name: "pro-off", Tier: cardhdr.RoutePro, Provider: "p", Model: "pro", Enabled: false},
	}
	return w
}

func TestReworkAcceptsATierAFriendServes(t *testing.T) {
	t.Parallel()
	stella := func(status string) FriendSeat {
		return FriendSeat{Name: "stella", Width: 2, Status: status, Class: "heavy,pro"}
	}
	johnny := FriendSeat{Name: "johnny", Width: 2, Status: Down, Class: "pro"}
	rework := func(w *world) Plan {
		return Rework(w.s, ReworkReq{Sel: Sel{IDs: []string{"s1-1"}}, Fix: "make it green", Tier: cardhdr.RoutePro})
	}

	t.Run("stella up serves pro: the rework moves, and the friends' deal deals it to her", func(t *testing.T) {
		t.Parallel()
		w := proOffWorld(t)
		w.s.Friends = []FriendSeat{stella(Up)}
		p := w.must(rework(w))
		require.Len(t, p.Units, 1)
		assert.Contains(t, p.Units[0].Moved, "review -> ready")
		assert.Contains(t, p.Units[0].Moved, "stella")
		assert.Equal(t, Ready, w.state("s1-1"))
		for _, m := range w.s.Members() {
			for _, c := range w.s.Fleet.Cell(m, Ready) {
				assert.NotEqual(t, "s1-1", c.F("primary"), "no machine is dealt a tier no route serves")
			}
		}
		// the tick: no judgment of the tier, and stella is dealt it
		dealWith(w, stella(Up))
		wc := w.s.Fleet.Card("s1-1.w2")
		require.NotNil(t, wc, "its next attempt is dealt")
		assert.Equal(t, FriendRow("stella"), wc.Row)
		for _, o := range w.s.Open {
			assert.NotEqual(t, NNoRoute, o.Note.Type, "a tier a friend up serves is no judgment: %s", o.Note.What)
		}
	})
	t.Run("stella held: refused naming her", func(t *testing.T) {
		t.Parallel()
		w := proOffWorld(t)
		w.s.Friends = []FriendSeat{stella(Held)}
		p := rework(w)
		require.Len(t, p.Refused, 1)
		assert.Contains(t, p.Refused[0].Why, "no enabled route and no up friend serves tier pro")
		assert.Contains(t, p.Refused[0].Why, "bring up a friend whose row lists pro")
		assert.Contains(t, p.Refused[0].Why, "stella serves pro but is held/down")
		assert.Equal(t, Review, w.state("s1-1"))
	})
	t.Run("stella held and johnny down: refused naming both", func(t *testing.T) {
		t.Parallel()
		w := proOffWorld(t)
		w.s.Friends = []FriendSeat{stella(Held), johnny}
		p := rework(w)
		require.Len(t, p.Refused, 1)
		assert.Contains(t, p.Refused[0].Why, "stella and johnny serve pro but are held/down")
	})
	t.Run("no friend: refused naming both ways", func(t *testing.T) {
		t.Parallel()
		w := proOffWorld(t)
		p := rework(w)
		require.Len(t, p.Refused, 1)
		assert.Contains(t, p.Refused[0].Why, "no enabled route and no up friend serves tier pro: enable a route (nova-config route add")
	})
}

// Every verb that validates a tier goes through one check (tierServed): every string
// literal of the sprint's sources that refuses for want of an enabled route is inside it,
// and the deal's draw (routeOf), the tick's (noRoute) and the reads' (readRouteMissing)
// call it.
func TestATierCheckIsOneFunction(t *testing.T) {
	t.Parallel()
	files, err := filepath.Glob("*.go")
	require.NoError(t, err)
	fset := token.NewFileSet()
	calls := map[string]bool{}
	found := 0
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, err := os.ReadFile(f)
		require.NoError(t, err)
		file, err := parser.ParseFile(fset, f, src, 0)
		require.NoError(t, err)
		for _, d := range file.Decls {
			fn, ok := d.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				switch x := n.(type) {
				case *ast.BasicLit:
					if x.Kind == token.STRING && strings.Contains(x.Value, "no enabled route") {
						found++
						assert.Equal(t, "tierServed", fn.Name.Name, "%s: a refusal for want of a route outside the one check", fset.Position(x.Pos()))
					}
				case *ast.CallExpr:
					if sel, ok := x.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "tierServed" {
						calls[fn.Name.Name] = true
					}
				}
				return true
			})
		}
	}
	assert.Positive(t, found, "the one check's refusal is found")
	for _, caller := range []string{"routeOf", "readRouteMissing"} {
		assert.True(t, calls[caller], "%s goes through tierServed", caller)
	}
}
