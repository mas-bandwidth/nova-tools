package pulse

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func writeConfig(t *testing.T, dir, body string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ConfigFile), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// config-file-is-the-configuration (class A): the loop's configuration is one file in the
// queue directory -- slots and headroom per bench, tick, refill cadence, the integration
// branches and the runners per machine -- and every value read is the file's, never an
// environment variable's.
func TestConfigReadsEveryKeyFromTheFile(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, strings.Join([]string{
		"# the loop's configuration, one file",
		"slots.studio = 11",
		"slots.space=19",
		"slots.local = 2",
		"headroom.studio = 3",
		"headroom.space = 4",
		"headroom.local = 1",
		"tick.seconds = 30",
		"refill.cadence = 7",
		"integration.branches = main, dev, next",
		"runners.per-machine = 8",
		"launch.files = 40",
		"launch.tokens = unmetered",
		"",
	}, "\n"))

	var out bytes.Buffer
	cfg, err := LoadConfig(dir, &out, 20)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.Slots["studio"] != 11 || cfg.Slots["space"] != 19 || cfg.Slots["local"] != 2 {
		t.Errorf("slots = %v, want studio 11, space 19, local 2", cfg.Slots)
	}
	if cfg.Headroom["studio"] != 3 || cfg.Headroom["space"] != 4 || cfg.Headroom["local"] != 1 {
		t.Errorf("headroom = %v, want studio 3, space 4, local 1", cfg.Headroom)
	}
	if cfg.TickSeconds != 30 || cfg.RefillCadence != 7 || cfg.RunnersPerMachine != 8 {
		t.Errorf("tick=%d refill=%d runners=%d, want 30, 7, 8", cfg.TickSeconds, cfg.RefillCadence, cfg.RunnersPerMachine)
	}
	if got := strings.Join(cfg.IntegrationBranches, ","); got != "main,dev,next" {
		t.Errorf("integration branches = %q, want main,dev,next", got)
	}
	if cfg.Files != 40 || cfg.Tokens != "unmetered" {
		t.Errorf("files=%d tokens=%q, want 40 and unmetered", cfg.Files, cfg.Tokens)
	}
	lines := nonEmptyLines(out.String())
	if len(lines) != 1 || !strings.HasPrefix(lines[0], "CONFIG OK ") {
		t.Fatalf("want exactly one CONFIG line, got %q", out.String())
	}
}

// config-default-printed-once: a missing key takes the documented default and says so once;
// the second load of the same unchanged file says nothing about it again.
func TestConfigDefaultIsPrintedOnce(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, "slots.studio = 6\n")

	var first bytes.Buffer
	cfg, err := LoadConfig(dir, &first, 20)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.TickSeconds != DefaultTickSeconds {
		t.Errorf("tick = %d, want the documented default %d", cfg.TickSeconds, DefaultTickSeconds)
	}
	if !strings.Contains(first.String(), "DEFAULT tick.seconds="+strconv.Itoa(DefaultTickSeconds)) {
		t.Fatalf("first load did not name the default it took: %q", first.String())
	}

	var second bytes.Buffer
	if _, err := LoadConfig(dir, &second, 20); err != nil {
		t.Fatalf("second LoadConfig: %v", err)
	}
	if strings.Contains(second.String(), "DEFAULT ") {
		t.Fatalf("the same default was announced twice: %q", second.String())
	}
	lines := nonEmptyLines(second.String())
	if len(lines) != 1 || !strings.HasPrefix(lines[0], "CONFIG OK ") {
		t.Fatalf("want exactly one CONFIG line on an unchanged reload, got %q", second.String())
	}
}

// config-names-what-changed: a value edited between two loads is named on the CONFIG line,
// and changing a value never needs a restart -- the next load is the next tick's.
func TestConfigNamesTheKeysThatChanged(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, "slots.studio = 8\ntick.seconds = 60\n")
	var first bytes.Buffer
	if _, err := LoadConfig(dir, &first, 20); err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if !strings.Contains(first.String(), "changed=0") {
		t.Fatalf("a first load has nothing to compare against: %q", first.String())
	}

	writeConfig(t, dir, "slots.studio = 4\ntick.seconds = 60\n")
	var second bytes.Buffer
	cfg, err := LoadConfig(dir, &second, 20)
	if err != nil {
		t.Fatalf("second LoadConfig: %v", err)
	}
	if cfg.Slots["studio"] != 4 {
		t.Errorf("slots.studio = %d, want the edited 4 with no restart", cfg.Slots["studio"])
	}
	line := strings.TrimSpace(second.String())
	if !strings.Contains(line, "changed=1") || !strings.Contains(line, "keys=slots.studio") {
		t.Fatalf("CONFIG line did not name the changed key: %q", line)
	}
	if len(nonEmptyLines(line)) != 1 {
		t.Fatalf("want one line, got %q", line)
	}
}

// config-never-expands: an unknown key is a refusal naming the keys, as the manager's
// policy is -- a configuration the tool half-understands is one nobody approved.
func TestConfigRefusesUnknownKey(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, "slots.studio = 8\nslots.moon = 3\n")
	var out bytes.Buffer
	if _, err := LoadConfig(dir, &out, 20); err == nil {
		t.Fatal("an unknown key was accepted")
	} else if !strings.Contains(err.Error(), "slots.moon") || !strings.Contains(err.Error(), "slots.studio") {
		t.Fatalf("the refusal names neither the key nor the remedy: %v", err)
	}
}

