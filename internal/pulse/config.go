package pulse

import (
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
)

// #828, rule A: the coordination loop's env vars and shell counters become pulse.toml
// and the state file, so a restart loses nothing and a change is printed, not remembered.
//
// pulse.toml holds the coordination facts per machine, read fresh every tick:
//
//	tick                  = 10          # seconds, the manager cycle
//	refill-cadence        = 60          # seconds between queue refills to the floor
//	integration-branches  = ["dev"]     # branches a gated card waits on
//	runners-per-machine   = 1           # swarm runners this machine hosts
//
//	[slots]    studio = 8               # slots per bench (or the dotted keys slots.*)
//	[headroom] space  = 4               # per-tick headroom per bench (headroom.*)
//
// A missing top-level key takes its documented default and the load prints
// `DEFAULT key=<k>`. A key that changes between two loads is named in one
// `CONFIG <k> [<k> ...]` line, so the coordinator re-reads rather than remembers.

const (
	// DefaultTick is the manager cycle length in seconds when pulse.toml names no tick.
	DefaultTick = 10
	// DefaultRefillCadence is the queue refill interval in seconds when pulse.toml names none.
	DefaultRefillCadence = 60
	// DefaultRunnersPerMachine is the swarm runner count when pulse.toml names none.
	DefaultRunnersPerMachine = 1
)

// DefaultIntegrationBranches is the integration branch list when pulse.toml names none.
var DefaultIntegrationBranches = []string{"dev"}

// defaultableKeys are the top-level keys that carry a documented default: a missing one
// fills the default and prints DEFAULT. Per-bench slots and headroom grow maps instead.
var defaultableKeys = []string{"tick", "refill-cadence", "integration-branches", "runners-per-machine"}

// Config is one pulse.toml's values. Slots and Headroom are keyed by bench name.
type Config struct {
	Tick                int
	RefillCadence       int
	IntegrationBranches []string
	RunnersPerMachine   int
	Slots               map[string]int
	Headroom            map[string]int

	path string
	seen map[string]string // the canonical values last loaded, keyed by logical key
}

// LoadConfig reads path, applies the documented defaults for missing top-level keys and
// prints a DEFAULT line for each one, and remembers the file so a later Reload can report
// changes. The print goes to out.
func LoadConfig(path string, out io.Writer) (*Config, error) {
	cfg, defaulted, err := readConfig(path)
	if err != nil {
		return nil, err
	}
	cfg.path = path
	cfg.seen = cfg.canonical()
	for _, k := range defaulted {
		fmt.Fprintf(out, "DEFAULT key=%s\n", k)
	}
	return cfg, nil
}

// Reload re-reads the config's file (its documented tick cadence) and, when any key moved,
// prints one CONFIG line naming the changed keys. Missing keys fall back to their defaults
// as at load, and nothing is printed when nothing changed.
func (c *Config) Reload(out io.Writer) error {
	next, _, err := readConfig(c.path)
	if err != nil {
		return err
	}
	now := next.canonical()
	changed := make([]string, 0, len(now))
	for k, v := range now {
		if c.seen[k] != v {
			changed = append(changed, k)
		}
	}
	for k := range c.seen {
		if _, ok := now[k]; !ok {
			changed = append(changed, k)
		}
	}
	if len(changed) > 0 {
		sort.Strings(changed)
		fmt.Fprintf(out, "CONFIG %s\n", strings.Join(changed, " "))
	}
	path := c.path
	*c = *next
	c.path = path
	c.seen = now
	return nil
}

// canonical flattens the config into logical key -> value strings for change detection.
func (c *Config) canonical() map[string]string {
	m := map[string]string{
		"tick":                 strconv.Itoa(c.Tick),
		"refill-cadence":       strconv.Itoa(c.RefillCadence),
		"integration-branches": strings.Join(c.IntegrationBranches, ","),
		"runners-per-machine":  strconv.Itoa(c.RunnersPerMachine),
	}
	for k, v := range c.Slots {
		m["slots."+k] = strconv.Itoa(v)
	}
	for k, v := range c.Headroom {
		m["headroom."+k] = strconv.Itoa(v)
	}
	return m
}

