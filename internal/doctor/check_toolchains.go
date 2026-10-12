package doctor

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

var requiredToolchains = []string{"node", "dotnet", "dart", "elixir", "otp", "rust", "jdk", "sbcl", "gh"}

const toolchainsProbeTimeout = 3 * time.Second

type toolchainFacts struct {
	Schema     int `json:"schema"`
	Toolchains map[string]struct {
		Path    string `json:"path"`
		Version string `json:"version"`
	} `json:"toolchains"`
}

func init() {
	Default.Register(Check{Name: "toolchains", Dependency: "the language SDKs installed by the toolchains fleet role", Run: checkToolchains})
}

// checkToolchains reads the fleet role's host facts and verifies each recorded path
// is executable (docs/SPEC-DOCTOR.md, "toolchains").
func checkToolchains(ctx context.Context, env Env) Result {
	fix := "ansible-playbook fleet/tools.yml --tags toolchains"
	home := env.Getenv("HOME")
	if home == "" {
		return Result{Status: Fail, Evidence: "missing tool facts: HOME is unset; " + missingToolNames(requiredToolchains), Fix: fix}
	}
	factsPath := filepath.Join(home, ".config", "nova", "toolchains.json")
	raw, err := env.ReadFile(factsPath)
	if err != nil {
		return Result{Status: Fail, Evidence: fmt.Sprintf("toolchain facts %s are unavailable; %s", factsPath, missingToolNames(requiredToolchains)), Fix: fix}
	}
	var facts toolchainFacts
	if err := json.Unmarshal(raw, &facts); err != nil {
		return Result{Status: Fail, Evidence: "toolchain facts are invalid JSON: " + err.Error(), Fix: fix}
	}
	if facts.Schema != 1 {
		return Result{Status: Fail, Evidence: fmt.Sprintf("toolchain facts have schema %d, want 1", facts.Schema), Fix: fix}
	}
	var missing, found []string
	for _, name := range requiredToolchains {
		fact, ok := facts.Toolchains[name]
		if !ok || strings.TrimSpace(fact.Path) == "" || strings.TrimSpace(fact.Version) == "" || !filepath.IsAbs(fact.Path) {
			missing = append(missing, "missing tool="+name)
			continue
		}
		probeCtx, cancel := context.WithTimeout(ctx, toolchainsProbeTimeout)
		_, err := env.Exec(probeCtx, "test", "-x", fact.Path)
		cancel()
		if err != nil {
			missing = append(missing, "missing tool="+name)
			continue
		}
		found = append(found, name+" "+fact.Version)
	}
	if len(missing) > 0 {
		return Result{Status: Fail, Evidence: strings.Join(missing, ", "), Fix: fix}
	}
	return Result{Status: OK, Evidence: "toolchains: " + strings.Join(found, ", ")}
}

// missingToolNames keeps doctor evidence explicit for every absent SDK (docs/SPEC-DOCTOR.md, "toolchains").
func missingToolNames(names []string) string {
	missing := make([]string, 0, len(names))
	for _, name := range names {
		missing = append(missing, "missing tool="+name)
	}
	return strings.Join(missing, ", ")
}
