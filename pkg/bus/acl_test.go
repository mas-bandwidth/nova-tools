package bus

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// friendACL is the least set per friend as SPEC-BUS.md writes it: the fenced
// line that starts "ACL SETUSER <f>", its lines joined.
func friendACL(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile("../../docs/SPEC-BUS.md")
	require.NoError(t, err)
	doc := string(raw)
	at := strings.Index(doc, "```\nACL SETUSER <f> ")
	require.GreaterOrEqual(t, at, 0, "SPEC-BUS.md writes the friend's ACL line")
	block := doc[at+4:]
	block = block[:strings.Index(block, "```")]
	return strings.Join(strings.Fields(block), " ")
}

// aclRules is an ACL SETUSER line split into the root's rules and each
// selector's, a selector being the rules inside one pair of parentheses.
func aclRules(line string) (root []string, selectors [][]string) {
	var sel []string
	in := false
	for _, w := range strings.Fields(line) {
		switch {
		case !in && strings.HasPrefix(w, "("):
			in, sel = true, nil
			w = strings.TrimPrefix(w, "(")
		}
		if !in {
			root = append(root, w)
			continue
		}
		end := strings.HasSuffix(w, ")")
		if w = strings.TrimSuffix(w, ")"); w != "" {
			sel = append(sel, w)
		}
		if end {
			selectors = append(selectors, sel)
			in = false
		}
	}
	return root, selectors
}

// overwrites are the commands that read or replace a key of any type by its
// name alone; a send needs them on its token's record and nowhere else.
var overwrites = map[string]bool{"+get": true, "+set": true, "+getset": true, "+setex": true, "+psetex": true, "+getdel": true, "+del": true, "+unlink": true, "+rename": true, "+copy": true, "+restore": true, "+@all": true, "+@write": true, "+@string": true, "+@keyspace": true, "allcommands": true}

// aclBreaches says where a friend's ACL line lets a friend read or overwrite
// a key that is no token record of its own: an overwriting command on the
// root (which reaches every key pattern the user has), or in a selector
// whose keys are wider than ~bus2:sent:<f>:*.
func aclBreaches(line string) []string {
	var out []string
	root, selectors := aclRules(line)
	var keys []string
	for _, r := range root {
		if strings.HasPrefix(r, "~") || strings.HasPrefix(r, "%") {
			keys = append(keys, r)
		}
	}
	for _, r := range root {
		if overwrites[r] {
			out = append(out, r+" on the root reaches "+strings.Join(keys, " "))
		}
	}
	for _, sel := range selectors {
		var cmds, wide []string
		for _, r := range sel {
			switch {
			case overwrites[r]:
				cmds = append(cmds, r)
			case r == "~bus2:sent:<f>:*":
			case strings.HasPrefix(r, "~") || strings.HasPrefix(r, "%") || r == "allkeys":
				wide = append(wide, r)
			}
		}
		if len(cmds) > 0 && len(wide) > 0 {
			out = append(out, strings.Join(cmds, " ")+" in a selector reaches "+strings.Join(wide, " "))
		}
	}
	return out
}

// The least set per friend in SPEC-BUS.md gives GET and SET on the sender's
// own token records and on nothing else, so no friend can SET friends or
// bus2:log; the send-once script's keys are still on the root, so EVAL runs.
// The reversed witnesses are the line attempt 2 wrote (+get +set on the root)
// and a selector wider than the records: each is a breach.
func TestTheFriendACLSetsOnlyItsOwnTokenRecords(t *testing.T) {
	t.Parallel()
	line := friendACL(t)
	assert.Empty(t, aclBreaches(line), line)

	root, selectors := aclRules(line)
	assert.Contains(t, root, "~bus2:sent:<f>:*", "EVAL's declared keys pass on the root")
	assert.Contains(t, root, "+eval")
	assert.Contains(t, root, "+evalsha")
	assert.Contains(t, selectors, []string{"~bus2:sent:<f>:*", "+get", "+set"})

	t.Run("reversed: +get +set on the root", func(t *testing.T) {
		wide := strings.Replace(line, " (~bus2:sent:<f>:* +get +set)", " +get +set", 1)
		require.NotEqual(t, line, wide)
		got := aclBreaches(wide)
		require.Len(t, got, 2)
		assert.Contains(t, got[0], "+get on the root reaches")
		assert.Contains(t, got[1], "~friends")
		assert.Contains(t, got[1], "~bus2:log")
	})
	t.Run("reversed: a selector wider than the records", func(t *testing.T) {
		got := aclBreaches(strings.Replace(line, "(~bus2:sent:<f>:* +get +set)", "(~bus2:sent:<f>:* ~friends +get +set)", 1))
		assert.Equal(t, []string{"+get +set in a selector reaches ~friends"}, got)
	})
}

// The line is a checked artifact (SPEC-BUS.md, the ACL per friend): the keys
// and commands pkg/bus uses are derived from its Go and Lua source, and the
// line must grant each of them and nothing else. Redis is the outside judge
// (TestRedisFriendACLSetsOnlyItsOwnTokenRecords applies the same line to a
// real server); this is the unit tier, which opens no store and no clock.

