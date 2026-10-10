package docs

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// coordinator_tools_test.go holds docs/COORDINATOR-TOOLS.md to the tools it
// names. The page maps each seat-only wrapper, script and loop the
// coordinator runs to the nova verb that does the same, or to a proposed card;
// a stranger setting the sprint up pastes the verb lines, so a verb or a flag
// the tool does not carry is a line that fails the first time it is run. The
// verbs and flags are read from each tool's own source, never copied here:
// nova-sprint's verb table (cmd/nova-sprint/verbs.go) and the flags its
// common.register adds, the usage banner of nova-secrets, nova-swarm and nova-config, and
// the tool.Verb table of nova-friend and nova-bus. A flag counts when the
// verb's synopsis names it and the tool's source registers it.

// coordinatorToolsPath is the page, relative to this package.
const coordinatorToolsPath = "../../docs/COORDINATOR-TOOLS.md"

// novaVerb is one verb of a tool: the flags its synopsis names.
type novaVerb struct{ flags map[string]bool }

// novaTool is what the page's verb lines are checked against: the verbs by
// name (one or two words), the flags every verb takes, and every string
// literal of the tool's source, the flag names its FlagSets register among them.
type novaTool struct {
	verbs    map[string]novaVerb
	common   map[string]bool
	literals map[string]bool
}

// flagNameRe is the shape of a flag's name.
var flagNameRe = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

// synopsisFlagRe reads a flag a synopsis names: `--name`, `--name=value`.
var synopsisFlagRe = regexp.MustCompile(`--([a-z][a-z0-9-]*)`)

func synopsisFlags(syn string) map[string]bool {
	flags := map[string]bool{}
	for _, m := range synopsisFlagRe.FindAllStringSubmatch(syn, -1) {
		flags[m[1]] = true
	}
	return flags
}

// parseToolDir parses the non-test Go files of cmd/<tool>.
func parseToolDir(t *testing.T, tool string) []*ast.File {
	t.Helper()
	dir := filepath.Join("../../cmd", tool)
	names, err := filepath.Glob(filepath.Join(dir, "*.go"))
	require.NoError(t, err)
	fset := token.NewFileSet()
	var files []*ast.File
	for _, name := range names {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, 0)
		require.NoError(t, err, "%s: %v", name, err)
		files = append(files, f)
	}
	require.NotEmpty(t, files, "%s holds no Go source", dir)
	return files
}

// stringLit is the value of a string literal, ok false for anything else.
func stringLit(e ast.Expr) (string, bool) {
	lit, ok := e.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	s, err := strconv.Unquote(lit.Value)
	return s, err == nil
}

// sourceLiterals is every string literal of the files.
func sourceLiterals(files []*ast.File) map[string]bool {
	lits := map[string]bool{}
	for _, f := range files {
		ast.Inspect(f, func(n ast.Node) bool {
			if e, ok := n.(ast.Expr); ok {
				if s, ok := stringLit(e); ok {
					lits[s] = true
				}
			}
			return true
		})
	}
	return lits
}

// sprintTool reads nova-sprint: each element of the verbs table is a literal
// whose first two fields are the verb's name and its synopsis, and the flags
// common.register and common.registerStore add are taken by every verb that
// registers them, which is every verb that opens the store or a server.
func sprintTool(t *testing.T) novaTool {
	t.Helper()
	files := parseToolDir(t, "nova-sprint")
	nt := novaTool{verbs: map[string]novaVerb{}, common: map[string]bool{}, literals: sourceLiterals(files)}
	for _, f := range files {
		ast.Inspect(f, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.CompositeLit:
				arr, ok := n.Type.(*ast.ArrayType)
				if !ok {
					return true
				}
				if id, ok := arr.Elt.(*ast.Ident); !ok || id.Name != "verb" {
					return true
				}
				for _, e := range n.Elts {
					v, ok := e.(*ast.CompositeLit)
					if !ok || len(v.Elts) < 2 {
						continue
					}
					name, ok1 := stringLit(v.Elts[0])
					syn, ok2 := stringLit(v.Elts[1])
					if ok1 && ok2 {
						nt.verbs[name] = novaVerb{flags: synopsisFlags(syn)}
					}
				}
			case *ast.FuncDecl:
				if n.Recv == nil || (n.Name.Name != "register" && n.Name.Name != "registerStore") {
					return true
				}
				ast.Inspect(n.Body, func(m ast.Node) bool {
					call, ok := m.(*ast.CallExpr)
					if !ok {
						return true
					}
					for _, arg := range call.Args {
						if s, ok := stringLit(arg); ok && flagNameRe.MatchString(s) {
							nt.common[s] = true
							break
						}
					}
					return true
				})
				return false
			}
			return true
		})
	}
	require.Contains(t, nt.verbs, "where", "cmd/nova-sprint: the verb table was not read")
	require.Contains(t, nt.common, "redis", "cmd/nova-sprint: common.registerStore's flags were not read")
	return nt
}

