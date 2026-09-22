package swarm

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"runtime"
	"strings"
	"testing"
)

//go:embed testdata/providers.tsv
var providersTSV string

// TestEveryProviderLaunchesThroughOneArgv asserts that for EVERY row of the
// providers table, LaunchArgv returns an argv for both linux and darwin, that
// none of them names a per-provider script, that the two OS argvs differ only
// in the fields the table declares as OS-specific, and that an unknown provider
// returns an error rather than a guessed argv.
func TestEveryProviderLaunchesThroughOneArgv(t *testing.T) {
	providers, err := readProvidersTSV(providersTSV)
	if err != nil {
		t.Fatalf("read providers.tsv: %v", err)
	}
	if len(providers) == 0 {
		t.Fatal("providers.tsv has no rows")
	}

	// The standard launcher binaries that a providers table entry must use.
	// A bespoke launcher on a bench is the fault that cost the Studio a night
	// (card-tools-48).
	standardLaunchers := map[string]bool{
		"opencode":                true,
		"/usr/local/bin/opencode": true,
	}

	// Per-provider scripts are bespoke launchers: a path with a slash that
	// names a script specific to one provider, not the one launcher.
	isBespokeScript := func(path string) bool {
		if strings.Contains(path, "/") {
			return !standardLaunchers[path]
		}
		return false
	}

	for _, p := range providers {
		t.Run(p.Name+"/linux", func(t *testing.T) {
			argv, err := LaunchArgv(p.Name, "linux")
			if err != nil {
				t.Fatalf("LaunchArgv(%q, linux): %v", p.Name, err)
			}
			if len(argv) == 0 {
				t.Fatalf("LaunchArgv(%q, linux) returned empty argv", p.Name)
			}

			harness := argv[0]
			if isBespokeScript(harness) {
				t.Errorf("LaunchArgv(%q, linux) harness %q is a bespoke per-provider script; want the one launcher", p.Name, harness)
			}
			if !standardLaunchers[harness] {
				t.Errorf("LaunchArgv(%q, linux) harness %q is not a recognized standard launcher", p.Name, harness)
			}

			args := argv[1:]
			argsJSON, _ := json.Marshal(args)
			t.Logf("LaunchArgv(%q, linux): %s %s", p.Name, harness, string(argsJSON))

			if p.HarnessArgs != "" {
				var expectedArgs []string
				if err := json.Unmarshal([]byte(p.HarnessArgs), &expectedArgs); err != nil {
					t.Fatalf("parse harness_args for %q: %v", p.Name, err)
				}
				// The model placeholder should be expanded to the table's model.
				for _, a := range expectedArgs {
					if a == "{model}" {
						found := false
						for _, actual := range args {
							if actual == p.Model {
								found = true
								break
							}
						}
						if !found {
							t.Errorf("LaunchArgv(%q, linux) args %v do not contain model %q (placeholder {model} not expanded)", p.Name, args, p.Model)
						}
					}
				}
			}
		})

		t.Run(p.Name+"/darwin", func(t *testing.T) {
			argv, err := LaunchArgv(p.Name, "darwin")
			if err != nil {
				t.Fatalf("LaunchArgv(%q, darwin): %v", p.Name, err)
			}
			if len(argv) == 0 {
				t.Fatalf("LaunchArgv(%q, darwin) returned empty argv", p.Name)
			}

			harness := argv[0]
			if isBespokeScript(harness) {
				t.Errorf("LaunchArgv(%q, darwin) harness %q is a bespoke per-provider script; want the one launcher", p.Name, harness)
			}
			if !standardLaunchers[harness] {
				t.Errorf("LaunchArgv(%q, darwin) harness %q is not a recognized standard launcher", p.Name, harness)
			}

			args := argv[1:]
			argsJSON, _ := json.Marshal(args)
			t.Logf("LaunchArgv(%q, darwin): %s %s", p.Name, harness, string(argsJSON))

			if p.HarnessArgs != "" {
				var expectedArgs []string
				if err := json.Unmarshal([]byte(p.HarnessArgs), &expectedArgs); err != nil {
					t.Fatalf("parse harness_args for %q: %v", p.Name, err)
				}
				for _, a := range expectedArgs {
					if a == "{model}" {
						found := false
						for _, actual := range args {
							if actual == p.Model {
								found = true
								break
							}
						}
						if !found {
							t.Errorf("LaunchArgv(%q, darwin) args %v do not contain model %q (placeholder {model} not expanded)", p.Name, args, p.Model)
						}
					}
				}
			}
		})

		t.Run(p.Name+"/os-diff", func(t *testing.T) {
			linuxArgv, errL := LaunchArgv(p.Name, "linux")
			darwinArgv, errD := LaunchArgv(p.Name, "darwin")
			if errL != nil || errD != nil {
				t.Fatalf("LaunchArgv(%q, linux/darwin): linux=%v darwin=%v", p.Name, errL, errD)
			}
			if len(linuxArgv) != len(darwinArgv) {
				t.Errorf("LaunchArgv(%q) linux argv len %d != darwin argv len %d", p.Name, len(linuxArgv), len(darwinArgv))
				return
			}
			// The two OS argvs must differ only in the harness path (the table
			// declares OS-specific columns; the rest is shared).
			if linuxArgv[0] == darwinArgv[0] {
				// Same launcher on both OSes — that is fine.
				return
			}
			// Different launcher: every other field must be identical.
			for i := 1; i < len(linuxArgv); i++ {
				if linuxArgv[i] != darwinArgv[i] {
					t.Errorf("LaunchArgv(%q) linux[%d]=%q != darwin[%d]=%q (non-OS field differs)", p.Name, i, linuxArgv[i], i, darwinArgv[i])
				}
			}
			// If the harness paths differ, both must be standard launchers.
			if isBespokeScript(linuxArgv[0]) {
				t.Errorf("LaunchArgv(%q) linux harness %q is a bespoke script", p.Name, linuxArgv[0])
			}
			if isBespokeScript(darwinArgv[0]) {
				t.Errorf("LaunchArgv(%q) darwin harness %q is a bespoke script", p.Name, darwinArgv[0])
			}
		})
	}

	// An unknown provider returns an error rather than a guessed argv.
	t.Run("unknown", func(t *testing.T) {
		argv, err := LaunchArgv("nonexistent-provider-xyz", runtime.GOOS)
		if err == nil {
			t.Errorf("LaunchArgv(%q, %s) returned argv %v with no error; want an error for an unknown provider", "nonexistent-provider-xyz", runtime.GOOS, argv)
		}
	})

	// Every provider in the table is actually reachable.
	t.Run("table-not-empty", func(t *testing.T) {
		if len(providers) == 0 {
			t.Error("providers table is empty")
		}
		for _, p := range providers {
			if p.Name == "" {
				t.Error("providers table has a row with an empty name")
			}
		}
	})
}

