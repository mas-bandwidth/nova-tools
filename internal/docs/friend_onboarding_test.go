package docs

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// friend_onboarding_test.go holds docs/FRIEND-ONBOARDING.md to the tools it
// names. The guide is the standard onboarding path for a new AI friend:
// it must teach them to join, configure their harness, receive a card, run it,
// and write their report. Every nova-* command line in the guide's fenced blocks
// is parsed and checked against the actual verb tables of the tools.

// onboardingPath is the guide, relative to this package.
const onboardingPath = "../../docs/FRIEND-ONBOARDING.md"

// codeBlockRe reads fenced code blocks.
var codeBlockRe = regexp.MustCompile("(?s)```[a-z]*\n(.*?)```")

// novaCmdRe reads nova-* command lines from text.
var novaCmdRe = regexp.MustCompile(`(?:^|\s)(nova-[a-z0-9_-]+)\s+([a-z0-9_\s-]+)`)

// TestFriendOnboardingGuideCoversJoinToFirstCard parses every nova-* command line
// in docs/FRIEND-ONBOARDING.md and checks that each tool, verb and flag exists.
func TestFriendOnboardingGuideCoversJoinToFirstCard(t *testing.T) {
	t.Parallel()

	tools := readNovaTools(t)

	raw, err := os.ReadFile(onboardingPath)
	require.NoError(t, err, "%s: %v", onboardingPath, err)

	blocks := codeBlockRe.FindAllStringSubmatch(string(raw), -1)
	require.NotEmpty(t, blocks, "%s contains no fenced code blocks", onboardingPath)

	var problems []string
	for _, block := range blocks {
		code := block[1]
		for _, line := range strings.Split(code, "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "//") || strings.Contains(line, "<") {
				continue
			}
			matches := novaCmdRe.FindAllStringSubmatch(line, -1)
			for _, m := range matches {
				toolName := m[1]
				verbAndFlags := m[2]

				if _, ok := externalTools[toolName]; ok {
					continue
				}
				tool, ok := tools[toolName]
				if !ok {
					problems = append(problems, toolName+" is a nova tool this test reads no verb table of")
					continue
				}

				words := strings.Fields(verbAndFlags)
				var verbName string
				for n := min(2, len(words)); n >= 1; n-- {
					if _, ok := tool.verbs[strings.Join(words[:n], " ")]; ok {
						verbName = strings.Join(words[:n], " ")
						words = words[n:]
						break
					}
				}
				if verbName == "" && len(words) > 0 {
					problems = append(problems, toolName+" has no verb "+words[0]+" in its verb table")
					continue
				}
				if verbName != "" {
					verb := tool.verbs[verbName]
					for _, w := range words {
						flag, ok := strings.CutPrefix(w, "--")
						if !ok {
							continue
						}
						flag, _, _ = strings.Cut(flag, "=")
						if !verb.flags[flag] && !tool.common[flag] {
							problems = append(problems, toolName+" "+verbName+" has no flag --"+flag+" in its synopsis")
						}
					}
				}
			}
		}
	}

	assert.Empty(t, problems, "%s:\n%s", onboardingPath, strings.Join(problems, "\n"))
}

// TestFriendOnboardingGuideExists ensures the guide exists and has content.
func TestFriendOnboardingGuideExists(t *testing.T) {
	t.Parallel()

	info, err := os.Stat(onboardingPath)
	require.NoError(t, err, "%s does not exist", onboardingPath)
	require.Greater(t, info.Size(), int64(1000), "%s is too short to be useful", onboardingPath)

	raw, err := os.ReadFile(onboardingPath)
	require.NoError(t, err)

	// Check for expected sections
	sections := []string{
		"# Friend Onboarding",
		"What a Friend Is",
		"Joining",
		"Configuration",
		"First Card",
		"Reporting",
	}
	for _, section := range sections {
		assert.Contains(t, string(raw), section, "%s is missing from the guide", section)
	}
}