// usageTool reads a tool whose usage constant (usage, or nova-config's usageTop)
// carries one synopsis line per verb form, `  <tool> <verb words> <flags>`: the verb is the words before the
// first flag or placeholder.
func usageTool(t *testing.T, tool string) novaTool {
	t.Helper()
	files := parseToolDir(t, tool)
	nt := novaTool{verbs: map[string]novaVerb{}, common: map[string]bool{}, literals: sourceLiterals(files)}
	var usage string
	for _, f := range files {
		for _, decl := range f.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || gen.Tok != token.CONST {
				continue
			}
			for _, spec := range gen.Specs {
				val := spec.(*ast.ValueSpec)
				for i, name := range val.Names {
					if (name.Name == "usage" || name.Name == "usageTop") && i < len(val.Values) {
						usage, _ = stringLit(val.Values[i])
					}
				}
			}
		}
	}
	require.NotEmpty(t, usage, "cmd/%s declares no usage constant; its verb table has moved", tool)
	for _, line := range strings.Split(usage, "\n") {
		rest, ok := strings.CutPrefix(strings.TrimSpace(line), tool+" ")
		if !ok {
			continue
		}
		var words []string
		fields := strings.Fields(rest)
		for _, w := range fields {
			if strings.ContainsAny(w[:1], "-<[(|") {
				break
			}
			words = append(words, w)
		}
		if len(words) == 0 {
			continue
		}
		name := strings.Join(words, " ")
		v, ok := nt.verbs[name]
		if !ok {
			v = novaVerb{flags: map[string]bool{}}
			nt.verbs[name] = v
		}
		for f := range synopsisFlags(strings.Join(fields[len(words):], " ")) {
			v.flags[f] = true
		}
	}
	require.NotEmpty(t, nt.verbs, "cmd/%s: no synopsis line was read from its usage", tool)
	return nt
}

// verbTableTool reads a tool built on pkg/tool: each tool.Verb literal's
// Name and Usage, the Usage one form per line, each line beginning with the
// verb's name.
func verbTableTool(t *testing.T, tool string) novaTool {
	t.Helper()
	files := parseToolDir(t, tool)
	nt := novaTool{verbs: map[string]novaVerb{}, common: map[string]bool{}, literals: sourceLiterals(files)}
	for _, f := range files {
		ast.Inspect(f, func(n ast.Node) bool {
			lit, ok := n.(*ast.CompositeLit)
			if !ok {
				return true
			}
			var name, usage string
			for _, e := range lit.Elts {
				kv, ok := e.(*ast.KeyValueExpr)
				if !ok {
					continue
				}
				key, ok := kv.Key.(*ast.Ident)
				if !ok {
					continue
				}
				switch key.Name {
				case "Name":
					name, _ = stringLit(kv.Value)
				case "Usage":
					usage, _ = stringLit(kv.Value)
				}
			}
			if name != "" && usage != "" {
				nt.verbs[name] = novaVerb{flags: synopsisFlags(usage)}
			}
			return true
		})
	}
	require.NotEmpty(t, nt.verbs, "cmd/%s: no tool.Verb with a Name and a Usage was read", tool)
	return nt
}

// readNovaTools reads every tool the page may name in a verb line. A verb line
// that names another nova tool is refused: its verbs are not read, so the line
// is unchecked, and an unchecked line is what this test exists to prevent.
func readNovaTools(t *testing.T) map[string]novaTool {
	t.Helper()
	return map[string]novaTool{
		"nova-sprint":  sprintTool(t),
		"nova-secrets": usageTool(t, "nova-secrets"),
		"nova-swarm":   usageTool(t, "nova-swarm"),
		"nova-config":  usageTool(t, "nova-config"),
		"nova-friend":  verbTableTool(t, "nova-friend"),
		"nova-bus":     verbTableTool(t, "nova-bus"),
	}
}

// codeSpanRe reads the inline code spans of a table cell.
var codeSpanRe = regexp.MustCompile("`([^`]+)`")

// cardCellRe reads a mapping cell that proposes a card: its id, the verb it
// needs (the one span not checked: it does not exist yet), and its PATHS.
var cardCellRe = regexp.MustCompile("^card: `?([a-z0-9][a-z0-9.-]*)`?; needs `([^`]+)`.*?PATHS: (\\S.*?)(?:;|$)")

