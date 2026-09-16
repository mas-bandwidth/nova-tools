package pulse

// The loop's configuration is ONE FILE in the queue directory, and its counters are one
// state file beside it (state.go). Pit stop 3, class A: the loop used to carry its
// configuration in environment variables, so a restart for a headroom change forgot
// PULSE_STUDIO_SLOTS=8 and the Studio launched eleven cards at load 26 (issue #828, bug 2),
// and a value could not be changed at all without a restart that lost the counters (bug 3).
// A tick loads this file; a value edited between two ticks is named on the CONFIG line and
// takes effect on the next one, with no restart and nothing forgotten.
//
// The file is key=value, `#` comments, blank lines ignored, and the toml subset a reader
// of a file called pulse.toml expects: a `[slots]` table is the dotted `slots.*` keys, and
// a value written `["main", "dev"]` is the list written `main,dev` (the section and array
// reading is taken from PR #830). It is called pulse.toml because that is what it will be
// when a toml reader is worth a dependency; go.mod carries none today and this card adds
// none.

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/bounded"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// ConfigFile is the loop's configuration, in the queue directory. SeenFile is the snapshot
// of the last load, which is how a CONFIG line can say what changed and a DEFAULT line can
// be printed once rather than every tick.
const (
	ConfigFile = "pulse.toml"
	SeenFile   = "pulse.config.seen"
)

// The documented defaults. A missing key takes the value here and says so once.
const (
	DefaultSlotsStudio       = 8
	DefaultSlotsSpace        = 8
	DefaultSlotsLocal        = 2
	DefaultHeadroomStudio    = 2
	DefaultHeadroomSpace     = 2
	DefaultHeadroomLocal     = 1
	DefaultTickSeconds       = 60
	DefaultRefillCadence     = 5
	DefaultRunnersPerMachine = 8
)

// DefaultIntegrationBranches is the one list of integration branches (class B, bug 8: dev
// inherited main's cancellation bug because the branch name was written twice).
var DefaultIntegrationBranches = []string{"main", "dev"}

// Benches are the benches this configuration knows, in the order a line prints them.
var Benches = []string{"studio", "space", "local"}

// Config is the loop's whole configuration: slots and headroom per bench, the tick, the
// refill cadence, the integration branches and the runners each machine runs.
type Config struct {
	Slots               map[string]int
	Headroom            map[string]int
	TickSeconds         int
	RefillCadence       int
	IntegrationBranches []string
	RunnersPerMachine   int
}

// configKeys is every key the file may carry, in the order the CONFIG line and the
// refusal name them. The loop never expands its configuration: an unknown key is a
// refusal, as the manager's policy is, because a configuration the tool half-understands
// is one nobody approved.
var configKeys = []string{
	"slots.studio", "slots.space", "slots.local",
	"headroom.studio", "headroom.space", "headroom.local",
	"tick.seconds", "refill.cadence", "integration.branches", "runners.per-machine",
}

// defaultValues is each key's documented default, as the file would have written it.
func defaultValues() map[string]string {
	return map[string]string{
		"slots.studio":         strconv.Itoa(DefaultSlotsStudio),
		"slots.space":          strconv.Itoa(DefaultSlotsSpace),
		"slots.local":          strconv.Itoa(DefaultSlotsLocal),
		"headroom.studio":      strconv.Itoa(DefaultHeadroomStudio),
		"headroom.space":       strconv.Itoa(DefaultHeadroomSpace),
		"headroom.local":       strconv.Itoa(DefaultHeadroomLocal),
		"tick.seconds":         strconv.Itoa(DefaultTickSeconds),
		"refill.cadence":       strconv.Itoa(DefaultRefillCadence),
		"integration.branches": strings.Join(DefaultIntegrationBranches, ","),
		"runners.per-machine":  strconv.Itoa(DefaultRunnersPerMachine),
	}
}

// LoadConfig reads <dir>/pulse.toml, takes the documented default for every key it does not
// carry, and prints ONE CONFIG line naming the keys that changed since the last load, plus
// one DEFAULT line per key taking a default it has not announced before, bounded at max.
// A missing file is every default and no refusal: a first tick on a fresh queue still runs.
func LoadConfig(dir string, out io.Writer, max int) (Config, error) {
	path := filepath.Join(dir, ConfigFile)
	given, err := readKV(path)
	if err != nil {
		return Config{}, err
	}
	for k := range given {
		if !slicesContains(configKeys, k) {
			return Config{}, fmt.Errorf("%s: unknown key %q; the loop never expands its configuration (the keys are %s)",
				path, k, strings.Join(configKeys, ", "))
		}
	}

	effective := defaultValues()
	var missing []string
	for _, k := range configKeys {
		if v, ok := given[k]; ok {
			effective[k] = v
			continue
		}
		missing = append(missing, k)
	}

	cfg, err := configFrom(effective, path)
	if err != nil {
		return Config{}, err
	}

	// What the last load saw, so this one can say only what is new.
	seenPath := filepath.Join(dir, SeenFile)
	seen, err := readKV(seenPath)
	if err != nil {
		return Config{}, err
	}
	first := len(seen) == 0

	defaults := bounded.Capped(out, max, "CONFIG", "default", "(write the key into "+path+" to pin it)")
	for _, k := range missing {
		if !first && seen[k] == effective[k] {
			continue // already announced, and unchanged: a line that carries nothing.
		}
		defaults.Line("DEFAULT " + k + "=" + oneline.Field(effective[k]))
	}
	defaults.More()

	var changed []string
	if !first {
		for _, k := range configKeys {
			if seen[k] != effective[k] {
				changed = append(changed, k)
			}
		}
	}
	sort.Strings(changed)

	if err := writeKV(seenPath, configKeys, effective); err != nil {
		return Config{}, err
	}

	keys := "-"
	if len(changed) > 0 {
		keys = strings.Join(changed, ",")
	}
	fmt.Fprintf(out, "CONFIG OK file=%s changed=%d defaults=%d keys=%s\n",
		oneline.Field(path), len(changed), len(missing), oneline.Field(keys))
	return cfg, nil
}

