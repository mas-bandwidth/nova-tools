package sprintfn

import (
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// TestRuleNotesJAccepts is the class test of the family "a rule's note names a
// cause J accepts": every NoteReq the sprint rules can write, each open, close,
// update, hold, unhold and know, goes through J's check (jCheck) and the first
// refusal fails the test, by the site that writes it.
//
// The sweep enumerates the note constructors, not the runs. Every rule writes
// its notes as a NoteReq literal in a non-test file of internal/sprint (the
// rules' tables and steps write sprint.Note, which J is not asked about), and
// driving each rule to each note kind would test the drive as much as the
// notes. A site whose op, type and cause are constants (a literal, a const, a
// lookup in a map var of constants) is run through jCheck as it stands. A site
// whose cause is a value the rule reads (a lateness kind, a row's name, a note
// already open, a need) cannot be run as it stands: it is pinned in
// dynamicCauses by its function and expression, with the values it takes,
// which the test runs through jCheck in its place, and a dynamic site that is
// not pinned fails the test, so a new one is decided when it is written.
// The models: tla/SprintEvents.tla (the judgment causes, "-" for those that
// have none), design 2.2 and 2.5 (the types), IT15 (J's check).
func TestRuleNotesJAccepts(t *testing.T) {
	t.Parallel()
	fset := token.NewFileSet()
	files, err := filepath.Glob(filepath.Join("..", "*.go"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no sources of internal/sprint: %v", err)
	}
	sort.Strings(files)
	var parsed []*ast.File
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		af, err := parser.ParseFile(fset, f, nil, 0)
		if err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		parsed = append(parsed, af)
	}
	ev := newStrEval(parsed)

	pinned := map[string]bool{}
	seen := map[string]bool{}
	sites, static, state := 0, 0, 0
	for _, af := range parsed {
		for _, d := range af.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok || fd.Body == nil {
				continue
			}
			ast.Inspect(fd.Body, func(n ast.Node) bool {
				cl, ok := n.(*ast.CompositeLit)
				if !ok {
					return true
				}
				if id, ok := cl.Type.(*ast.Ident); !ok || id.Name != "NoteReq" {
					return true
				}
				sites++
				where := fset.Position(cl.Pos()).String()
				fields := map[string]ast.Expr{}
				for _, e := range cl.Elts {
					if kv, ok := e.(*ast.KeyValueExpr); ok {
						if k, ok := kv.Key.(*ast.Ident); ok {
							fields[k.Name] = kv.Value
						}
					}
				}
				op, ok := ev.eval(fields["Op"])
				if !ok {
					t.Errorf("%s: the note's op is not a constant; a rule names its op by a literal or a const", where)
					return true
				}
				req := NoteReq{Op: op, Type: standIn(op), Subjects: []string{"x"}}
				if e, has := fields["Type"]; has {
					if typ, ok := ev.eval(e); ok {
						req.Type = typ
					}
				}
				isState := jIsStateOp(op)
				if isState {
					state++
				}
				e, has := fields["Cause"]
				switch {
				case !has:
					// no cause: a know may (2.5), a state op may not; jCheck decides.
					static++
					if ref := jCheck(0, req); ref != nil {
						t.Errorf("%s: J refuses the %s of %q with no cause: %+v", where, op, req.Type, ref)
					}
				default:
					if cause, ok := ev.eval(e); ok {
						static++
						req.Cause = cause
						if ref := jCheck(0, req); ref != nil {
							t.Errorf("%s: J refuses the %s of %q with the cause %q: %+v", where, op, req.Type, cause, ref)
						}
						return true
					}
					key := fd.Name.Name + " " + types.ExprString(e)
					seen[key] = true
					causes, ok := dynamicCauses[key]
					if !ok {
						t.Errorf("%s: the cause %s of a note in %s is a value the rule reads and is not pinned in dynamicCauses; pin it with the values it takes", where, types.ExprString(e), fd.Name.Name)
						return true
					}
					pinned[key] = true
					for _, cause := range causes() {
						req.Cause = cause
						if ref := jCheck(0, req); ref != nil {
							t.Errorf("%s: J refuses the %s of %q with the cause %q (%s): %+v", where, op, req.Type, cause, key, ref)
						}
					}
				}
				return true
			})
		}
	}
	for key := range dynamicCauses {
		if !seen[key] {
			t.Errorf("dynamicCauses pins %q, which no rule writes; remove it", key)
		}
	}
	// the sweep found the family: the notes of the fleet, held, position, review
	// and time rules, more than half of them state ops.
	if sites < 30 || state < 15 || static < 20 {
		t.Fatalf("the sweep found %d note sites (%d static, %d state ops); the rules write at least 30", sites, static, state)
	}
	// the check is not vacuous: the same state op with no cause is REQUEST.
	if ref := jCheck(0, NoteReq{Op: "close", Type: sprint.NCross, Subjects: []string{"x"}}); ref == nil || ref.Code != CodeRequest {
		t.Fatalf("J takes a close with no cause (%v): the sweep proves nothing", ref)
	}
}