// aclFamily is one key family pkg/bus builds: the constant that names it,
// its value, and a sample key of it for a friend named ada.
type aclFamily struct{ Const, Value, Sample, Where string }

// aclCommand is one Redis command pkg/bus sends, by its ACL spelling
// (XGROUP|CREATE), the go-redis method or "redis.call" that sends it and where.
type aclCommand struct{ Name, Via, Where string }

type aclNeeds struct {
	Families []aclFamily
	Commands []aclCommand
	Unmapped []string // go-redis calls that goRedisCommands does not map to a command
}

// goRedisCommands maps the go-redis methods pkg/bus calls on its client
// (r.C) and its pipelines (pipe) to the commands they send. A method that
// sends no command of its own maps to none; a method that is not here is a
// failure, so a new call never passes unseen.
var goRedisCommands = map[string][]string{
	"SMembers": {"SMEMBERS"}, "SAdd": {"SADD"}, "Time": {"TIME"}, "XAdd": {"XADD"},
	"HDel": {"HDEL"}, "HSet": {"HSET"}, "HSetNX": {"HSETNX"}, "HGetAll": {"HGETALL"}, "Get": {"GET"},
	"XGroupCreateMkStream": {"XGROUP|CREATE"}, "XAutoClaim": {"XAUTOCLAIM"}, "XReadGroup": {"XREADGROUP"},
	"XRead": {"XREAD"}, "XAck": {"XACK"}, "XPendingExt": {"XPENDING"}, "XInfoGroups": {"XINFO|GROUPS"},
	"XInfoStream": {"XINFO|STREAM"}, "XRange": {"XRANGE"}, "XRangeN": {"XRANGE"},
	"TxPipeline": {"MULTI", "EXEC"}, // EXEC runs the transaction; MULTI opens it
	"Pipeline":   nil, "Options": nil, "Exec": nil,
	"Do": nil, // derive the command from this call's actual argument
}

var luaCall = regexp.MustCompile(`redis\.p?call\(\s*['"]([A-Za-z]+)['"]`)

// busSources reads the non-test Go files of dir, by file name.
func busSources(t *testing.T, dir string) map[string]string {
	t.Helper()
	names, err := filepath.Glob(filepath.Join(dir, "*.go"))
	require.NoError(t, err)
	out := map[string]string{}
	for _, n := range names {
		if strings.HasSuffix(n, "_test.go") {
			continue
		}
		raw, err := os.ReadFile(n)
		require.NoError(t, err)
		out[filepath.Base(n)] = string(raw)
	}
	require.NotEmpty(t, out, dir)
	return out
}

func parseAll(t *testing.T, srcs map[string]string) (*token.FileSet, map[string]*ast.File) {
	t.Helper()
	fset := token.NewFileSet()
	files := map[string]*ast.File{}
	for name, text := range srcs {
		f, err := parser.ParseFile(fset, name, text, 0)
		require.NoError(t, err, name)
		files[name] = f
	}
	return fset, files
}

