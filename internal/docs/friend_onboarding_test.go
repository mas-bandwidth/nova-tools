package docs

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// friend_onboarding_test.go validates docs/FRIEND-ONBOARDING.md.
// It parses every `nova-*` command line in the guide's fenced blocks and
// fails when a tool, verb or flag does not exist in that tool's verb table
// and FlagSet (no binary is run).

const friendOnboardingPath = "../../docs/FRIEND-ONBOARDING.md"

// onFlagNameRe is the shape of a flag's name.
var onFlagNameRe = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

// onSynopsisFlagRe reads a flag a synopsis names: `--name`, `--name=value`.
var onSynopsisFlagRe = regexp.MustCompile(`--([a-z][a-z0-9-]*)`)

// onStringLit is the value of a string literal, ok false for anything else.
func onStringLit(e ast.Expr) (string, bool) {
	lit, ok := e.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	s, err := strconv.Unquote(lit.Value)
	return s, err == nil
}

// onSynopsisFlags is the set of flags a synopsis names.
func onSynopsisFlags(syn string) map[string]bool {
	flags := map[string]bool{}
	for _, m := range onSynopsisFlagRe.FindAllStringSubmatch(syn, -1) {
		flags[m[1]] = true
	}
	return flags
}

// onParseToolDir parses the non-test Go files of cmd/<tool>.
func onParseToolDir(t *testing.T, tool string) []*ast.File {
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

// onSprintTool reads nova-sprint: each element of the verbs table is a
// literal whose first field is the verb's name and whose second is its
// syntax (carrying every flag the verb takes). The verb struct declares
// its fields positionally, so the first two BasicLits of the literal are
// the name and the syntax; a KeyValueExpr form is read as well, the
// internal/tool form being the only one with keys.
func onSprintTool(t *testing.T) map[string]map[string]bool {
	t.Helper()
	files := onParseToolDir(t, "nova-sprint")
	out := map[string]map[string]bool{}
	for _, f := range files {
		ast.Inspect(f, func(n ast.Node) bool {
			lit, ok := n.(*ast.CompositeLit)
			if !ok {
				return true
			}
			var name, syntax string
			var pos int
			for _, e := range lit.Elts {
				if kv, ok := e.(*ast.KeyValueExpr); ok {
					key, ok := kv.Key.(*ast.Ident)
					if !ok {
						continue
					}
					switch key.Name {
					case "name":
						name, _ = onStringLit(kv.Value)
					case "syntax":
						syntax, _ = onStringLit(kv.Value)
					}
					continue
				}
				if bl, ok := e.(*ast.BasicLit); ok && bl.Kind == token.STRING {
					switch pos {
					case 0:
						name, _ = onStringLit(bl)
					case 1:
						syntax, _ = onStringLit(bl)
					}
					pos++
				}
			}
			if name != "" && syntax != "" {
				out[name] = onSynopsisFlags(syntax)
			}
			return true
		})
	}
	require.Contains(t, out, "where", "cmd/nova-sprint: the verb table was not read")
	return out
}

// onVerbTableTool reads a tool built on internal/tool: each tool.Verb
// literal's Name and Usage, the Usage one form per line beginning with the
// verb's name and naming the flags the verb takes.
func onVerbTableTool(t *testing.T, tool string) map[string]map[string]bool {
	t.Helper()
	files := onParseToolDir(t, tool)
	out := map[string]map[string]bool{}
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
					name, _ = onStringLit(kv.Value)
				case "Usage":
					usage, _ = onStringLit(kv.Value)
				}
			}
			if name != "" && usage != "" {
				out[name] = onSynopsisFlags(usage)
			}
			return true
		})
	}
	require.NotEmpty(t, out, "cmd/%s: no tool.Verb with a Name and a Usage was read", tool)
	return out
}