// configFrom turns the effective key=value map into the typed configuration, refusing a
// value that is not what its key wants.
func configFrom(v map[string]string, path string) (Config, error) {
	cfg := Config{Slots: map[string]int{}, Headroom: map[string]int{}}
	num := func(key string, least int) (int, error) {
		n, err := strconv.Atoi(strings.TrimSpace(v[key]))
		if err != nil || n < least {
			return 0, fmt.Errorf("%s: %s wants a whole number %d or more, got %q", path, key, least, v[key])
		}
		return n, nil
	}
	var err error
	for _, b := range Benches {
		if cfg.Slots[b], err = num("slots."+b, 0); err != nil {
			return Config{}, err
		}
		if cfg.Headroom[b], err = num("headroom."+b, 0); err != nil {
			return Config{}, err
		}
	}
	if cfg.TickSeconds, err = num("tick.seconds", 1); err != nil {
		return Config{}, err
	}
	if cfg.RefillCadence, err = num("refill.cadence", 1); err != nil {
		return Config{}, err
	}
	if cfg.RunnersPerMachine, err = num("runners.per-machine", 1); err != nil {
		return Config{}, err
	}
	cfg.IntegrationBranches = splitList(v["integration.branches"])
	if len(cfg.IntegrationBranches) == 0 {
		return Config{}, fmt.Errorf("%s: integration.branches wants at least one branch, got %q (the list is one fact: %s)",
			path, v["integration.branches"], strings.Join(DefaultIntegrationBranches, ","))
	}
	return cfg, nil
}

// readKV reads a flat key=value file. A file that is not there is an empty map and no
// error: a missing configuration is every default, and a missing snapshot is a first load.
func readKV(path string) (map[string]string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]string{}, nil
		}
		return nil, fmt.Errorf("cannot read %s: %s", path, oneline.Err(err))
	}
	out := map[string]string{}
	section := ""
	for n, line := range strings.Split(string(raw), "\n") {
		t := strings.TrimSpace(line)
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		if strings.HasPrefix(t, "[") && strings.HasSuffix(t, "]") {
			section = strings.TrimSpace(t[1 : len(t)-1])
			continue
		}
		key, value, ok := strings.Cut(t, "=")
		if !ok {
			return nil, fmt.Errorf("%s line %d is not key=value: %q (the file is key=value lines, one per key, under an optional [table])", path, n+1, t)
		}
		key = strings.TrimSpace(key)
		if section != "" {
			key = section + "." + key
		}
		out[key] = unarray(strings.TrimSpace(value))
	}
	return out, nil
}

// writeKV writes a flat key=value file whole, through a temp file and a rename, so a tick
// that dies mid-write leaves the last good file rather than half of this one.
func writeKV(path string, keys []string, v map[string]string) error {
	var sb strings.Builder
	for _, k := range keys {
		sb.WriteString(k + "=" + v[k] + "\n")
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(sb.String()), 0o644); err != nil {
		return fmt.Errorf("cannot write %s: %s", path, oneline.Err(err))
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("cannot write %s: %s", path, oneline.Err(err))
	}
	return nil
}

// unarray reads a toml array of strings as the comma-separated list this file's flat
// spelling uses: ["main", "dev"] and main,dev are one value written two ways.
func unarray(v string) string {
	if !strings.HasPrefix(v, "[") || !strings.HasSuffix(v, "]") {
		return v
	}
	var items []string
	for _, p := range strings.Split(strings.TrimSpace(v[1:len(v)-1]), ",") {
		p = strings.TrimSpace(p)
		p = strings.TrimPrefix(p, `"`)
		p = strings.TrimSuffix(p, `"`)
		p = strings.TrimPrefix(p, "'")
		p = strings.TrimSuffix(p, "'")
		if p != "" {
			items = append(items, p)
		}
	}
	return strings.Join(items, ",")
}

func slicesContains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