func sortedFiles(files map[string]*ast.File) []string {
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

func stringLit(e ast.Expr) (string, bool) {
	l, ok := e.(*ast.BasicLit)
	if !ok || l.Kind != token.STRING {
		return "", false
	}
	s, err := strconv.Unquote(l.Value)
	return s, err == nil
}

// constText is the text an expression of string constants, literals and +
// stands for; ok is false for anything else.
func constText(e ast.Expr, consts map[string]string, params map[string]string) (string, bool) {
	switch e := e.(type) {
	case *ast.BasicLit:
		return stringLit(e)
	case *ast.Ident:
		if v, ok := params[e.Name]; ok {
			return v, true
		}
		v, ok := consts[e.Name]
		return v, ok
	case *ast.ParenExpr:
		return constText(e.X, consts, params)
	case *ast.BinaryExpr:
		if e.Op != token.ADD {
			return "", false
		}
		l, ok1 := constText(e.X, consts, params)
		r, ok2 := constText(e.Y, consts, params)
		return l + r, ok1 && ok2
	}
	return "", false
}

// deriveACLNeeds reads the source of pkg/bus and says every key family and
// every command it sends: the key families are the string constants that
// start bus2: and those passed as the key of a client call; the commands are
// the client's calls by their command names, every redis.call in a script's
// Lua, the scripts' own EVALSHA and EVAL, and the []any{"COMMAND", ...} a Do
// is given.
func deriveACLNeeds(t *testing.T, srcs map[string]string) aclNeeds {
	t.Helper()
	fset, files := parseAll(t, srcs)
	where := func(n ast.Node) string {
		p := fset.Position(n.Pos())
		return fmt.Sprintf("%s:%d", p.Filename, p.Line)
	}
	consts := map[string]string{}
	constAt := map[string]string{}
	builders := map[string]*ast.FuncDecl{}
	scripts := map[string]string{} // package var -> its Lua
	for _, fn := range sortedFiles(files) {
		for _, d := range files[fn].Decls {
			switch d := d.(type) {
			case *ast.GenDecl:
				for _, sp := range d.Specs {
					vs, ok := sp.(*ast.ValueSpec)
					if !ok || len(vs.Names) != len(vs.Values) {
						continue
					}
					for i, n := range vs.Names {
						if d.Tok == token.CONST {
							if v, ok := stringLit(vs.Values[i]); ok {
								consts[n.Name], constAt[n.Name] = v, where(n)
							}
						}
					}
				}
			case *ast.FuncDecl:
				if d.Recv == nil {
					builders[d.Name.Name] = d
				}
			}
		}
	}
	// the scripts: var x = redis.NewScript(<text>)
	for _, fn := range sortedFiles(files) {
		for _, d := range files[fn].Decls {
			gd, ok := d.(*ast.GenDecl)
			if !ok || gd.Tok != token.VAR {
				continue
			}
			for _, sp := range gd.Specs {
				vs := sp.(*ast.ValueSpec)
				for i, v := range vs.Values {
					call, ok := v.(*ast.CallExpr)
					if !ok || types.ExprString(call.Fun) != "redis.NewScript" || len(call.Args) != 1 {
						continue
					}
					if text, ok := constText(call.Args[0], consts, nil); ok {
						scripts[vs.Names[i].Name] = text
					}
				}
			}
		}
	}

	var needs aclNeeds
	family := map[string]bool{}
	for n, v := range consts {
		if strings.HasPrefix(v, "bus2:") {
			family[n] = true
		}
	}
	add := func(name, via string, n ast.Node) {
		needs.Commands = append(needs.Commands, aclCommand{Name: name, Via: via, Where: where(n)})
	}
	for _, fn := range sortedFiles(files) {
		ast.Inspect(files[fn], func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.CallExpr:
				sel, ok := n.Fun.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				recv := types.ExprString(sel.X)
				if _, isScript := scripts[recv]; isScript {
					switch sel.Sel.Name {
					case "Run":
						add("EVALSHA", recv+".Run", n)
						add("EVAL", recv+".Run", n)
					case "Eval":
						add("EVAL", recv+".Eval", n)
					}
					return true
				}
				if !redisReceiver(sel.X, 0) {
					return true
				}
				cmds, known := goRedisCommands[sel.Sel.Name]
				if !known {
					needs.Unmapped = append(needs.Unmapped, sel.Sel.Name+" at "+where(n))
					return true
				}
				if sel.Sel.Name == "Do" {
					if command, ok := doCommand(n, consts, files[fn]); ok {
						add(command, "Do", n)
					} else {
						needs.Unmapped = append(needs.Unmapped, "Do command unresolved at "+where(n))
					}
				}
				for _, c := range cmds {
					add(c, sel.Sel.Name, n)
				}
				if len(n.Args) > 1 {
					if id, ok := n.Args[1].(*ast.Ident); ok {
						if _, isConst := consts[id.Name]; isConst {
							family[id.Name] = true // a constant passed as the key
						}
					}
				}
			}
			return true
		})
	}
	for name, text := range scripts {
		for _, m := range luaCall.FindAllStringSubmatch(text, -1) {
			needs.Commands = append(needs.Commands, aclCommand{Name: strings.ToUpper(m[1]), Via: "redis.call in " + name, Where: name})
		}
	}

	for name := range family {
		sample := consts[name]
		if strings.HasSuffix(sample, ":") {
			sample += "ada"
		}
		// a key builder (func StreamOf(name) string { return Prefix + name }) says
		// how the family's keys are put together: the first argument is the
		// friend ada, any other is a token or an id
		for _, b := range builders {
			if b.Body == nil || len(b.Body.List) != 1 || b.Type.Params == nil {
				continue
			}
			ret, ok := b.Body.List[0].(*ast.ReturnStmt)
			if !ok || len(ret.Results) != 1 || !mentions(ret.Results[0], name) {
				continue
			}
			params := map[string]string{}
			first := true
			for _, f := range b.Type.Params.List {
				for _, pn := range f.Names {
					params[pn.Name] = "t"
					if first {
						params[pn.Name], first = "ada", false
					}
				}
			}
			if s, ok := constText(ret.Results[0], consts, params); ok {
				sample = s
			}
		}
		needs.Families = append(needs.Families, aclFamily{Const: name, Value: consts[name], Sample: sample, Where: constAt[name]})
	}
	sort.Slice(needs.Families, func(i, j int) bool { return needs.Families[i].Const < needs.Families[j].Const })
	sort.Slice(needs.Commands, func(i, j int) bool {
		a, b := needs.Commands[i], needs.Commands[j]
		return a.Name+a.Where < b.Name+b.Where
	})
	sort.Strings(needs.Unmapped)
	return needs
}

// initialValue reads a local declaration without executing the program.
func initialValue(id *ast.Ident) ast.Expr {
	if id.Obj == nil {
		return nil
	}
	switch decl := id.Obj.Decl.(type) {
	case *ast.AssignStmt:
		for i, lhs := range decl.Lhs {
			if types.ExprString(lhs) == id.Name && i < len(decl.Rhs) {
				return decl.Rhs[i]
			}
		}
	case *ast.ValueSpec:
		for i, name := range decl.Names {
			if name.Name == id.Name && i < len(decl.Values) {
				return decl.Values[i]
			}
		}
	}
	return nil
}