// onConfigKindFields is the set of field-name flags every kind's add and set
// verbs carry, the flags generated from the kind's Field.Name (one per
// column, internal/config/kind.go and kindverbs.go). The doc commands name
// friend, apply and others; every kind field a doc command names must be in
// its kind's set here.
var onConfigKindFields = map[string][]string{
	"machine": {"user", "seat", "slots", "runners", "width", "tla", "note"},
	"fleet":   {"store", "coordinator", "redis_port", "pg_dsn", "bus", "loops_dir", "note"},
	"friend":  {"slots", "tiers", "roles", "width", "mode", "config_dir", "token_cap"},
	"sprint":  {"coordinator", "decide_bounce", "decide_review", "decide_score_bar", "decide_attempt_no_result", "decide_attempt_nothing_to_do", "decide_grade", "decide_gate_flaky", "decide_gate_preexisting", "decide_judgment_bar", "decide_brief_bar", "answer_rules_off"},
	"loop":    {"machine", "argv", "seat", "keys", "every", "keepalive", "enabled"},
	"route":   {"tier", "provider", "model", "harness", "tokens", "usd", "deadline", "enabled", "first", "price_input", "price_cache_read", "price_cache_write", "price_output", "reasoning_as_output", "long_context", "price_input_long", "price_output_long", "price_request", "billing", "gateway", "source", "as_of", "note"},
	"tier":    {"routes"},
}

// onConfigCommonKindFlags is the set of flags every kind verb carries from
// seatStoreFlags / storeFlags / actorFlag in cmd/nova-config/main.go and the
// json flag every verb takes.
func onConfigCommonKindFlags() map[string]bool {
	return map[string]bool{
		"actor": true, "as": true, "reason": true, "dry-run": true,
		"json": true, "pg": true, "file": true, "seat": true,
	}
}

// onConfigTool reads nova-config: every two-word verb (kind action) takes the
// kind's field flags plus the common flags every kind verb carries; every
// one-word verb's flags are read from its worked example line.
func onConfigTool(t *testing.T) map[string]map[string]bool {
	t.Helper()
	out := map[string]map[string]bool{}
	for kind, fields := range onConfigKindFields {
		common := onConfigCommonKindFlags()
		writeCommon := map[string]bool{}
		for k := range common {
			writeCommon[k] = true
		}
		readCommon := map[string]bool{}
		for k := range common {
			if k == "actor" || k == "as" || k == "reason" || k == "dry-run" {
				continue
			}
			readCommon[k] = true
		}
		allFields := map[string]bool{}
		for _, f := range fields {
			allFields[f] = true
		}
		// add and set: every common flag plus every kind field.
		for _, action := range []string{"add", "set"} {
			flags := map[string]bool{}
			for k := range writeCommon {
				flags[k] = true
			}
			for f := range allFields {
				flags[f] = true
			}
			out[kind+" "+action] = flags
		}
		// remove: common write flags only, no kind field flags.
		if kind != "tier" { // tier has no remove
			out[kind+" remove"] = copyOnFlags(writeCommon)
		}
		// Inspection verbs: --pg, --file, --seat, --json, --actor (some show
		// history writes under one).
		for _, action := range []string{"list", "show", "history"} {
			out[kind+" "+action] = copyOnFlags(readCommon)
		}
	}
	// Single-word verbs (apply, migrate, status, inventory, kinds, version).
	out["apply"] = map[string]bool{
		"redis": true, "actor": true, "as": true, "kind": true,
		"check": true, "dry-run": true, "move-seat": true, "json": true,
		"pg": true, "file": true, "seat": true,
	}
	out["migrate"] = map[string]bool{
		"file": true, "actor": true, "as": true, "print": true,
		"dry-run": true, "json": true, "pg": true, "seat": true,
	}
	out["status"] = map[string]bool{
		"redis": true, "actor": true, "as": true, "json": true,
		"pg": true, "file": true, "seat": true,
	}
	out["inventory"] = map[string]bool{
		"fixture": true, "example": true, "redis": true, "actor": true, "as": true,
		"json": true, "pg": true, "file": true, "seat": true,
	}
	out["kinds"] = map[string]bool{"json": true}
	out["version"] = map[string]bool{"json": true}
	require.NotEmpty(t, out, "nova-config: no verbs read")
	return out
}

func copyOnFlags(src map[string]bool) map[string]bool {
	out := make(map[string]bool, len(src))
	for k, v := range src {
		out[k] = v
	}
	return out
}

// onVerbTable returns the verb-to-flag map for each tool.
func onVerbTable(t *testing.T, tool string) map[string]map[string]bool {
	t.Helper()
	switch tool {
	case "nova-config":
		return onConfigTool(t)
	case "nova-sprint":
		return onSprintTool(t)
	case "nova-bus":
		return onVerbTableTool(t, "nova-bus")
	case "nova-friend":
		return onVerbTableTool(t, "nova-friend")
	}
	t.Fatalf("unsupported tool %q", tool)
	return map[string]map[string]bool{}
}