// checkCommand checks one command line, the words of a code span: leading
// environment words (`env`, NAME=value) are skipped, a word that is not a nova
// tool ends the check (a placeholder, a program the line runs), and after a bare
// `--` the rest is checked as the command it runs.
func checkCommand(tools map[string]novaTool, words []string) (checked bool, problems []string) {
	for len(words) > 0 && (words[0] == "env" || words[0] == "/usr/bin/env" || (strings.Contains(words[0], "=") && !strings.HasPrefix(words[0], "-"))) {
		words = words[1:]
	}
	if len(words) == 0 {
		return false, nil
	}
	name := path.Base(words[0])
	if !strings.HasPrefix(name, "nova-") {
		return false, nil
	}
	tool, ok := tools[name]
	if !ok {
		return true, []string{name + " is a nova tool this test reads no verb table of: map to a verb of nova-sprint, nova-secrets, nova-swarm, nova-config, nova-friend or nova-bus, or teach the test the tool"}
	}
	words = words[1:]
	var verbName string
	for n := min(2, len(words)); n >= 1; n-- {
		if _, ok := tool.verbs[strings.Join(words[:n], " ")]; ok {
			verbName = strings.Join(words[:n], " ")
			words = words[n:]
			break
		}
	}
	if verbName == "" {
		first := "(nothing)"
		if len(words) > 0 {
			first = words[0]
		}
		return true, []string{name + " has no verb " + strconv.Quote(first) + " in its verb table"}
	}
	verb := tool.verbs[verbName]
	for i, w := range words {
		if w == "--" {
			_, sub := checkCommand(tools, words[i+1:])
			return true, append(problems, sub...)
		}
		flag, ok := strings.CutPrefix(w, "--")
		if !ok {
			continue
		}
		flag, _, _ = strings.Cut(flag, "=")
		switch {
		case !verb.flags[flag] && !tool.common[flag]:
			problems = append(problems, name+" "+verbName+" has no flag --"+flag+" in its synopsis")
		case !tool.literals[flag]:
			problems = append(problems, name+" "+verbName+": --"+flag+" is in the synopsis and no FlagSet of cmd/"+name+" registers it")
		}
	}
	return true, problems
}

// coordinatorToolRow is one row of the page's table.
type coordinatorToolRow struct {
	line              int
	tool, what, verbs string
}

// coordinatorToolRows reads the rows of every table whose header is
// `| Tool | What it does | Replaced by |`.
func coordinatorToolRows(text string) []coordinatorToolRow {
	var rows []coordinatorToolRow
	in := false
	for i, line := range strings.Split(text, "\n") {
		if !strings.HasPrefix(line, "|") {
			in = false
			continue
		}
		cells := strings.Split(strings.Trim(strings.TrimSpace(line), "|"), "|")
		for j := range cells {
			cells[j] = strings.TrimSpace(cells[j])
		}
		if len(cells) == 3 && cells[0] == "Tool" && cells[1] == "What it does" && cells[2] == "Replaced by" {
			in = true
			continue
		}
		if !in || strings.HasPrefix(cells[0], "---") {
			continue
		}
		row := coordinatorToolRow{line: i + 1, tool: cells[0]}
		if len(cells) == 3 {
			row.what, row.verbs = cells[1], cells[2]
		}
		rows = append(rows, row)
	}
	return rows
}

// checkCoordinatorTools is every problem of the page's rows.
func checkCoordinatorTools(tools map[string]novaTool, text string) []string {
	var problems []string
	say := func(r coordinatorToolRow, s string) {
		problems = append(problems, "line "+strconv.Itoa(r.line)+" ("+r.tool+"): "+s)
	}
	rows := coordinatorToolRows(text)
	if len(rows) == 0 {
		return []string{"no row under a | Tool | What it does | Replaced by | header"}
	}
	seen := map[string]bool{}
	for _, r := range rows {
		if !codeSpanRe.MatchString(r.tool) {
			say(r, "the tool cell names no path or unit in a code span")
		}
		if seen[r.tool] {
			say(r, "a second row for the same tool")
		}
		seen[r.tool] = true
		if r.what == "" || r.verbs == "" {
			say(r, "a row wants three cells: the tool, what it does in one line, and the verb or the card")
			continue
		}
		spans := codeSpanRe.FindAllStringSubmatch(r.verbs, -1)
		if strings.HasPrefix(r.verbs, "card:") {
			m := cardCellRe.FindStringSubmatch(r.verbs)
			if m == nil {
				say(r, "a card cell is `card: <id>; needs `<verb line>`; PATHS: <path>,...`")
				continue
			}
			proposed := false
			for _, s := range spans {
				if !proposed && s[1] == m[2] {
					proposed = true
					continue // the proposed verb: the card is what makes it exist
				}
				_, ps := checkCommand(tools, strings.Fields(s[1]))
				for _, p := range ps {
					say(r, p)
				}
			}
			continue
		}
		checked := false
		for _, s := range spans {
			ok, ps := checkCommand(tools, strings.Fields(s[1]))
			checked = checked || ok
			for _, p := range ps {
				say(r, p)
			}
		}
		if !checked {
			say(r, "the replacement names no nova verb line and no card")
		}
	}
	return problems
}