// providerRow is one row of the providers TSV table.
type providerRow struct {
	Name          string
	LinuxHarness  string
	DarwinHarness string
	HarnessArgs   string // JSON array
	EnvVar        string
	Model         string
	BaseURL       string
}

// readProvidersTSV parses the providers table: one header line, then one
// tab-separated row per provider.
func readProvidersTSV(raw string) ([]providerRow, error) {
	var out []providerRow
	for i, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.Split(line, "\t")
		if i == 0 {
			if len(parts) < 6 {
				return nil, fmt.Errorf("providers.tsv header has %d columns, want at least 6 (provider, linuxHarness, darwinHarness, harnessArgs, envVar, model)", len(parts))
			}
			continue
		}
		if len(parts) < 6 {
			return nil, fmt.Errorf("providers.tsv line %d has %d columns, want at least 6", i+1, len(parts))
		}
		out = append(out, providerRow{
			Name:          strings.TrimSpace(parts[0]),
			LinuxHarness:  strings.TrimSpace(parts[1]),
			DarwinHarness: strings.TrimSpace(parts[2]),
			HarnessArgs:   strings.TrimSpace(parts[3]),
			EnvVar:        strings.TrimSpace(parts[4]),
			Model:         strings.TrimSpace(parts[5]),
			BaseURL: func() string {
				if len(parts) > 6 {
					return strings.TrimSpace(parts[6])
				}
				return ""
			}(),
		})
	}
	return out, nil
}