// redisReceiver follows local client/pipeline aliases. The depth bound makes
// malformed cyclic source terminate; real calls use the client or a pipeline.
func redisReceiver(e ast.Expr, depth int) bool {
	if e == nil || depth > 16 {
		return false
	}
	switch e := e.(type) {
	case *ast.SelectorExpr:
		return e.Sel.Name == "C"
	case *ast.Ident:
		if e.Name == "pipe" {
			return true
		}
		if e.Obj != nil {
			if field, ok := e.Obj.Decl.(*ast.Field); ok {
				return slices.Contains([]string{"*redis.Client", "redis.Pipeliner", "redis.Cmdable", "redis.UniversalClient"}, types.ExprString(field.Type))
			}
		}
		return redisReceiver(initialValue(e), depth+1)
	case *ast.CallExpr:
		if sel, ok := e.Fun.(*ast.SelectorExpr); ok && (sel.Sel.Name == "Pipeline" || sel.Sel.Name == "TxPipeline") {
			return redisReceiver(sel.X, depth+1)
		}
	}
	return false
}

func refersTo(n ast.Node, object *ast.Ident) bool {
	found := false
	ast.Inspect(n, func(n ast.Node) bool {
		if id, ok := n.(*ast.Ident); ok && id.Obj == object.Obj {
			found = true
		}
		return !found
	})
	return found
}

// stableCommandArgs accepts the current tail-append construction only. A
// replacement, indexed write, alias or opaque call can change the command;
// fail closed instead of claiming its initializer is still the command.
func stableCommandArgs(file *ast.File, object *ast.Ident, use *ast.CallExpr) bool {
	stable := true
	appendOnly := func(e ast.Expr) bool {
		call, ok := e.(*ast.CallExpr)
		if !ok || types.ExprString(call.Fun) != "append" || len(call.Args) == 0 {
			return false
		}
		id, ok := call.Args[0].(*ast.Ident)
		return ok && id.Obj == object.Obj
	}
	ast.Inspect(file, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.AssignStmt:
			if n == object.Obj.Decl {
				break
			}
			for _, lhs := range n.Lhs {
				if refersTo(lhs, object) && !(len(n.Lhs) == 1 && len(n.Rhs) == 1 && types.ExprString(lhs) == object.Name && appendOnly(n.Rhs[0])) {
					stable = false
				}
			}
			for _, rhs := range n.Rhs {
				if refersTo(rhs, object) && !appendOnly(rhs) {
					stable = false
				}
			}
		case *ast.ValueSpec:
			for _, value := range n.Values {
				if refersTo(value, object) {
					stable = false
				}
			}
		case *ast.CallExpr:
			if n == use || appendOnly(n) {
				break
			}
			for _, arg := range n.Args {
				if _, closure := arg.(*ast.FuncLit); closure {
					continue
				} // its body is inspected separately
				if refersTo(arg, object) {
					stable = false
				}
			}
		}
		return stable
	})
	return stable
}

// doCommand follows a Do call's literal command or its slice initializer.
// A dynamic command fails closed; unrelated slices are not Redis calls.
func doCommand(call *ast.CallExpr, consts map[string]string, file *ast.File) (string, bool) {
	if len(call.Args) < 2 {
		return "", false
	}
	e := call.Args[1]
	if call.Ellipsis.IsValid() {
		if id, ok := e.(*ast.Ident); ok {
			if id.Obj == nil || !stableCommandArgs(file, id, call) {
				return "", false
			}
			e = initialValue(id)
		}
		list, ok := e.(*ast.CompositeLit)
		if !ok || len(list.Elts) == 0 {
			return "", false
		}
		e = list.Elts[0]
	}
	command, ok := constText(e, consts, nil)
	return strings.ToUpper(command), ok && command != ""
}

func mentions(e ast.Expr, name string) bool {
	found := false
	ast.Inspect(e, func(n ast.Node) bool {
		if id, ok := n.(*ast.Ident); ok && id.Name == name {
			found = true
		}
		return !found
	})
	return found
}

// loginCommands are the commands pkg/redisconn sends for every connection of
// every verb: the probe it sends once open (a constant named probe) and the
// HELLO that go-redis sends when the client's Protocol is set.
func loginCommands(t *testing.T, srcs map[string]string) []aclCommand {
	t.Helper()
	fset, files := parseAll(t, srcs)
	var out []aclCommand
	for _, fn := range sortedFiles(files) {
		ast.Inspect(files[fn], func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.ValueSpec:
				for i, id := range n.Names {
					if id.Name == "probe" && i < len(n.Values) {
						if s, ok := stringLit(n.Values[i]); ok {
							out = append(out, aclCommand{Name: strings.ToUpper(s), Via: "redisconn probe", Where: fmt.Sprintf("%s:%d", fn, fset.Position(n.Pos()).Line)})
						}
					}
				}
			case *ast.KeyValueExpr:
				if id, ok := n.Key.(*ast.Ident); ok && id.Name == "Protocol" {
					out = append(out, aclCommand{Name: "HELLO", Via: "go-redis handshake (Protocol set)", Where: fmt.Sprintf("%s:%d", fn, fset.Position(n.Pos()).Line)})
				}
			}
			return true
		})
	}
	return out
}

