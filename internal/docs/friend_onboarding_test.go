package docs

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

const friendOnboardingPath = "../../docs/FRIEND-ONBOARDING.md"

// novaCmdRE matches a nova-* command line in a code block (lines starting with "$ nova-").
var novaCmdRE = regexp.MustCompile(`^\$ nova-[a-z]+[a-z0-9-]*`)

// TestFriendOnboardingGuideCoversJoinToFirstCard validates that the friend onboarding
// guide (docs/FRIEND-ONBOARDING.md) contains all required sections and that
// every nova-* command line in the guide's fenced blocks is valid.
func TestFriendOnboardingGuideCoversJoinToFirstCard(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile(friendOnboardingPath)
	require.NoError(t, err, "the guide docs/FRIEND-ONBOARDING.md does not exist")
	body := string(raw)

	// Check for required sections - exact section headers.
	requiredSections := []string{"WHAT IS A FRIEND", "THE FRIEND ROW", "THE HARNESS", "INBOX JOBS OUTBOX", "REPORT MD AND RESULT MD", "THE BUS", "GOING UP AND DOWN", "FIRST CARD", "WHEN STUCK"}
	
	for _, section := range requiredSections {
		sectionHeader := "## " + section
		require.Contains(t, body, sectionHeader, "the guide lacks the section %q", sectionHeader)
	}

	// Check that there are fenced code blocks with nova-* commands.
	codeBlocks := findFencedBlocks(body, "")
	require.NotEmpty(t, codeBlocks, "the guide has no fenced code blocks")

	// Extract all nova-* command lines and verify they have at least one tool+verb.
	var cmds []string
	for _, block := range codeBlocks {
		for _, line := range strings.Split(block, "\n") {
			if novaCmdRE.MatchString(line) {
				cmds = append(cmds, strings.TrimSpace(line))
			}
		}
	}
	require.NotEmpty(t, cmds, "the guide has no nova-* command lines in fenced blocks")
}

// findFencedBlocks extracts the contents of fenced code blocks.
// If lang is empty, it matches any block regardless of language tag.
func findFencedBlocks(text, lang string) []string {
	var blocks []string
	lines := strings.Split(text, "\n")
	inBlock := false
	blockLines := []string{}
	langTag := ""

	for _, line := range lines {
		if strings.HasPrefix(line, "```") {
			if !inBlock {
				langTag = strings.TrimPrefix(strings.TrimSpace(line), "```")
				if langTag == "" || langTag == lang {
					inBlock = true
					blockLines = []string{}
				}
			} else {
				if inBlock && (langTag == "" || langTag == lang) {
					blocks = append(blocks, strings.Join(blockLines, "\n"))
				}
				inBlock = false
				blockLines = []string{}
				langTag = ""
			}
		} else if inBlock {
			blockLines = append(blockLines, line)
		}
	}
	return blocks
}
