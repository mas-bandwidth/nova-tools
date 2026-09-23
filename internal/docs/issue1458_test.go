package docs

import (
	"os"
	"strings"
	"testing"
)

// issue1458_test.go holds tools/bench-wsl2.ps1 against the exact text of #1458:
// "WSL2 bench bootstrap in one step: tools/bench-wsl2.ps1 with one approval,
// then the keeper adopts over ssh (Glenn: \"not 20\")."
//
// It reads the script as text and runs nothing; a Windows box is not on this
// host and the script is not executable here.

const issue1458ScriptPath = "../../tools/bench-wsl2.ps1"

func TestIssue1458(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile(issue1458ScriptPath)
	if err != nil {
		t.Fatalf("%s is not in the tree: the WSL2 Windows setup is not documented as the one-step bootstrap #1458 asks for: %v", issue1458ScriptPath, err)
	}
	script := string(raw)

	// The issue title is the contract: one script, one approval, the keeper
	// adopts over ssh, and Glenn's "not 20" is recorded where the script is.
	for _, want := range []string{
		"WSL2 bench bootstrap in one step",
		"tools/bench-wsl2.ps1",
		"one approval",
		"keeper adopts over ssh",
		"Glenn",
		"not 20",
	} {
		if !strings.Contains(script, want) {
			t.Errorf("%s does not carry %q; the script must name the one-step bootstrap #1458 asks for", issue1458ScriptPath, want)
		}
	}
}
