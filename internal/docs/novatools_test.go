package docs

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// novatools_test.go reads the verbs and flags of the nova tools a guide's
// command lines name, from each tool's own source, never copied here: the usage
// banner of nova-secrets, nova-swarm and nova-config, and the tool.Verb table of
// nova-friend and nova-bus. A flag counts when the verb's synopsis names it.

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

// readNovaTools reads every tool a guide may name in a command line. A line
// that names another nova tool of this tree is refused: its verbs are not read,
// so the line is unchecked, and an unchecked line is what these tests exist to
// prevent.
func readNovaTools(t *testing.T) map[string]novaTool {
	t.Helper()
	return map[string]novaTool{
		"nova-secrets": usageTool(t, "nova-secrets"),
		"nova-swarm":   usageTool(t, "nova-swarm"),
		"nova-config":  usageTool(t, "nova-config"),
		"nova-friend":  verbTableTool(t, "nova-friend"),
		"nova-bus":     verbTableTool(t, "nova-bus"),
	}
}

// externalTools are the nova tools of another repository, by the repository
// whose tests check their command lines. nova-sprint left this tree at v1.2.3
// (the split); a line naming it is checked there, never here.
var externalTools = map[string]string{"nova-sprint": "mas-bandwidth/nova-sprint"}