// TestEveryCoordinatorToolMapsToARealNovaVerb is the contract: every row of
// docs/COORDINATOR-TOOLS.md maps its tool to verb lines whose verbs are in the
// tool's verb table and whose flags are in that verb's synopsis and FlagSet,
// or to a card with the verb it needs and its PATHS. The witnesses run the
// same check on rows that must be refused, so a check that passes everything
// fails here.
func TestEveryCoordinatorToolMapsToARealNovaVerb(t *testing.T) {
	t.Parallel()
	tools := readNovaTools(t)

	t.Run("the page", func(t *testing.T) {
		raw, err := os.ReadFile(coordinatorToolsPath)
		require.NoError(t, err, "%s: %v", coordinatorToolsPath, err)
		require.GreaterOrEqual(t, len(coordinatorToolRows(string(raw))), 10, "%s maps fewer tools than the coordinator runs", coordinatorToolsPath)
		for _, p := range checkCoordinatorTools(tools, string(raw)) {
			t.Errorf("%s %s", coordinatorToolsPath, p)
		}
	})

	t.Run("the witnesses", func(t *testing.T) {
		head := "| Tool | What it does | Replaced by |\n|---|---|---|\n"
		good := head + "| `a` | x | `NOVA_SPRINT_SERVER=127.0.0.1:6390 nova-sprint where --watch --every 1s` |\n" +
			"| `b` | x | `nova-secrets exec --store <d> --as <s> --key <k> --sops <p> --only A --require=A -- env A=b nova-sprint friend sync --root <d> --actor <s>` |\n" +
			"| `c` | x | `nova-friend ping --as <c> --to <f>`, `nova-swarm disk-guard --root <d> --disk-floor 200`, `nova-bus recv --as <me> --forever --exec <cmd>` |\n" +
			"| `d` | x | card: friend-ping-every; needs `nova-friend ping --every <d>`; PATHS: cmd/nova-friend/main.go |\n"
		checked, ps := checkCommand(tools, nil)
		assert.False(t, checked)
		assert.Empty(t, ps)
		assert.Empty(t, checkCoordinatorTools(tools, good), "rows that name real verbs and flags are refused")

		for line, want := range map[string]string{
			"| `a` | x | `nova-sprint fly` |":                                                        `nova-sprint has no verb "fly"`,
			"| `a` | x | `nova-sprint where --fly` |":                                                "nova-sprint where has no flag --fly",
			"| `a` | x | `nova-sprint where --check` |":                                              "nova-sprint where has no flag --check",
			"| `a` | x | `nova-secrets exec --fly -- nova-sprint where` |":                           "nova-secrets exec has no flag --fly",
			"| `a` | x | `nova-secrets exec -- nova-sprint fly` |":                                   `nova-sprint has no verb "fly"`,
			"| `a` | x | `nova-teleport go` |":                                                       "nova-teleport is a nova tool this test reads no verb table of",
			"| `a` | x | `ns.sh where` |":                                                            "names no nova verb line and no card",
			"| `a` | x | card: x-1; needs `nova-sprint fly` |":                                       "a card cell is",
			"| `a` | x | card: x-1; needs `nova-sprint fly`; PATHS: a.go; today `nova-sprint fly` |": `nova-sprint has no verb "fly"`,
			"| `a` | x |":                     "a row wants three cells",
			"| a | x | `nova-sprint where` |": "names no path or unit",
		} {
			ps := checkCoordinatorTools(tools, head+line+"\n")
			assert.NotEmpty(t, ps, "the row is not refused: %s", line)
			assert.Contains(t, strings.Join(ps, "\n"), want, "the row is refused for another reason: %s", line)
		}
		assert.Contains(t, strings.Join(checkCoordinatorTools(tools, head+"| `a` | x | `nova-sprint where` |\n| `a` | x | `nova-sprint where` |\n"), "\n"), "a second row")
		assert.NotEmpty(t, checkCoordinatorTools(tools, "no table"))
	})
}
