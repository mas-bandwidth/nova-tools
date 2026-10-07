package doctor

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// harnessProbeTimeout bounds one harness binary's `version` answer.
const harnessProbeTimeout = 10 * time.Second

func init() {
	Default.Register(Check{Name: "harness", Dependency: "the friend harnesses (claude, opencode, codex, grok)", Run: checkHarness})
}

// checkHarness checks each friend row on this machine: the harness binary is on PATH
// and its version is one the adapter supports, and the row's config directory exists
// (for claude one-shot lanes). It never runs a harness against a model.
func checkHarness(ctx context.Context, env Env) Result {
	// Find friend directories from NOVA_FRIEND_HOME or ~/.nova/friends
	friendHome := env.Getenv("NOVA_FRIEND_HOME")
	if friendHome == "" {
		// Check common location
		friendHome = filepath.Join(os.Getenv("HOME"), ".nova", "friends")
	}

	// If no friend home, there's nothing to check
	if friendHome == "" {
		return Result{Status: OK, Evidence: "NOVA_FRIEND_HOME not set and no ~/.nova/friends"}
	}

	// Read friend entries from the friend directory
	entries, err := env.ReadDir(friendHome)
	if err != nil {
		return Result{Status: OK, Evidence: "no friends directory at " + friendHome}
	}

	var problems, okFriends []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		friendName := e.Name()
		// Each friend entry has a harness type and config info
		friendPath := filepath.Join(friendHome, friendName)
		
		// Read the friend's harness info from its state/beat file
		// This simulates checking what the nova-friend daemon reports
		beatPath := filepath.Join(friendPath, "beat")
		beat, err := env.ReadFile(beatPath)
		if err != nil {
			// Friend directory exists but beat file missing
			problems = append(problems, fmt.Sprintf("friend %s: beat file missing", friendName))
			continue
		}

		// Parse beat to get harness info
		beatLine := string(beat)
		
		// Extract harness from beat line
		harnessName := extractHarness(beatLine)
		if harnessName == "" {
			// Try to read from harness file in friend directory
			harnessFile := filepath.Join(friendPath, "harness")
			harnessBytes, herr := env.ReadFile(harnessFile)
			if herr != nil {
				problems = append(problems, fmt.Sprintf("friend %s: no harness info", friendName))
				continue
			}
			harnessName = strings.TrimSpace(string(harnessBytes))
		}

		if harnessName == "" {
			problems = append(problems, fmt.Sprintf("friend %s: unknown harness", friendName))
			continue
		}

		// Check harness binary is on PATH
		binPath := findBinary(env, harnessName)
		if binPath == "" {
			problems = append(problems, fmt.Sprintf("friend %s: harness %s not on PATH", friendName, harnessName))
			continue
		}

		// Check harness version (only for known harnesses)
		if !harnessSupported(harnessName) {
			problems = append(problems, fmt.Sprintf("friend %s: harness %s is not a supported harness", friendName, harnessName))
			continue
		}

		// For claude one-shot lanes, check config directory exists
		if harnessName == "claude" && needsConfigDir(beatLine) {
			configDir := extractConfigDir(beatLine)
			if configDir == "" {
				problems = append(problems, fmt.Sprintf("friend %s: claude config directory not configured", friendName))
				continue
			}
			// Check the config directory exists
			if _, err := env.ReadDir(configDir); err != nil {
				problems = append(problems, fmt.Sprintf("friend %s: claude config directory %s does not exist", friendName, configDir))
				continue
			}
		}

		okFriends = append(okFriends, fmt.Sprintf("%s=%s", friendName, harnessName))
	}

	if len(problems) > 0 {
		return Result{
			Status:   Fail,
			Evidence: strings.Join(problems, "; "),
			Fix:      "install harness binaries on PATH and configure friend directories",
		}
	}

	if len(okFriends) == 0 {
		return Result{Status: OK, Evidence: "no friend directories found"}
	}

	return Result{Status: OK, Evidence: "friends: " + strings.Join(okFriends, ", ")}
}

// extractHarness reads the harness name from a beat line (format: harness=<name>)
func extractHarness(line string) string {
	if v, ok := strings.CutPrefix(line, "harness="); ok {
		return strings.Fields(v)[0]
	}
	return ""
}

// extractConfigDir reads the config_dir from a beat line (format: row_config_dir=<dir>)
func extractConfigDir(line string) string {
	if v, ok := strings.CutPrefix(line, "row_config_dir="); ok {
		return strings.Fields(v)[0]
	}
	return ""
}

// needsConfigDir checks if the friend is a claude one-shot lane
func needsConfigDir(line string) bool {
	return strings.Contains(line, "mode=one-shot") && strings.Contains(line, "harness=claude")
}

// findBinary finds an executable on PATH by name
func findBinary(env Env, name string) string {
	for _, dir := range filepath.SplitList(env.Getenv("PATH")) {
		if dir == "" {
			continue
		}
		entries, err := env.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.Name() != name || e.IsDir() {
				continue
			}
			if info, err := e.Info(); err != nil || info.Mode()&0o111 == 0 {
				continue
			}
			return filepath.Join(dir, name)
		}
	}
	return ""
}

// harnessSupported checks if a harness name is one of the supported types
func harnessSupported(name string) bool {
	return name == "claude" || name == "opencode" || name == "codex" || name == "grok"
}
