package docs

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// friend_onboarding_test.go validates docs/FRIEND-ONBOARDING.md.
// It parses every `nova-*` command line in the guide's fenced blocks and
// fails when a tool, verb or flag does not exist in that tool's verb table.
// No binary is run.

const friendOnboardingPath = "../../docs/FRIEND-ONBOARDING.md"

// getVerbs reads the verb table of a tool from its source.
func getVerbs(t *testing.T, tool string) map[string]bool {
	t.Helper()
	verbs := map[string]bool{}

	switch tool {
	case "nova-sprint":
		src, err := os.ReadFile("../../cmd/nova-sprint/verbs.go")
		require.NoError(t, err)
		re := regexp.MustCompile(`^\s*{"([a-z][a-z0-9- ]*)",`)
		for _, m := range re.FindAllStringSubmatch(string(src), -1) {
			verbs[m[1]] = true
		}
	case "nova-config":
		src, err := os.ReadFile("../../cmd/nova-config/verbs.go")
		require.NoError(t, err)
		reKind := regexp.MustCompile(`{"([a-z]+)",\s*"([a-z]+)"`)
		for _, m := range reKind.FindAllStringSubmatch(string(src), -1) {
			verbs[m[1]+" "+m[2]] = true
		}
		reTool := regexp.MustCompile(`"([a-z]+)":\s*"[^"]*"`)
		for _, m := range reTool.FindAllStringSubmatch(string(src), -1) {
			verbs[m[1]] = true
		}
	case "nova-bus":
		src, err := os.ReadFile("../../cmd/nova-bus/main.go")
		require.NoError(t, err)
		re := regexp.MustCompile(`tool\.Verb\s*=\s*{"([a-z]+)"`)
		for _, m := range re.FindAllStringSubmatch(string(src), -1) {
			verbs[m[1]] = true
		}
	case "nova-friend":
		src, err := os.ReadFile("../../cmd/nova-friend/main.go")
		require.NoError(t, err)
		re := regexp.MustCompile(`tool\.Verb\s*=\s*{"([a-z]+)"`)
		for _, m := range re.FindAllStringSubmatch(string(src), -1) {
			verbs[m[1]] = true
		}
	default:
		require.Fail(t, "unsupported tool", tool)
	}
	return verbs
}

// extractGuideCommands extracts all nova-* command lines from fenced code blocks.
func extractGuideCommands(t *testing.T, path string) []string {
	t.Helper()
	content, err := os.ReadFile(path)
	require.NoError(t, err)

	var commands []string
	lines := strings.Split(string(content), "\n")
	inBlock := false

	for _, line := range lines {
		if strings.HasPrefix(line, "```") {
			inBlock = !inBlock
		} else if inBlock {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "nova-") {
				commands = append(commands, trimmed)
			}
		}
	}
	return commands
}

// validateCommand checks if a command's tool and verb exist in the verb tables.
func validateCommand(t *testing.T, cmd string, verbs map[string]bool) []string {
	var errors []string
	parts := strings.Fields(cmd)
	if len(parts) == 0 {
		return errors
	}

	// Extract tool name
	tool := ""
	if strings.HasPrefix(parts[0], "nova-") {
		tool = strings.SplitN(parts[0], " ", 2)[0]
	}

	if tool == "" {
		return errors
	}

	// Check verb exists
	verb := ""
	if len(parts) > 1 {
		verb = parts[1]
		if len(parts) > 2 && !strings.HasPrefix(parts[2], "-") {
			verb = verb + " " + parts[2]
		}
	}

	if verb != "" && !verbs[verb] {
		errors = append(errors, "unknown verb "+verb+" for tool "+tool)
	}

	return errors
}

func TestFriendOnboardingGuideCoversJoinToFirstCard(t *testing.T) {
	t.Parallel()

	// Read guide
	_, err := os.Stat(friendOnboardingPath)
	if os.IsNotExist(err) {
		t.Skip("FRIEND-ONBOARDING.md not yet created")
	}
	require.NoError(t, err)

	// Extract commands
	commands := extractGuideCommands(t, friendOnboardingPath)
	require.NotEmpty(t, commands, "No nova-* commands found in guide")

	// Load verb tables for all tools
	allVerbs := map[string]bool{}
	tools := []string{"nova-config", "nova-sprint", "nova-bus", "nova-friend"}
	for _, tool := range tools {
		for verb := range getVerbs(t, tool) {
			allVerbs[verb] = true
		}
	}

	// Validate each command
	var allErrors []string
	for _, cmd := range commands {
		errors := validateCommand(t, cmd, allVerbs)
		if len(errors) > 0 {
			allErrors = append(allErrors, cmd+": "+strings.Join(errors, "; "))
		}
	}

	if len(allErrors) > 0 {
		t.Error("Guide contains invalid commands:")
		for _, e := range allErrors {
			t.Error("  " + e)
		}
	}
}