// aclPolicy says what a friend does with a key family, which the source does
// not: the families themselves are derived, and a derived family that is not
// classified here fails.
type aclPolicy struct {
	// Kind: "writes" (a friend's verb writes it), "reads" (a friend's verbs
	// only read it) or "server" (only a server writes it; the line grants
	// nothing for it).
	Kind string
	// Own: a friend writes only the keys of her own name (the key is built
	// from the sender), so the line pins <f> and no other friend's is writable.
	Own bool
	// Others: a friend's verb also reads other names' keys of the family.
	Others bool
	Why    string
}

// aclFamilies classifies the key families of pkg/bus by the constant that
// names them.
var aclFamilies = map[string]aclPolicy{
	"Prefix":           {Kind: "writes", Why: "send: XADD to every recipient's stream; recv, wait, ack, peek: its own"},
	"LogKey":           {Kind: "writes", Why: "send: XADD; log: XRANGE"},
	"OwedPrefix":       {Kind: "writes", Why: "send: HSET for every friend recipient; ack and a reply: HDEL"},
	"StagePrefix":      {Kind: "writes", Why: "recv and stamp: the receipt script; a reply acts in the send"},
	"SentPrefix":       {Kind: "writes", Own: true, Why: "send --token: the token's record, GET and SET in a selector"},
	"AskPrefix":        {Kind: "writes", Why: "send --kind ask: HSET bus2:ask:<id>, a new id, so no name to pin; asks reads the records"},
	"AsksPrefix":       {Kind: "writes", Own: true, Others: true, Why: "send --kind ask: HSET the sender's index; asks --liveness and overdue read every name's"},
	"AnswersPrefix":    {Kind: "writes", Own: true, Others: true, Why: "every reply: HSETNX the sender's own; asks reads the recipients'"},
	"friendsKey":       {Kind: "reads", Why: "every verb: SMEMBERS; SADD is the enroller's (nova-sprint friend sync)"},
	"machinesKey":      {Kind: "reads", Why: "every verb: SMEMBERS; written by nova-config apply"},
	"AskTimeoutPrefix": {Kind: "server", Why: "ExpireAsks' once-key: written by nova-bus overdue (the coordinator's loop) and nova-hook serve, each under its own server login, never a friend's line"},
}

// aclServerOnlyCommands are commands pkg/bus sends that no friend verb does.
var aclServerOnlyCommands = map[string]string{
	"SADD": "Redis.Enroll: nova-sprint friend sync tells the bus store the friend rows, under the coordinator's login",
}

type aclPattern struct {
	Rule, Pattern string
	Read, Write   bool
	Scope         int
}

type aclLine struct {
	Patterns []aclPattern
	Grants   []string   // "+hset", "+xgroup|create"
	Other    []string   // a rule that is neither a key pattern nor a command grant
	Scopes   [][]string // root first, then independent selectors
}

// parseACLLine reads the rules of the line: the root's and every selector's
// key patterns (<f> being ada) and command grants.
func parseACLLine(line string) aclLine {
	var out aclLine
	root, selectors := aclRules(strings.TrimPrefix(line, "ACL SETUSER <f> "))
	out.Scopes = append([][]string{root}, selectors...)
	for scope, rules := range out.Scopes {
		for _, r := range rules {
			switch {
			case strings.HasPrefix(r, "~"):
				out.Patterns = append(out.Patterns, aclPattern{r, strings.ReplaceAll(strings.TrimPrefix(r, "~"), "<f>", "ada"), true, true, scope})
			case strings.HasPrefix(r, "%R~"):
				out.Patterns = append(out.Patterns, aclPattern{r, strings.ReplaceAll(strings.TrimPrefix(r, "%R~"), "<f>", "ada"), true, false, scope})
			case strings.HasPrefix(r, "%W~"), strings.HasPrefix(r, "%RW~"):
				out.Patterns = append(out.Patterns, aclPattern{r, strings.ReplaceAll(r[strings.Index(r, "~")+1:], "<f>", "ada"), strings.HasPrefix(r, "%RW~"), true, scope})
			case r == "allkeys":
				out.Patterns = append(out.Patterns, aclPattern{r, "*", true, true, scope})
			case strings.HasPrefix(r, "+"):
				out.Grants = append(out.Grants, strings.ToLower(r))
			case r == "on", r == "resetchannels", strings.HasPrefix(r, ">"):
			default:
				out.Other = append(out.Other, r)
			}
		}
	}
	return out
}

// globMatch is Redis's key glob for the patterns the line uses: * ? and
// [..], as path.Match reads them (Redis's * also crosses a slash, which no
// key here holds).
func globMatch(pattern, key string) bool {
	ok, err := path.Match(pattern, key)
	return err == nil && ok
}

