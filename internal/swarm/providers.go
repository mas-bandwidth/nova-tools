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
// provider name. It declares the four providers confirmed in docs/MODELS.md
// (one row per provider, both OSes) as the data the one launcher will read.
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

// DefaultLaunchRow names the providers-table row a route launches with when its provider
// has no row of its own. It is declared in the table like every other row, so a route the
// table does not name still launches through the one launcher and its one argv shape --
// never through a guessed argv or a per-provider script.
const DefaultLaunchRow = "*"

// LaunchRow is the table row a provider launches with: its own row when the table names
// it, and DefaultLaunchRow otherwise. A table that cannot be read answers the provider
// itself, so the caller's LaunchArgvFor reports the read error.
func LaunchRow(provider string) string {
	table, err := readProvidersTable()
	if err != nil {
		return provider
	}
	if _, ok := table[provider]; ok {
		return provider
	}
	return DefaultLaunchRow
}

// LaunchRequest is what one run hands the one launcher beyond the table. Every field is
// optional: an empty one takes the table's value (Harness: the row's column for goos;
// Model: the row's model; Title: the provider name; Prompt: the literal "PROMPT.md").
type LaunchRequest struct {
	Harness string // the resolved harness binary the caller runs
	Model   string // the provider/model the caller routed this run to
	Title   string // the run's label, the harness's --title
	Prompt  string // the prompt argument the harness is handed
}

// LaunchArgv returns the argv for launching a provider on the given OS. The argv
// is the harness binary followed by the expanded harness arguments: {model} is
// replaced with the provider's model from the table, {title} with the provider
// name, and {prompt} with the literal "PROMPT.md". Unknown providers return an
// error rather than a guessed argv -- a launcher that guesses is a bespoke
// launcher with extra steps.
func LaunchArgv(provider, goos string) ([]string, error) {
	return LaunchArgvFor(provider, goos, LaunchRequest{})
}

// LaunchArgvFor is LaunchArgv for one run: the table's row for provider gives the
// argv shape, and the request's non-empty fields fill it. This is the one launcher:
// `nova-swarm native` builds every harness argv here (cmd/nova-swarm nativeLaunchArgv),
// so the table, not a per-provider script and not a literal in the caller, decides the
// shape a provider is launched with. The table declares OS-specific columns
// (linuxHarness, darwinHarness) and only those differ between the two OSes.
func LaunchArgvFor(provider, goos string, req LaunchRequest) ([]string, error) {
	table, err := readProvidersTable()
	if err != nil {
		return nil, fmt.Errorf("read providers table: %w", err)
	}
	entry, ok := table[provider]
	if !ok {
		return nil, fmt.Errorf("unknown provider %q", provider)
	}

	var harness string
	switch goos {
	case "darwin":
		harness = entry.DarwinHarness
	case "linux":
		harness = entry.LinuxHarness
	default:
		return nil, fmt.Errorf("unsupported GOOS %q", goos)
	}
	if req.Harness != "" {
		harness = req.Harness
	}
	model := entry.Model
	if req.Model != "" {
		model = req.Model
	}
	title := provider
	if req.Title != "" {
		title = req.Title
	}
	prompt := "PROMPT.md"
	if req.Prompt != "" {
		prompt = req.Prompt
	}

	// One pass over each template argument, so a value that itself spells a placeholder
	// (a card that mentions {model}) is handed to the harness verbatim.
	fill := strings.NewReplacer("{model}", model, "{title}", title, "{prompt}", prompt)
	argv := make([]string, len(entry.HarnessArgs))
	for i, a := range entry.HarnessArgs {
		argv[i] = fill.Replace(a)
	}
	return append([]string{harness}, argv...), nil
}
