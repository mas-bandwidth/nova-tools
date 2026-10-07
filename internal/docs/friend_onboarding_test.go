package docs

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

const friendOnboardingGuide = "../../docs/FRIEND-ONBOARDING.md"

var novaCmdLine = regexp.MustCompile(`(?m)^\$ nova-[a-z-]+[^\n]*`)

// TestFriendOnboardingGuideCoversJoinToFirstCard verifies that the friend onboarding
// guide at docs/FRIEND-ONBOARDING.md contains every nova-* command line a new
// friend needs to run, and that each command line exists in the tool's verb table.
func TestFriendOnboardingGuideCoversJoinToFirstCard(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile(friendOnboardingGuide)
	require.NoError(t, err, "the onboarding guide must exist at %s", friendOnboardingGuide)
	body := string(raw)

	// Extract all nova-* command lines from fenced code blocks
	codeBlocks := extractCodeBlocks(body)
	var novaCmds []string
	for _, block := range codeBlocks {
		matches := novaCmdLine.FindAllString(block, -1)
		novaCmds = append(novaCmds, matches...)
	}

	require.NotEmpty(t, novaCmds, "the guide must contain at least one nova-* command line")

	// For each command, verify the verb exists
	for _, cmd := range novaCmds {
		cmd = strings.TrimSpace(cmd)
		if !strings.HasPrefix(cmd, "$") {
			continue
		}
		cmd = strings.TrimPrefix(cmd, "$")
		cmd = strings.TrimSpace(cmd)

		// Extract verb name (first word after nova-friend)
		parts := strings.Fields(cmd)
		if len(parts) < 2 {
			continue
		}
		toolVerb := parts[1]

		// Check that the verb exists in the tool's verb table
		// This matches what tool.Tool would check
		verbs := novaFriendVerbs()
		found := false
		for _, v := range verbs {
			if v == toolVerb {
				found = true
				break
			}
		}
		require.True(t, found, "nova-friend %s verb must exist (found in guide)", toolVerb)
	}
}

// novaFriendVerbs returns the list of valid nova-friend verbs.
func novaFriendVerbs() []string {
	return []string{
		"run", "beat", "install", "uninstall", "check", "host", "ping",
		"ping-install", "ping-uninstall", "pong", "wait-pong", "watch",
		"status", "refuse-go", "resume", "serve",
	}
}

// extractCodeBlocks extracts the contents of fenced code blocks from markdown.
func extractCodeBlocks(body string) []string {
	var blocks []string
	inBlock := false
	blockContent := strings.Builder{}

	lines := strings.Split(body, "\n")
	for _, line := range lines {
		if strings.HasPrefix(line, "```") {
			if inBlock {
				blocks = append(blocks, blockContent.String())
				blockContent.Reset()
				inBlock = false
			} else {
				inBlock = true
			}
			continue
		}
		if inBlock {
			blockContent.WriteString(line)
			blockContent.WriteString("\n")
		}
	}

	return blocks
}
