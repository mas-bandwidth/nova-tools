package docs

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// friend_onboarding_test.go: the red test for docs/FRIEND-ONBOARDING.md.
// It parses every `$ nova-*` command line in the guide's fenced code blocks
// and fails when a tool, verb, or flag does not exist in that tool's verb table
// and FlagSet (no binary is run). The test mirrors the pattern of
// cli_links_flags_test.go, scanning main.go files for verb definitions and
// flag registrations.

func TestFriendOnboardingGuideCoversJoinToFirstCard(t *testing.T) {
	t.Parallel()

	// Read the guide
	guidePath := "../../docs/FRIEND-ONBOARDING.md"
	src, err := os.ReadFile(guidePath)
	require.NoError(t, err, "docs/FRIEND-ONBOARDING.md: %v", err)
	guide := string(src)

	// Extract all `$ nova-*` command lines from fenced code blocks
	cmds := extractNovaCommands(guide)
	require.NotZero(t, len(cmds), "docs/FRIEND-ONBOARDING.md: no `$ nova-*` command lines found in fenced blocks")

	// Load verb tables and flag sets for each tool
	toolData := loadToolData(t)

	// Check each command against the tool data
	var failures []string
	for _, cmd := range cmds {
		if err := checkCommand(cmd, toolData); err != nil {
			failures = append(failures, err.Error())
		}
	}

	if len(failures) > 0 {
		t.Errorf("docs/FRIEND-ONBOARDING.md: %d command(s) reference non-existent tools, verbs, or flags:\n%s",
			len(failures), strings.Join(failures, "\n"))
	}
}

// extractNovaCommands finds all `$ nova-*` lines in fenced code blocks.
func extractNovaCommands(guide string) []string {
	var cmds []string
	lines := strings.Split(guide, "\n")
	inBlock := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			inBlock = !inBlock
			continue
		}
		if inBlock && strings.HasPrefix(trimmed, "$ nova-") {
			cmds = append(cmds, trimmed)
		}
	}
	return cmds
}

// toolData holds a tool's verbs and their flag sets.
type toolData struct {
	verbs map[string][]string // verb name -> list of flag names
}

// loadToolData scans main.go files for verb and flag definitions.
func loadToolData(t *testing.T) map[string]toolData {
	// List of tools to check (from PATHS in BRIEF.md)
	tools := []string{"nova-bus", "nova-friend", "nova-config", "nova-check"}
	data := make(map[string]toolData)

	for _, tool := range tools {
		toolPath := "../../cmd/" + tool + "/main.go"
		src, err := os.ReadFile(toolPath)
		if err != nil {
			t.Logf("Warning: could not read %s: %v", toolPath, err)
			continue
		}

		srcStr := string(src)
		// Extract verbs and their flags from the tool.Verb structures
		verbs := extractVerbsAndFlags(srcStr)
		data[tool] = toolData{verbs: verbs}
	}

	return data
}

// extractVerbsAndFlags extracts all verbs and their flag names from a main.go.
func extractVerbsAndFlags(src string) map[string][]string {
	verbs := make(map[string][]string)
	lines := strings.Split(src, "\n")
	var currentVerb string

	// Regex to match flag registration patterns
	flagRe := regexp.MustCompile(`f\.(String|Bool|Int|Duration|Required|Var)\s*\(\s*"?([^\s",]+)"`)

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		// Check for verb name (in Verbs slice context)
		if strings.HasPrefix(trimmed, "Name:") {
			matches := regexp.MustCompile(`Name:\s*"([^\"]+)"`).FindStringSubmatch(line)
			if len(matches) > 1 {
				currentVerb = matches[1]
				verbs[currentVerb] = []string{}
			}
		}

		// Extract flags for current verb
		if currentVerb != "" {
			matches := flagRe.FindAllStringSubmatch(line, -1)
			for _, m := range matches {
				if len(m) > 2 {
					verbs[currentVerb] = append(verbs[currentVerb], m[2])
				}
			}
		}

		// Check if we're exiting the verb block (after Run: line)
		if currentVerb != "" && strings.HasPrefix(trimmed, "Run:") {
			// End of current verb definition
			currentVerb = ""
		}
	}

	return verbs
}

// checkCommand validates a command against the tool data.
func checkCommand(cmd string, data map[string]toolData) error {
	// Remove leading $ if present
	line := strings.TrimPrefix(strings.TrimSpace(cmd), "$")

	// Split into tool and rest
	parts := strings.Fields(line)
	if len(parts) == 0 {
		return nil
	}

	toolName := parts[0]

	// Check if tool exists
	tool, ok := data[toolName]
	if !ok {
		return fmt.Errorf("%s: unknown tool", toolName)
	}

	if len(parts) < 2 {
		return nil // Just tool name, no verb
	}

	verbName := parts[1]

	// Check if verb exists in tool
	verbFlags, ok := tool.verbs[verbName]
	if !ok {
		// List available verbs
		var available []string
		for v := range tool.verbs {
			available = append(available, v)
		}
		return fmt.Errorf("%s %s: unknown verb; available verbs: %v", toolName, verbName, available)
	}

	// Check flags from command
	cmdFlags := extractFlagsFromArgs(parts[1:])
	for _, f := range cmdFlags {
		found := false
		fName := strings.TrimPrefix(f, "--")
		for _, vf := range verbFlags {
			if strings.TrimPrefix(vf, "--") == fName {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("%s %s: unknown flag %s", toolName, verbName, f)
		}
	}

	return nil
}

// extractFlagsFromArgs extracts --flag arguments from command args.
func extractFlagsFromArgs(args []string) []string {
	var flags []string
	for _, a := range args {
		if strings.HasPrefix(a, "--") {
			flags = append(flags, a)
		}
	}
	return flags
}
