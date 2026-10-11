package doctor

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/harness"
)

const harnessProbeTimeout = 10 * time.Second

func init() {
	Default.Register(Check{Name: "harness", Dependency: "the friend harnesses", Run: checkHarness})
}

// checkHarness checks every friend row's harness binary and the config directory
// required by a Claude one-shot row. It only asks binaries for their version.
func checkHarness(ctx context.Context, env Env) Result {
	home := env.Getenv("NOVA_FRIEND_HOME")
	if home == "" {
		home = filepath.Join(env.Getenv("HOME"), ".nova", "friends")
	}
	entries, err := env.ReadDir(home)
	if err != nil {
		return Result{Status: OK, Evidence: "no friend rows at " + home}
	}

	var problems, friends []string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		friendDir := filepath.Join(home, name)
		beat, err := env.ReadFile(filepath.Join(friendDir, "beat"))
		if err != nil {
			problems = append(problems, fmt.Sprintf("friend %s: beat is missing", name))
			continue
		}
		fields := strings.Fields(string(beat))
		harness := field(fields, "row_harness")
		if harness == "" {
			harness = field(fields, "harness")
		}
		if harness == "" {
			problems = append(problems, fmt.Sprintf("friend %s: beat has no harness", name))
			continue
		}
		if !containsHarness(harness) {
			problems = append(problems, fmt.Sprintf("friend %s: %s is not a supported harness", name, harness))
			continue
		}
		binary := harnessOnPath(env, harness)
		if binary == "" {
			problems = append(problems, fmt.Sprintf("friend %s: %s is not on PATH", name, harness))
			continue
		}
		cctx, cancel := context.WithTimeout(ctx, harnessProbeTimeout)
		version, versionErr := env.Exec(cctx, binary, "--version")
		cancel()
		if versionErr != nil || !versionSupported(version) {
			problems = append(problems, fmt.Sprintf("friend %s: %s did not answer a supported version", name, harness))
			continue
		}
		mode := field(fields, "row_mode")
		if mode == "" {
			mode = field(fields, "mode")
		}
		if harness == "claude" && mode == "one-shot" {
			configDir := field(fields, "row_config_dir")
			if configDir == "" {
				configDir = field(fields, "config_dir")
			}
			if configDir == "" {
				problems = append(problems, fmt.Sprintf("friend %s: claude one-shot needs config_dir", name))
				continue
			}
			if _, err := env.ReadDir(configDir); err != nil {
				problems = append(problems, fmt.Sprintf("friend %s: config_dir %s is missing", name, configDir))
				continue
			}
		}
		friends = append(friends, name+"="+harness)
	}

	if len(problems) > 0 {
		return Result{Status: Fail, Evidence: strings.Join(problems, "; "), Fix: "run nova-friend install --as <friend> --harness <h> --dir <friend-dir> and set --config-dir <dir> for Claude one-shot rows"}
	}
	if len(friends) == 0 {
		return Result{Status: OK, Evidence: "no friend rows"}
	}
	return Result{Status: OK, Evidence: "friends: " + strings.Join(friends, ", ")}
}

func containsHarness(name string) bool {
	for _, known := range harness.Kinds {
		if name == known {
			return true
		}
	}
	return false
}

func field(fields []string, key string) string {
	for _, f := range fields {
		if value, ok := strings.CutPrefix(f, key+"="); ok {
			return value
		}
	}
	return ""
}

func harnessOnPath(env Env, name string) string {
	for _, dir := range filepath.SplitList(env.Getenv("PATH")) {
		if dir == "" {
			continue
		}
		entries, err := env.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if entry.Name() != name || entry.IsDir() {
				continue
			}
			info, err := entry.Info()
			if err == nil && info.Mode()&0o111 != 0 {
				return filepath.Join(dir, name)
			}
		}
	}
	return ""
}

func versionSupported(output string) bool {
	for _, word := range strings.Fields(output) {
		if strings.ContainsAny(word, "0123456789") {
			return true
		}
	}
	return false
}