// dynamicCauses are the note sites whose cause is a value the rule reads, by
// the function and the expression, each with the values it takes. A cause here
// is never empty: a lateness kind is a due kind (R11), a stall cause is the
// row's cause or else the row's name, a rule name names a rule, and a cause
// read from a note or a need is that note's or that need's, a subject J takes.
var dynamicCauses = map[string]func() []string{
	// R11's helpers: the kind a lateness judgment is raised under.
	"know cause":  latenessKinds, // a know may have none; J takes each kind
	"judge cause": latenessKinds,
	// R16's stall groups: the row's cause, the row's name when it has none.
	"planHeld g.cause": func() []string { return []string{"exhausted", "stranded", "review", "working", "waiting"} },
	// the review rules: the rule that refused a card.
	"reviewRefused rule": func() []string { return []string{"ask", "accept", "rework"} },
	// R12 raises a know on a note it names: the note's id.
	"planOverdue note": func() []string { return []string{"n1"} },
	// R13 unholds the type and cause a hold was made with: J wrote both.
	"planHold nf.Cause": func() []string { return []string{"-", "readers", "exhausted"} },
	// R7's close of a missing need: the need's id.
	"planNeed nv.Need": func() []string { return []string{"p1"} },
	// add's close of a missing need its part made (IT19, the model's madeclose):
	// the need's id.
	"AddMadeCloses n.ID": func() []string { return []string{"p1"} },
}

// latenessKinds are the due kinds that have a lateness judgment (sprint.DueKinds).
func latenessKinds() []string {
	var out []string
	for _, k := range sprint.DueKinds {
		if _, cause, ok := LatenessJudgment(k.Kind); ok {
			out = append(out, cause)
		}
	}
	return out
}

// standIn is a type J takes for an op, for a site whose type is a value the
// rule reads: a judgment of 2.2 for a state op, a notice of 2.5 for a know.
func standIn(op string) string {
	if jIsStateOp(op) {
		return sprint.NBound
	}
	return sprint.NMemberUp
}

// strEval evaluates the string constants of a package's non-test files: a
// literal, a const, a concatenation, and a lookup in a map var of constants.
type strEval struct {
	consts map[string]ast.Expr
	maps   map[string]*ast.CompositeLit
}

func newStrEval(files []*ast.File) *strEval {
	ev := &strEval{consts: map[string]ast.Expr{}, maps: map[string]*ast.CompositeLit{}}
	for _, f := range files {
		for _, d := range f.Decls {
			gd, ok := d.(*ast.GenDecl)
			if !ok {
				continue
			}
			for _, sp := range gd.Specs {
				vs, ok := sp.(*ast.ValueSpec)
				if !ok {
					continue
				}
				for i, name := range vs.Names {
					if i >= len(vs.Values) {
						continue
					}
					switch gd.Tok {
					case token.CONST:
						ev.consts[name.Name] = vs.Values[i]
					case token.VAR:
						if cl, ok := vs.Values[i].(*ast.CompositeLit); ok {
							if _, ok := cl.Type.(*ast.MapType); ok {
								ev.maps[name.Name] = cl
							}
						}
					}
				}
			}
		}
	}
	return ev
}

func (ev *strEval) eval(e ast.Expr) (string, bool) {
	switch x := e.(type) {
	case *ast.BasicLit:
		if x.Kind != token.STRING {
			return "", false
		}
		s, err := strconv.Unquote(x.Value)
		return s, err == nil
	case *ast.Ident:
		if v, ok := ev.consts[x.Name]; ok {
			return ev.eval(v)
		}
	case *ast.ParenExpr:
		return ev.eval(x.X)
	case *ast.BinaryExpr:
		if x.Op == token.ADD {
			a, ok1 := ev.eval(x.X)
			b, ok2 := ev.eval(x.Y)
			return a + b, ok1 && ok2
		}
	case *ast.IndexExpr:
		id, ok := x.X.(*ast.Ident)
		if !ok || ev.maps[id.Name] == nil {
			return "", false
		}
		key, ok := ev.eval(x.Index)
		if !ok {
			return "", false
		}
		for _, el := range ev.maps[id.Name].Elts {
			kv, ok := el.(*ast.KeyValueExpr)
			if !ok {
				continue
			}
			if k, ok := ev.eval(kv.Key); ok && k == key {
				return ev.eval(kv.Value)
			}
		}
	}
	return "", false
}
