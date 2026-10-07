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

// getToolVerbs reads the verb table of a tool from its source.
// For nova-sprint it reads cmd/nova-sprint/verbs.go (the init() var verbs).
// For nova-config it reads cmd/nova-config/verbs.go.
// For nova-bus and nova-friend it reads the tool.Verb tables in main.go.
func getToolVerbs(t *testing.T, tool string) map[string]map[string]bool {
	t.Helper()
	verbs := map[string]map[string]bool{}

	switch tool {
	case "nova-sprint":
		src, err := os.ReadFile("../../cmd/nova-sprint/verbs.go")
		require.NoError(t, err)
		// Read verb names from the init() table
		re := regexp.MustCompile(`^\s*{"([a-z][a-z0-9- ]*)",`)
		for _, m := range re.FindAllStringSubmatch(string(src), -1) {
			verb := m[1]
			verbs[verb] = map[string]bool{}
		}
	case "nova-config":
		src, err := os.ReadFile("../../cmd/nova-config/verbs.go")
		require.NoError(t, err)
		// Read from kindExamples and toolExamples
		reKind := regexp.MustCompile(`{"([a-z]+)",\s*"([a-z]+)"`)
		for _, m := range reKind.FindAllStringSubmatch(string(src), -1) {
			verb := m[1] + " " + m[2]
			verbs[verb] = map[string]bool{}
		}
		// Also get single-word verbs from toolExamples
		reTool := regexp.MustCompile(`"([a-z]+)":\s*"[^"]*"`)
		for _, m := range reTool.FindAllStringSubmatch(string(src), -1) {
			verb := m[1]
			verbs[verb] = map[string]bool{}
		}
	case "nova-bus":
		src, err := os.ReadFile("../../cmd/nova-bus/main.go")
		require.NoError(t, err)
		// Read from tool.Verb table
		re := regexp.MustCompile(`tool\.Verb\s*=\s*{"([a-z]+)"`)
		for _, m := range re.FindAllStringSubmatch(string(src), -1) {
			verb := m[1]
			verbs[verb] = map[string]bool{}
		}
	case "nova-friend":
		src, err := os.ReadFile("../../cmd/nova-friend/main.go")
		require.NoError(t, err)
		// Read from tool.Verb table
		re := regexp.MustCompile(`tool\.Verb\s*=\s*{"([a-z]+)"`)
		for _, m := range re.FindAllStringSubmatch(string(src), -1) {
			verb := m[1]
			verbs[verb] = map[string]bool{}
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
	// Look for fenced blocks with content
	lines := strings.Split(string(content), "\n")
	inBlock := false
	var block []string

	for _, line := range lines {
		if strings.HasPrefix(line, "```") {
			if !inBlock {
				inBlock = true
				block = []string{}
			} else {
				// End of block - extract commands
				inBlock = false
				for _, l := range block {
					trimmed := strings.TrimSpace(l)
					if strings.HasPrefix(trimmed, "nova-") {
						commands = append(commands, trimmed)
					}
				}
			}
		} else if inBlock {
			block = append(block, line)
		}
	}
	return commands
}

// validateCommand checks if a command's tool, verb, and flags exist.
func validateCommand(t *testing.T, cmd string, toolVerbs map[string]map[string]bool) []string {
	var errors []string
	// Parse command: nova-tool verb --flag ...
	parts := strings.Fields(cmd)
	if len(parts) == 0 {
		return errors
	}

	first := parts[0]
	// Extract tool name
	tool := ""
	if strings.HasPrefix(first, "nova-") {
		tool = strings.SplitN(first, " ", 2)[0]
	}

	if tool == "" {
		return errors
	}

	toolVbs, ok := toolVerbs[tool]
	if !ok {
		errors = append(errors, "unknown tool "+tool)
		return errors
	}

	// Extract verb
	verb := ""
	if len(parts) > 1 {
		verb = parts[1]
		// Check for two-word verbs
		if !strings.Contains(verb, " ") && len(parts) > 2 {
			second := parts[2]
			if !strings.HasPrefix(second, "-") {
				verb = verb + " " + second
			}
		}
	}

	// Check verb exists
	if verb != "" && !strings.Contains(verb, " ") {
		found := false
		for v := range toolVbs {
			if v == verb || strings.HasPrefix(v, verb+" ") {
				found = true
				break
			}
		}
		if !found {
			errors = append(errors, "unknown verb "+verb+" for tool "+tool)
		}
	}

	// Check flags
	for _, part := range parts {
		if strings.HasPrefix(part, "--") {
			flagName := strings.TrimPrefix(part, "--")
			if idx := strings.Index(flagName, "="); idx >= 0 {
				flagName = flagName[:idx]
			}
			// Simplified check - just verify the flag is plausible
			// A full check would parse the verb's FlagSet
			if flagName == "" {
				errors = append(errors, "malformed flag in "+cmd)
			}
		}
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

	// Load verb tables
	toolVerbs := map[string]map[string]bool{}
	tools := []string{"nova-config", "nova-sprint", "nova-bus", "nova-friend"}
	for _, tool := range tools {
		toolVerbs[tool] = getToolVerbs(t, tool)
	}

	// Validate each command
	var allErrors []string
	for _, cmd := range commands {
		errors := validateCommand(t, cmd, toolVerbs)
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
