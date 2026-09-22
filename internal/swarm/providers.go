package swarm

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"
)

//go:embed testdata/providers.tsv
var providersTableData string

// providerEntry is one parsed row of the providers table.
type providerEntry struct {
	Name          string
	LinuxHarness  string
	DarwinHarness string
	HarnessArgs   []string
	EnvVar        string
	Model         string
	BaseURL       string
}

// readProvidersTable parses the embedded providers.tsv into a map keyed by
// provider name. The table is the single source of truth for every provider a
// bench can launch: one row replaces ~45 per-provider scripts x 2 OS twins.
func readProvidersTable() (map[string]providerEntry, error) {
	out := map[string]providerEntry{}
	for i, line := range strings.Split(providersTableData, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.Split(line, "\t")
		if i == 0 {
			continue // header
		}
		if len(parts) < 6 {
			return nil, fmt.Errorf("providers.tsv line %d has %d columns, want at least 6 (provider, linuxHarness, darwinHarness, harnessArgs, envVar, model)", i+1, len(parts))
		}
		name := strings.TrimSpace(parts[0])
		var args []string
		if raw := strings.TrimSpace(parts[3]); raw != "" {
			if err := json.Unmarshal([]byte(raw), &args); err != nil {
				return nil, fmt.Errorf("providers.tsv line %d: parse harness_args: %v", i+1, err)
			}
		}
		baseURL := ""
		if len(parts) > 6 {
			baseURL = strings.TrimSpace(parts[6])
		}
		out[name] = providerEntry{
			Name:          name,
			LinuxHarness:  strings.TrimSpace(parts[1]),
			DarwinHarness: strings.TrimSpace(parts[2]),
			HarnessArgs:   args,
			EnvVar:        strings.TrimSpace(parts[4]),
			Model:         strings.TrimSpace(parts[5]),
			BaseURL:       baseURL,
		}
	}
	return out, nil
}

// LaunchArgv returns the argv for launching a provider on the given OS. The argv
// is the harness binary followed by the expanded harness arguments: {model} is
// replaced with the provider's model from the table, and {prompt} with the
// literal "PROMPT.md". Unknown providers return an error rather than a guessed
// argv — a launcher that guesses is a bespoke launcher with extra steps.
//
// Every provider launches through one verb on both OSes. The table declares
// OS-specific columns (linuxHarness, darwinHarness) and the code reads only
// those; no per-provider script is ever named.
func LaunchArgv(provider, goos string) ([]string, error) {
	table, err := readProvidersTable()
	if err != nil {
		return nil, fmt.Errorf("read providers table: %w", err)
	}
	entry, ok := table[provider]
	if !ok {
		return nil, fmt.Errorf("unknown provider %q", provider)
	}

	harness := entry.LinuxHarness
	if goos == "darwin" {
		harness = entry.DarwinHarness
	}

	argv := make([]string, len(entry.HarnessArgs))
	for i, a := range entry.HarnessArgs {
		a = strings.ReplaceAll(a, "{model}", entry.Model)
		a = strings.ReplaceAll(a, "{prompt}", "PROMPT.md")
		argv[i] = a
	}
	return append([]string{harness}, argv...), nil
}