// config-missing-file-is-all-defaults: no file at all is every documented default, one
// CONFIG line, and no refusal -- a first tick on a fresh queue still runs.
func TestConfigMissingFileIsAllDefaults(t *testing.T) {
	dir := t.TempDir()
	var out bytes.Buffer
	cfg, err := LoadConfig(dir, &out, 20)
	if err != nil {
		t.Fatalf("LoadConfig on a bare directory: %v", err)
	}
	if cfg.TickSeconds != DefaultTickSeconds || cfg.RunnersPerMachine != DefaultRunnersPerMachine {
		t.Errorf("defaults not taken: %+v", cfg)
	}
	if !strings.Contains(out.String(), "CONFIG OK ") {
		t.Fatalf("no CONFIG line: %q", out.String())
	}
}

// config-output-is-bounded: a bare queue takes every default at once, and the announcement
// is capped with one MORE line carrying the count, per SPEC.md's bounded output.
func TestConfigDefaultsAreBounded(t *testing.T) {
	dir := t.TempDir()
	var out bytes.Buffer
	if _, err := LoadConfig(dir, &out, 3); err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	defaults, more := 0, 0
	for _, l := range nonEmptyLines(out.String()) {
		switch {
		case strings.HasPrefix(l, "DEFAULT "):
			defaults++
		case strings.HasPrefix(l, "CONFIG MORE "):
			more++
		}
	}
	if defaults != 3 || more != 1 {
		t.Fatalf("want 3 DEFAULT lines and one CONFIG MORE, got %d and %d: %q", defaults, more, out.String())
	}
}

func nonEmptyLines(s string) []string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if strings.TrimSpace(l) != "" {
			out = append(out, l)
		}
	}
	return out
}

// config-reads-toml-sections-and-arrays (taken from PR #830, which parsed both): a file
// written as toml -- a [slots] table, an array of branches -- reads to the same keys as the
// flat dotted spelling, so the name pulse.toml is not a lie to whoever opens it.
func TestConfigReadsSectionsAndArrays(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, strings.Join([]string{
		"tick.seconds = 30",
		"integration.branches = [\"main\", \"dev\"]",
		"",
		"[slots]",
		"studio = 12",
		"space = 19",
		"",
		"[headroom]",
		"studio = 3",
		"",
	}, "\n"))
	var out bytes.Buffer
	cfg, err := LoadConfig(dir, &out, 20)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.Slots["studio"] != 12 || cfg.Slots["space"] != 19 || cfg.Headroom["studio"] != 3 {
		t.Fatalf("sections did not read: slots=%v headroom=%v", cfg.Slots, cfg.Headroom)
	}
	if got := strings.Join(cfg.IntegrationBranches, ","); got != "main,dev" {
		t.Fatalf("integration branches = %q, want main,dev", got)
	}
}

// launch-files-budget-is-configuration (issue #869): the wired launch carries a file budget
// because `nova-swarm batch` refuses without one; the key is `[launch] files`, its default is
// the documented 40, and a configuration that does not carry it says so ONCE like every other
// key. The mutation that matters: the key read but never announced, so the first tick of the
// switch refused with "--files is required and is at least 1, got 0" and no line said why.
func TestConfigLaunchFilesDefaultsAndIsPrintedOnce(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, "slots.studio = 6\n")

	var first bytes.Buffer
	cfg, err := LoadConfig(dir, &first, 20)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.Files != DefaultLaunchFiles {
		t.Errorf("files = %d, want the documented default %d", cfg.Files, DefaultLaunchFiles)
	}
	if n := strings.Count(first.String(), "DEFAULT launch.files="); n != 1 {
		t.Fatalf("want exactly one DEFAULT launch.files line, got %d:\n%s", n, first.String())
	}

	var second bytes.Buffer
	if _, err := LoadConfig(dir, &second, 20); err != nil {
		t.Fatalf("second LoadConfig: %v", err)
	}
	if strings.Contains(second.String(), "launch.files") {
		t.Fatalf("the default was announced twice:\n%s", second.String())
	}

	writeConfig(t, dir, "slots.studio = 6\n[launch]\nfiles = 12\n")
	var third bytes.Buffer
	cfg, err = LoadConfig(dir, &third, 20)
	if err != nil {
		t.Fatalf("third LoadConfig: %v", err)
	}
	if cfg.Files != 12 {
		t.Fatalf("files = %d, want the file's 12", cfg.Files)
	}
}

// config-headroom-takes-a-decimal (issue #869, the smaller edge): the spec's headroom is a
// ratio of cores, so 2.5 is a value a person writes; the loader used Atoi and refused it with
// "wants a whole number". The mutation that matters: 2.5 truncated to 2 at load, which is a
// different bench width than the file asked for.
func TestConfigHeadroomTakesADecimal(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, "headroom.studio = 2.5\nheadroom.space = 4\n")

	var out bytes.Buffer
	cfg, err := LoadConfig(dir, &out, 20)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.Headroom["studio"] != 2.5 {
		t.Errorf("headroom.studio = %v, want 2.5", cfg.Headroom["studio"])
	}
	if cfg.Headroom["space"] != 4 {
		t.Errorf("headroom.space = %v, want 4", cfg.Headroom["space"])
	}
}