func (l aclLine) matches(key string, writing bool) (string, bool) {
	return l.matchesScope(key, writing, -1)
}

func (l aclLine) matchesScope(key string, writing bool, scope int) (string, bool) {
	for _, p := range l.Patterns {
		if scope >= 0 && p.Scope != scope {
			continue
		}
		if ((writing && p.Write) || (!writing && p.Read)) && globMatch(p.Pattern, key) {
			return p.Rule, true
		}
	}
	return "", false
}

// aclProblems is every way the line differs from what pkg/bus needs: a key
// family or a command nothing grants, and a grant nothing uses.
func aclProblems(line string, needs aclNeeds, login []aclCommand) []string {
	var out []string
	l := parseACLLine(line)
	for _, u := range needs.Unmapped {
		out = append(out, "the go-redis call "+u+" is mapped to no command: add it to goRedisCommands")
	}
	for _, o := range l.Other {
		out = append(out, "the line has a rule this check does not know: "+o)
	}

	// the key families
	seen := map[string]bool{}
	var usable []aclFamily // the families the line is allowed to grant
	for _, f := range needs.Families {
		seen[f.Const] = true
		p, ok := aclFamilies[f.Const]
		if !ok {
			out = append(out, fmt.Sprintf("key family %s = %q (%s) is classified nowhere: grant it on the line, or name it in aclFamilies (writes, reads or server) with the verb and the user", f.Const, f.Value, f.Where))
			continue
		}
		if p.Kind == "server" {
			readRule, read := l.matches(f.Sample, false)
			writeRule, write := l.matches(f.Sample, true)
			if rule, hit := readRule+writeRule, read || write; hit {
				out = append(out, fmt.Sprintf("key family %s (%s) is server-only (%s) but the line grants it with %s", f.Const, f.Sample, p.Why, rule))
			}
			continue
		}
		usable = append(usable, f)
		_, readable := l.matchesScope(f.Sample, false, 0)
		_, writable := l.matchesScope(f.Sample, true, 0)
		switch {
		case !readable:
			out = append(out, fmt.Sprintf("key family %s = %q (%s): no key pattern on the line matches %s", f.Const, f.Value, f.Where, f.Sample))
		case p.Kind == "writes" && !writable:
			out = append(out, fmt.Sprintf("key family %s = %q (%s): a friend's verb writes it (%s) and no read-write pattern on the line matches %s", f.Const, f.Value, f.Where, p.Why, f.Sample))
		}
		_, anyWrite := l.matches(f.Sample, true)
		if p.Kind == "reads" && anyWrite {
			out = append(out, fmt.Sprintf("key family %s is read-only (%s), but the line grants write access", f.Const, p.Why))
		}
		other := strings.Replace(f.Sample, "ada", "bob", 1)
		if p.Own {
			if rule, hit := l.matches(other, true); hit {
				out = append(out, fmt.Sprintf("key family %s: a friend writes only her own (%s), but %s lets her write %s", f.Const, p.Why, rule, other))
			}
		}
		if p.Others {
			if _, hit := l.matchesScope(other, false, 0); !hit {
				out = append(out, fmt.Sprintf("key family %s: a friend's verb reads other names' keys (%s) and no pattern on the line lets her read %s", f.Const, p.Why, other))
			}
		}
	}
	for name := range aclFamilies {
		if !seen[name] {
			out = append(out, "aclFamilies names "+name+", which pkg/bus no longer has: remove it")
		}
	}
	for _, p := range l.Patterns {
		used := false
		for _, f := range usable {
			if globMatch(p.Pattern, f.Sample) || globMatch(p.Pattern, strings.Replace(f.Sample, "ada", "bob", 1)) && aclFamilies[f.Const].Others {
				used = true
			}
		}
		if !used {
			out = append(out, fmt.Sprintf("surplus: the key pattern %s on the line matches no key family pkg/bus builds", p.Rule))
		}
	}

	// the commands
	granted := map[string]bool{}
	for _, g := range l.Grants {
		granted[g] = true
	}
	used := map[string]bool{}
	reported := map[string]bool{}
	all := append(slices.Clone(needs.Commands), login...)
	sites := map[string][]string{} // command -> every call that sends it
	for _, c := range all {
		sites[c.Name] = append(sites[c.Name], c.Via+" at "+c.Where)
	}
	for _, c := range all {
		g := "+" + strings.ToLower(c.Name)
		if why, server := aclServerOnlyCommands[c.Name]; server {
			if granted[g] && !reported[g] {
				reported[g] = true
				out = append(out, fmt.Sprintf("command %s is server-only (%s) but the line grants %s", c.Name, why, g))
			}
			continue
		}
		used[g] = true
		covered := slices.Contains(l.Scopes[0], g)
		if c.Name == "GET" || c.Name == "SET" {
			covered = false
			for scope, rules := range l.Scopes {
				if !slices.Contains(rules, g) {
					continue
				}
				for _, pattern := range l.Patterns {
					if pattern.Scope == scope && pattern.Read && (c.Name != "SET" || pattern.Write) && globMatch(pattern.Pattern, "bus2:sent:ada:t") {
						covered = true
					}
				}
			}
		}
		if !covered && !reported[g] {
			reported[g] = true
			out = append(out, fmt.Sprintf("command %s (%s) is granted by no usable %s in its required scope on the line", c.Name, strings.Join(slices.Compact(sites[c.Name]), "; "), g))
		}
	}
	for name := range aclServerOnlyCommands {
		found := false
		for _, c := range needs.Commands {
			found = found || c.Name == name
		}
		if !found {
			out = append(out, "aclServerOnlyCommands names "+name+", which pkg/bus no longer sends: remove it")
		}
	}
	for _, g := range slices.Compact(slices.Sorted(slices.Values(l.Grants))) {
		if !used[g] {
			out = append(out, fmt.Sprintf("surplus: the line grants %s, which no verb of pkg/bus uses", g))
		}
	}
	return out
}