// readConfig parses path into a populated Config and reports which defaultable keys the
// file left out (and so took their defaults).
func readConfig(path string) (*Config, []string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}
	vals, err := parseConfig(string(raw))
	if err != nil {
		return nil, nil, fmt.Errorf("%s: %w", path, err)
	}
	cfg := &Config{
		Tick:                DefaultTick,
		RefillCadence:       DefaultRefillCadence,
		IntegrationBranches: append([]string(nil), DefaultIntegrationBranches...),
		RunnersPerMachine:   DefaultRunnersPerMachine,
		Slots:               map[string]int{},
		Headroom:            map[string]int{},
	}
	var defaulted []string
	for _, k := range defaultableKeys {
		if _, ok := vals[k]; !ok {
			defaulted = append(defaulted, k)
		}
	}
	for k, v := range vals {
		switch {
		case strings.HasPrefix(k, "slots."):
			n, err := parseInt(k, v)
			if err != nil {
				return nil, nil, err
			}
			cfg.Slots[strings.TrimPrefix(k, "slots.")] = n
		case strings.HasPrefix(k, "headroom."):
			n, err := parseInt(k, v)
			if err != nil {
				return nil, nil, err
			}
			cfg.Headroom[strings.TrimPrefix(k, "headroom.")] = n
		case k == "tick":
			if cfg.Tick, err = parseInt(k, v); err != nil {
				return nil, nil, err
			}
		case k == "refill-cadence":
			if cfg.RefillCadence, err = parseInt(k, v); err != nil {
				return nil, nil, err
			}
		case k == "runners-per-machine":
			if cfg.RunnersPerMachine, err = parseInt(k, v); err != nil {
				return nil, nil, err
			}
		case k == "integration-branches":
			cfg.IntegrationBranches, err = parseStrings(k, v)
			if err != nil {
				return nil, nil, err
			}
		default:
			return nil, nil, fmt.Errorf("%s: unknown key %q", path, k)
		}
	}
	return cfg, defaulted, nil
}

// parseInt reads one integer value and returns a keyed error on failure.
func parseInt(key, val string) (int, error) {
	n, err := strconv.Atoi(strings.TrimSpace(val))
	if err != nil {
		return 0, fmt.Errorf("key %q: %q is not an integer", key, strings.TrimSpace(val))
	}
	return n, nil
}

// parseStrings reads a TOML array of strings, e.g. `["dev", "main"]`.
func parseStrings(key, val string) ([]string, error) {
	v := strings.TrimSpace(val)
	if !strings.HasPrefix(v, "[") || !strings.HasSuffix(v, "]") {
		return nil, fmt.Errorf("key %q: %q is not an array", key, v)
	}
	inner := strings.TrimSpace(v[1 : len(v)-1])
	if inner == "" {
		return []string{}, nil
	}
	parts := strings.Split(inner, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if len(p) < 2 {
			return nil, fmt.Errorf("key %q: bad array element %q", key, p)
		}
		out = append(out, p[1:len(p)-1])
	}
	return out, nil
}

// parseConfig turns pulse.toml text into logical key -> value strings. It reads the subset
// this tool uses: `key = value`, dotted keys, `[table]` sections and full-line comments.
func parseConfig(src string) (map[string]string, error) {
	vals := map[string]string{}
	section := ""
	for i, raw := range strings.Split(src, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = strings.TrimSpace(line[1 : len(line)-1])
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			return nil, fmt.Errorf("line %d: expected key = value, got %q", i+1, raw)
		}
		key = strings.TrimSpace(key)
		val = strings.TrimSpace(val)
		if section != "" {
			key = section + "." + key
		}
		vals[key] = val
	}
	return vals, nil
}
