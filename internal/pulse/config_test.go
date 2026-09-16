package pulse

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// #828, rule A: pulse.toml and the state file replace env vars and shell counters.
// The config holds slots and headroom per bench, the tick, the refill cadence, the
// integration branches and the runners per machine; a load applies the documented
// default for a missing key and says so, and a changed file between two loads prints
// one CONFIG line naming the keys that moved. The state file carries next_card and
// tick so a restart loses nothing.

func writeConfigFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// A config with the per-bench keys loads them under the bench's name.
func TestConfigPerBenchSlotsAndHeadroomLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pulse.toml")
	writeConfigFile(t, path, "slots.studio = 8\nheadroom.space = 4\n")
	var out bytes.Buffer
	cfg, err := LoadConfig(path, &out)
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Slots["studio"]; got != 8 {
		t.Fatalf("slots.studio = %d, want 8", got)
	}
	if got := cfg.Headroom["space"]; got != 4 {
		t.Fatalf("headroom.space = %d, want 4", got)
	}
}

// A missing key takes the documented default, and the load prints DEFAULT key=<k>.
func TestConfigMissingKeyDefaultsAndPrints(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pulse.toml")
	writeConfigFile(t, path, "slots.studio = 8\n")
	var out bytes.Buffer
	cfg, err := LoadConfig(path, &out)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Tick != DefaultTick {
		t.Fatalf("tick = %d, want the documented default %d", cfg.Tick, DefaultTick)
	}
	if !strings.Contains(out.String(), "DEFAULT key=tick") {
		t.Fatalf("load did not print DEFAULT for the missing key; printed:\n%s", out.String())
	}
}

// A changed file between two loads prints one CONFIG line naming the keys that moved.
func TestConfigChangePrintsOneLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pulse.toml")
	writeConfigFile(t, path, "slots.studio = 8\nheadroom.space = 4\n")
	var out bytes.Buffer
	cfg, err := LoadConfig(path, &out)
	if err != nil {
		t.Fatal(err)
	}
	out.Reset()
	writeConfigFile(t, path, "slots.studio = 8\nheadroom.space = 6\n")
	if err := cfg.Reload(&out); err != nil {
		t.Fatal(err)
	}
	line := strings.TrimSpace(out.String())
	if !strings.HasPrefix(line, "CONFIG ") {
		t.Fatalf("expected one CONFIG line, printed:\n%s", line)
	}
	if !strings.Contains(line, "headroom.space") {
		t.Fatalf("CONFIG line does not name the moved key; printed:\n%s", line)
	}
	if strings.Count(line, "\n") != 0 {
		t.Fatalf("expected exactly one CONFIG line, printed:\n%s", line)
	}
}

// The state file round-trips next_card and tick and survives a simulated restart:
// a fresh load from disk, sharing nothing with the object that saved.
func TestStateRoundTripsAndSurvivesRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pulse.state")
	if err := (&State{NextCard: 3, Tick: 7}).Save(path); err != nil {
		t.Fatal(err)
	}
	got, err := LoadState(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.NextCard != 3 || got.Tick != 7 {
		t.Fatalf("round-trip = {next_card %d, tick %d}, want {3 7}", got.NextCard, got.Tick)
	}
}
