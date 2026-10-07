package docs

import (
	"os"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// friend_onboarding_test.go validates docs/FRIEND-ONBOARDING.md against each
// tool's own verb and flag tables. No binary is run.

const friendOnboardingPath = "../../docs/FRIEND-ONBOARDING.md"

// onboardingConfigTool models nova-config's generated friend verbs from the
// config descriptor and its explicit command FlagSets.
func onboardingConfigTool(t *testing.T) novaTool {
	t.Helper()
	files := parseToolDir(t, "nova-config")
	kind, ok := config.Lookup(config.KindFriend)
	require.True(t, ok, "internal/config has no friend descriptor")

	fieldFlags := map[string]bool{}
	fieldLiterals := map[string]bool{}
	for _, field := range kind.Fields {
		fieldFlags[field.Name] = true
		fieldLiterals[field.Name] = true
	}
	base := map[string]bool{"pg": true, "file": true, "seat": true, "as": true, "json": true}
	write := cloneFlags(base)
	write["reason"] = true
	write["dry-run"] = true
	for name := range fieldFlags {
		write[name] = true
	}

	verbs := map[string]novaVerb{
		"migrate":        {flags: flags("pg", "file", "print", "dry-run", "json")},
		"apply":          {flags: flags("pg", "file", "seat", "redis", "as", "kind", "check", "dry-run", "move-seat", "json")},
		"friend add":     {flags: write},
		"friend set":     {flags: write},
		"friend remove":  {flags: cloneFlags(write)},
		"friend list":    {flags: cloneFlags(base)},
		"friend show":    {flags: cloneFlags(base)},
		"friend history": {flags: cloneFlags(base)},
	}
	for name := range verbs["friend remove"].flags {
		if fieldFlags[name] {
			delete(verbs["friend remove"].flags, name)
		}
	}
	for name := range verbs["friend list"].flags {
		if name == "as" || name == "seat" {
			delete(verbs["friend list"].flags, name)
		}
	}
	for name := range verbs["friend show"].flags {
		if name == "as" || name == "seat" {
			delete(verbs["friend show"].flags, name)
		}
	}
	for name := range verbs["friend history"].flags {
		if name == "as" || name == "seat" {
			delete(verbs["friend history"].flags, name)
		}
	}

	literals := sourceLiterals(files)
	for name := range fieldLiterals {
		literals[name] = true
	}
	return novaTool{verbs: verbs, literals: literals}
}

func flags(names ...string) map[string]bool {
	set := make(map[string]bool, len(names))
	for _, name := range names {
		set[name] = true
	}
	return set
}

func cloneFlags(src map[string]bool) map[string]bool {
	dst := make(map[string]bool, len(src))
	for name := range src {
		dst[name] = true
	}
	return dst
}

// extractGuideCommands extracts every nova-* command line from fenced blocks.
func extractGuideCommands(t *testing.T, path string) []string {
	t.Helper()
	content, err := os.ReadFile(path)
	require.NoError(t, err)

	var commands []string
	inBlock := false
	for _, line := range strings.Split(string(content), "\n") {
		if strings.HasPrefix(line, "```") {
			inBlock = !inBlock
			continue
		}
		if inBlock {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "nova-") {
				commands = append(commands, trimmed)
			}
		}
	}
	return commands
}

func onboardingTools(t *testing.T) map[string]novaTool {
	t.Helper()
	tools := readNovaTools(t)
	tools["nova-config"] = onboardingConfigTool(t)
	return tools
}

func TestFriendOnboardingGuideCoversJoinToFirstCard(t *testing.T) {
	t.Parallel()
	content, err := os.ReadFile(friendOnboardingPath)
	require.NoError(t, err, "%s: %v", friendOnboardingPath, err)
	commands := extractGuideCommands(t, friendOnboardingPath)
	require.NotEmpty(t, commands, "No nova-* commands found in guide")

	tools := onboardingTools(t)
	var problems []string
	for _, command := range commands {
		checked, errs := checkCommand(tools, strings.Fields(command))
		if !checked {
			problems = append(problems, command+": not a parsed nova command")
		}
		for _, problem := range errs {
			problems = append(problems, command+": "+problem)
		}
	}
	for _, problem := range problems {
		t.Error(problem)
	}

	assert.Contains(t, string(content), "NOVA_SPRINT_REDIS=mem:", "the sprint twin is NOVA_SPRINT_REDIS=mem:<file>")
	assert.NotContains(t, string(content), "NOVA_SPRINT_TWIN", "nova-sprint does not read NOVA_SPRINT_TWIN")
	assert.Contains(t, string(content), "nova-friend ping --as", "ping requires the coordinator identity")

	t.Run("negative command witnesses", func(t *testing.T) {
		t.Parallel()
		cases := []struct {
			name, command, want string
		}{
			{"unknown tool", "nova-unknown run", "is a nova tool this test reads no verb table"},
			{"wrong tool verb", "nova-bus status", "has no verb"},
			{"unknown flag", "nova-bus names --invented", "has no flag --invented"},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()
				checked, errs := checkCommand(tools, strings.Fields(tc.command))
				assert.True(t, checked)
				assert.Contains(t, strings.Join(errs, "\n"), tc.want)
			})
		}
	})
}