// The friend's line grants exactly the keys and commands pkg/bus sends: the
// key families and commands are read from pkg/bus's Go and Lua, so a family
// or a call added without a grant, or without a named reason it needs none,
// fails here, and so does a grant nothing uses. This is the bug the ask
// records were: the line was a hand-kept copy of what the code did.
func TestTheFriendACLCoversEveryBusKeyFamily(t *testing.T) {
	t.Parallel()
	line := friendACL(t)
	srcs := busSources(t, ".")
	needs := deriveACLNeeds(t, srcs)
	login := loginCommands(t, busSources(t, "../redisconn"))

	// the derivation sees what the files hold, or this test proves nothing
	var fams, cmds []string
	for _, f := range needs.Families {
		fams = append(fams, f.Const)
	}
	for _, c := range needs.Commands {
		cmds = append(cmds, c.Name)
	}
	for _, want := range []string{"Prefix", "LogKey", "OwedPrefix", "StagePrefix", "SentPrefix", "AskPrefix", "AsksPrefix", "AskTimeoutPrefix", "AnswersPrefix", "friendsKey", "machinesKey"} {
		assert.Contains(t, fams, want)
	}
	for _, want := range []string{"HSETNX", "XCLAIM", "XGROUP|CREATE", "EVALSHA", "EVAL", "GET", "SET", "XADD", "HGET", "TIME", "MULTI", "EXEC", "SADD"} {
		assert.Contains(t, cmds, want)
	}
	assert.Equal(t, []string{"HELLO", "PING"}, func() []string {
		var n []string
		for _, c := range login {
			n = append(n, c.Name)
		}
		sort.Strings(n)
		return n
	}())

	assert.Empty(t, aclProblems(line, needs, login), line)

	t.Run("reversed: the line without +hsetnx", func(t *testing.T) {
		got := aclProblems(strings.Replace(line, " +hsetnx", "", 1), needs, login)
		require.Len(t, got, 1, got)
		assert.Contains(t, got[0], "HSETNX")
		assert.Contains(t, got[0], "HSetNX")
	})
	t.Run("reversed: the line without +xclaim", func(t *testing.T) {
		got := aclProblems(strings.Replace(line, " +xclaim", "", 1), needs, login)
		require.Len(t, got, 1, got)
		assert.Contains(t, got[0], "XCLAIM")
	})
	t.Run("reversed: the line without ~bus2:answers:<f>", func(t *testing.T) {
		got := aclProblems(strings.Replace(line, " ~bus2:answers:<f>", "", 1), needs, login)
		require.NotEmpty(t, got)
		assert.Contains(t, got[0], "AnswersPrefix")
		assert.Contains(t, got[0], "no read-write pattern")
	})
	t.Run("reversed: answers writable for every friend", func(t *testing.T) {
		got := aclProblems(strings.Replace(line, " ~bus2:answers:<f>", " ~bus2:answers:*", 1), needs, login)
		require.NotEmpty(t, got)
		assert.Contains(t, got[0], "AnswersPrefix")
		assert.Contains(t, got[0], "bus2:answers:bob")
	})
	t.Run("reversed: other names' answers not readable", func(t *testing.T) {
		got := aclProblems(strings.Replace(line, " %R~bus2:answers:*", "", 1), needs, login)
		require.NotEmpty(t, got)
		assert.Contains(t, got[0], "AnswersPrefix")
		assert.Contains(t, got[0], "read")
	})
	t.Run("reversed: the server's once-key granted", func(t *testing.T) {
		got := aclProblems(strings.Replace(line, " ~bus2:ask:*", " ~bus2:ask:* ~bus2:asktimeout:*", 1), needs, login)
		require.NotEmpty(t, got)
		assert.Contains(t, got[0], "AskTimeoutPrefix")
		assert.Contains(t, got[0], "server-only")
	})
	t.Run("reversed: a new key constant", func(t *testing.T) {
		more := cloneSrcs(srcs)
		more["new.go"] = "package bus\n\nconst NewPrefix = \"bus2:new:\"\n"
		got := aclProblems(line, deriveACLNeeds(t, more), login)
		require.Len(t, got, 1, got)
		assert.Contains(t, got[0], "NewPrefix")
		assert.Contains(t, got[0], "bus2:new:")
	})
	t.Run("reversed: a new redis call", func(t *testing.T) {
		more := cloneSrcs(srcs)
		more["new.go"] = "package bus\n\nimport \"context\"\n\nfunc (r Redis) Flush(ctx context.Context) error { return r.C.FlushAll(ctx).Err() }\n"
		got := aclProblems(line, deriveACLNeeds(t, more), login)
		require.Len(t, got, 1, got)
		assert.Contains(t, got[0], "FlushAll")
	})
	t.Run("reversed: a new command in a script", func(t *testing.T) {
		more := cloneSrcs(srcs)
		more["new.go"] = "package bus\n\nimport \"github.com/redis/go-redis/v9\"\n\nvar wipe = redis.NewScript(`return redis.call('FLUSHALL')`)\n"
		got := aclProblems(line, deriveACLNeeds(t, more), login)
		require.Len(t, got, 1, got)
		assert.Contains(t, got[0], "FLUSHALL")
	})
	t.Run("reversed: the line with an extra +flushall", func(t *testing.T) {
		got := aclProblems(line+" +flushall", needs, login)
		require.Len(t, got, 1, got)
		assert.Contains(t, got[0], "surplus")
		assert.Contains(t, got[0], "+flushall")
	})
	t.Run("reversed: the line with a key nothing builds", func(t *testing.T) {
		got := aclProblems(strings.Replace(line, " %R~friends", " %R~friends ~bus2:push", 1), needs, login)
		require.Len(t, got, 1, got)
		assert.Contains(t, got[0], "surplus")
		assert.Contains(t, got[0], "~bus2:push")
	})
	for _, row := range []struct{ name, from, to, want string }{
		{"write-only roster", "%R~friends", "%W~friends", "friendsKey"},
		{"writable roster", "%R~machines", "~machines", "read-only"},
		{"command on unrelated selector", " +hsetnx", "", "HSETNX"},
		{"key on unrelated selector", " ~bus2:answers:<f>", "", "AnswersPrefix"},
	} {
		t.Run("reversed: "+row.name, func(t *testing.T) {
			mutant := strings.Replace(line, row.from, row.to, 1)
			if row.name == "command on unrelated selector" {
				mutant += " (~bus2:sent:<f>:* +hsetnx)"
			}
			if row.name == "key on unrelated selector" {
				mutant += " (~bus2:answers:<f> +get)"
			}
			got := aclProblems(mutant, needs, login)
			assert.Contains(t, strings.Join(got, "\n"), row.want)
		})
	}
	for _, row := range []struct{ name, source, want string }{
		{"direct Do", `func (r Redis) probe(ctx any) { r.C.Do(ctx, "FLUSHALL") }`, "FLUSHALL"},
		{"slice Do", `func (r Redis) probe(ctx any) { args := []any{"FLUSHALL"}; r.C.Do(ctx, args...) }`, "FLUSHALL"},
		{"dynamic Do", `func (r Redis) probe(ctx any, command string) { r.C.Do(ctx, command) }`, "Do command unresolved"},
		{"aliased client", `func (r Redis) probe(ctx any) { client := r.C; client.FlushAll(ctx) }`, "FlushAll"},
		{"aliased pipeline", `func (r Redis) probe(ctx any) { p := r.C.TxPipeline(); p.FlushAll(ctx) }`, "FlushAll"},
		{"mutated Do element", `func (r Redis) probe(ctx any) { args := []any{"PING"}; args[0]="FLUSHALL"; r.C.Do(ctx,args...) }`, "Do command unresolved"},
		{"replaced Do slice", `func (r Redis) probe(ctx any) { args := []any{"PING"}; args=[]any{"FLUSHALL"}; r.C.Do(ctx,args...) }`, "Do command unresolved"},
		{"escaped Do slice", `func (r Redis) probe(ctx any) { args := []any{"PING"}; mutate(args); r.C.Do(ctx,args...) }`, "Do command unresolved"},
	} {
		t.Run("reversed: "+row.name, func(t *testing.T) {
			more := cloneSrcs(srcs)
			more["probe.go"] = "package bus\n" + row.source
			assert.Contains(t, strings.Join(aclProblems(line, deriveACLNeeds(t, more), login), "\n"), row.want)
		})
	}
	t.Run("unrelated slice is no command", func(t *testing.T) {
		more := cloneSrcs(srcs)
		more["probe.go"] = `package bus; var unused = []any{"FLUSHALL"}`
		assert.Empty(t, aclProblems(line, deriveACLNeeds(t, more), login))
	})

	t.Run("reversed: a stale classification", func(t *testing.T) {
		more := cloneSrcs(srcs)
		delete(more, "ask.go")
		got := aclProblems(line, deriveACLNeeds(t, more), login)
		require.NotEmpty(t, got)
		assert.Contains(t, strings.Join(got, "\n"), "AskTimeoutPrefix, which pkg/bus no longer has")
	})
}

func cloneSrcs(m map[string]string) map[string]string {
	out := make(map[string]string, len(m)+1)
	for k, v := range m {
		out[k] = v
	}
	return out
}
