package docs

import (
	"go/ast"
	"go/token"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const friendOnboardingPath = "../../docs/FRIEND-ONBOARDING.md"

type guideCommand struct {
	line int
	text string
}

func friendOnboardingCommands(t *testing.T) []guideCommand {
	t.Helper()
	data, err := os.ReadFile(friendOnboardingPath)
	require.NoError(t, err, "%s: %v", friendOnboardingPath, err)

	var out []guideCommand
	inBlock := false
	for i, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "```") {
			inBlock = !inBlock
			continue
		}
		if !inBlock {
			continue
		}
		if strings.HasPrefix(line, "nova-") {
			out = append(out, guideCommand{line: i + 1, text: line})
		}
	}
	return out
}

func TestFriendOnboardingStepsHaveCommandsAndChecks(t *testing.T) {
	t.Parallel()
	data, err := os.ReadFile(friendOnboardingPath)
	require.NoError(t, err, "%s: %v", friendOnboardingPath, err)
	text := string(data)
	starts := regexp.MustCompile(`(?m)^[ \t]*[0-9]+\. \*\*`).FindAllStringIndex(text, -1)
	require.GreaterOrEqual(t, len(starts), 10, "the guide needs a numbered path from setup through recovery")

	for i, start := range starts {
		end := len(text)
		if i+1 < len(starts) {
			end = starts[i+1][0]
		}
		step := text[start[0]:end]
		assert.Contains(t, step, "**Worked:**", "numbered step %d has no observable success check", i+1)
		commands := false
		inBlock := false
		for _, line := range strings.Split(step, "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "```") {
				inBlock = !inBlock
				continue
			}
			commands = commands || (inBlock && strings.HasPrefix(line, "nova-"))
		}
		assert.True(t, commands, "numbered step %d has no exact nova-* command in a fenced block", i+1)
	}
}

func friendOnboardingTools(t *testing.T) map[string]novaTool {
	t.Helper()
	tools := readNovaTools(t)

	files := parseToolDir(t, "nova-config")
	cfg := novaTool{verbs: map[string]novaVerb{}, common: map[string]bool{}, literals: sourceLiterals(files)}
	var usage string
	for _, file := range files {
		for _, decl := range file.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || gen.Tok != token.CONST {
				continue
			}
			for _, spec := range gen.Specs {
				values := spec.(*ast.ValueSpec)
				for i, name := range values.Names {
					if name.Name == "usageTop" && i < len(values.Values) {
						usage, _ = stringLit(values.Values[i])
					}
				}
			}
		}
	}
	require.NotEmpty(t, usage, "cmd/nova-config/main.go has no usageTop banner")
	for _, line := range strings.Split(usage, "\n") {
		rest, ok := strings.CutPrefix(strings.TrimSpace(line), "nova-config ")
		if !ok {
			continue
		}
		fields := strings.Fields(rest)
		var words []string
		for _, word := range fields {
			if strings.ContainsAny(word[:1], "-<[(|") {
				break
			}
			words = append(words, word)
		}
		if len(words) > 0 {
			cfg.verbs[strings.Join(words, " ")] = novaVerb{flags: synopsisFlags(strings.Join(fields[len(words):], " "))}
		}
	}
	for _, name := range []string{"kind", "check", "move-seat"} {
		if cfg.literals[name] {
			cfg.verbs["apply"].flags[name] = true // runApply registers its complete FlagSet in apply.go
		}
	}
	for _, kind := range config.Kinds {
		verbs := []string{"add", "set", "remove", "list", "show", "history"}
		if kind.Singleton {
			verbs = []string{"set", "show", "history"}
		}
		if kind.Name == config.KindTier {
			verbs = []string{"set", "list", "show", "history"}
		}
		for _, verb := range verbs {
			flags := map[string]bool{"pg": true, "file": true, "seat": true, "json": true}
			switch verb {
			case "add", "set":
				for _, name := range []string{"actor", "as", "reason", "dry-run"} {
					flags[name] = true
				}
			case "remove":
				for _, name := range []string{"actor", "as", "reason", "dry-run"} {
					flags[name] = true
				}
			}
			if verb == "add" || verb == "set" {
				for _, field := range kind.Fields {
					if verb == "set" && field.Name == "seat" {
						continue
					}
					cfg.literals[field.Name] = true // runKindWrite registers descriptor fields on this FlagSet
					flags[field.Name] = true
				}
			}
			if kind.Name == config.KindMachine && (verb == "list" || verb == "show") {
				flags["redis"] = true // live machine facts are the only kind read from Redis
			}
			cfg.verbs[kind.Name+" "+verb] = novaVerb{flags: flags}
		}
	}
	tools["nova-config"] = cfg
	return tools
}

func TestFriendOnboardingGuideCoversJoinToFirstCard(t *testing.T) {
	t.Parallel()
	commands := friendOnboardingCommands(t)
	require.NotEmpty(t, commands, "%s has no nova-* command lines in fenced blocks", friendOnboardingPath)
	tools := friendOnboardingTools(t)

	var problems []string
	for _, command := range commands {
		_, found := checkCommand(tools, strings.Fields(command.text))
		for _, problem := range found {
			problems = append(problems, friendOnboardingPath+":"+strconv.Itoa(command.line)+": "+command.text+": "+problem)
		}
	}
	assert.Empty(t, problems, "every command in the guide must name a real tool, verb and registered flag")
}

func TestFriendOnboardingCommandCheckRejectsUnknownCommands(t *testing.T) {
	t.Parallel()
	tools := friendOnboardingTools(t)
	for _, tc := range []struct {
		line string
		want string
	}{
		{"nova-not-a-tool go", "is a nova tool this test reads no verb table of"},
		{"nova-friend nonexistent --as <friend>", `has no verb "nonexistent"`},
		{"nova-bus send --to <friend> --subject hello --body hi --message hi", "has no flag --message"},
		{"nova-config friend add <friend> --bogus x", "has no flag --bogus"},
	} {
		t.Run(tc.line, func(t *testing.T) {
			t.Parallel()
			_, problems := checkCommand(tools, strings.Fields(tc.line))
			assert.Contains(t, strings.Join(problems, "\n"), tc.want)
		})
	}
}