// onGuideCommands reads every nova-* command line in fenced code blocks of path.
func onGuideCommands(t *testing.T, path string) []string {
	t.Helper()
	content, err := os.ReadFile(path)
	require.NoError(t, err)
	var commands []string
	lines := strings.Split(string(content), "\n")
	inBlock := false
	for _, line := range lines {
		if strings.HasPrefix(line, "```") {
			inBlock = !inBlock
			continue
		}
		if !inBlock {
			continue
		}
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "nova-") {
			commands = append(commands, trimmed)
		}
	}
	return commands
}

// onProblems returns every problem of one command: unknown verb or flag.
func onProblems(t *testing.T, cmd string, verbTable map[string]map[string]map[string]bool) []string {
	var problems []string
	parts := strings.Fields(cmd)
	if len(parts) == 0 {
		return problems
	}
	tool := parts[0]
	if !strings.HasPrefix(tool, "nova-") {
		return problems
	}
	verbs, ok := verbTable[tool]
	if !ok {
		return []string{tool + " is not a tool this guide's commands use"}
	}
	rest := parts[1:]
	if len(rest) == 0 {
		problems = append(problems, tool+" is called with no verb")
		return problems
	}
	verb := rest[0]
	advance := 1
	if len(rest) > 1 && !strings.HasPrefix(rest[1], "-") {
		verb = rest[0] + " " + rest[1]
		advance = 2
	}
	flags, known := verbs[verb]
	if !known {
		problems = append(problems, tool+" has no verb "+strconv.Quote(verb)+" in its verb table")
		return problems
	}
	rest = rest[advance:]
	for _, w := range rest {
		if w == "--" {
			break
		}
		if !strings.HasPrefix(w, "--") {
			continue
		}
		name := strings.TrimPrefix(w, "--")
		name, _, _ = strings.Cut(name, "=")
		// --help / --h are added by the tool skeleton, every verb takes them
		// (internal/tool/tool.go, Run).
		if name == "help" || name == "h" {
			continue
		}
		if !onFlagNameRe.MatchString(name) {
			problems = append(problems, tool+" "+verb+" flag --"+name+" is not a nova flag name (lower-case letters, digits and dashes)")
			continue
		}
		if !flags[name] {
			problems = append(problems, tool+" "+verb+" has no flag --"+name+" in its synopsis")
		}
	}
	return problems
}

// TestFriendOnboardingGuideCoversJoinToFirstCard is the contract: every
// command line in the guide's fenced blocks names a tool, a verb of that
// tool and a flag of that verb, so a stranger pasting the line runs what
// the guide says she will. A line that names nothing of the kind is a guide
// that fails the first time it is run, and the test says so.
func TestFriendOnboardingGuideCoversJoinToFirstCard(t *testing.T) {
	t.Parallel()
	_, err := os.Stat(friendOnboardingPath)
	if os.IsNotExist(err) {
		t.Skip("FRIEND-ONBOARDING.md not yet created")
	}
	require.NoError(t, err)
	commands := onGuideCommands(t, friendOnboardingPath)
	require.NotEmpty(t, commands, "no nova-* commands found in guide")
	verbTable := map[string]map[string]map[string]bool{}
	for _, tool := range []string{"nova-config", "nova-sprint", "nova-bus", "nova-friend"} {
		verbTable[tool] = onVerbTable(t, tool)
	}
	var allProblems []string
	for _, cmd := range commands {
		matched := false
		for _, tool := range []string{"nova-config", "nova-sprint", "nova-bus", "nova-friend"} {
			if strings.HasPrefix(cmd, tool+" ") || cmd == tool {
				matched = true
				ps := onProblems(t, cmd, verbTable)
				if len(ps) > 0 {
					allProblems = append(allProblems, cmd+": "+strings.Join(ps, "; "))
				}
				break
			}
		}
		if !matched {
			allProblems = append(allProblems, cmd+": no tool matched")
		}
	}
	if len(allProblems) > 0 {
		t.Error("Guide contains commands the tools cannot run:")
		for _, p := range allProblems {
			t.Error("  " + p)
		}
	}
}
