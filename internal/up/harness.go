package up

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// The harness step: checks and optionally sets up friend harness directories.
// This step verifies that harness binaries are available on PATH and that
// friend directories are properly configured (docs/SPEC-UP.md "Steps").
func init() { Register(Step{Name: "harness", Order: 65, Plan: planHarness, Apply: applyHarness}) }

type harnessStep struct {
	Friends []string    // friend directories found
	Missing []string    // harness binaries missing from PATH
	Valid   []string    // properly configured harness
}

func planHarness(e *Env) Finding {
	// Check for NOVA_FRIEND_HOME
	friendHome := os.Getenv("NOVA_FRIEND_HOME")
	if friendHome == "" {
		// Check common location under HOME
		if e.Home != "" {
			friendHome = filepath.Join(e.Home, ".nova", "friends")
		}
	}

	if friendHome == "" {
		return Finding{OK, "NOVA_FRIEND_HOME not set and no ~/.nova/friends"}
	}

	// Check if friend directory exists
	if _, err := os.Stat(friendHome); err != nil {
		return Finding{OK, "no friends directory at " + friendHome}
	}

	// Read friend entries
	entries, err := os.ReadDir(friendHome)
	if err != nil {
		return Finding{OK, "cannot read friends directory"}
	}

	var problems []string
	supported := map[string]bool{"claude": true, "opencode": true, "codex": true, "grok": true}

	for _, ent := range entries {
		if !ent.IsDir() {
			continue
		}
		friendName := ent.Name()
		friendPath := filepath.Join(friendHome, friendName)

		// Check harness file
		harnessFile := filepath.Join(friendPath, "harness")
		harnessBytes, err := os.ReadFile(harnessFile)
		if err != nil {
			problems = append(problems, fmt.Sprintf("friend %s: missing harness file", friendName))
			continue
		}

		harnessName := strings.TrimSpace(string(harnessBytes))
		if !supported[harnessName] {
			problems = append(problems, fmt.Sprintf("friend %s: unknown harness %s", friendName, harnessName))
			continue
		}

		// Check harness binary is on PATH
		if _, err := e.Machine.Exec.LookPath(harnessName); err != nil {
			problems = append(problems, fmt.Sprintf("friend %s: harness %s not found on PATH", friendName, harnessName))
			continue
		}

		// For claude one-shot mode, check config_dir
		beatFile := filepath.Join(friendPath, "beat")
		if beatBytes, err := os.ReadFile(beatFile); err == nil {
			beatLine := string(beatBytes)
			if strings.Contains(beatLine, "mode=one-shot") && strings.Contains(beatLine, "harness=claude") {
				// Check for config_dir in beat line
				if !strings.Contains(beatLine, "row_config_dir=") {
					problems = append(problems, fmt.Sprintf("friend %s: claude one-shot mode requires config_dir", friendName))
					continue
				}
			}
		}
	}

	if len(problems) > 0 {
		return Finding{Missing, strings.Join(problems, "; ") + "; install harnesses via nova-friend install"}
	}

	if len(entries) == 0 {
		return Finding{OK, "no friend directories found"}
	}

	return Finding{OK, fmt.Sprintf("%d friend(s) configured", len(entries))}
}

func applyHarness(e *Env) error {
	// This step is informational only - it doesn't create harness directories
	// Friend directories are set up via nova-friend install command
	return nil
}
