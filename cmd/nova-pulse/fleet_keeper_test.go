package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type mockKeeperRunner struct {
	loads   []string
	unloads []string
	lists   []string
}

func (m *mockKeeperRunner) Load(ctx context.Context, plistPath string) (string, error) {
	m.loads = append(m.loads, plistPath)
	return "loaded", nil
}

func (m *mockKeeperRunner) Unload(ctx context.Context, plistPath string) (string, error) {
	m.unloads = append(m.unloads, plistPath)
	return "unloaded", nil
}

func (m *mockKeeperRunner) List(ctx context.Context, label string) (string, error) {
	m.lists = append(m.lists, label)
	return "\"PID\" = 9999;\n\"LastExitStatus\" = 0;\n", nil
}

func TestFleetKeeperRefusesMissingAction(t *testing.T) {
	var out, errb bytes.Buffer
	code := run([]string{"fleet", "keeper"}, &out, &errb, time.Now().UTC())
	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if !strings.Contains(errb.String(), "an action is required") {
		t.Errorf("refusal does not mention action required:\n%s", errb.String())
	}
}

func TestFleetKeeperInspect(t *testing.T) {
	var out, errb bytes.Buffer
	code := run([]string{"fleet", "keeper", "--inspect", "--unit", "dealer"}, &out, &errb, time.Now().UTC())
	if code != 0 {
		t.Fatalf("exit = %d, want 0; err=%s", code, errb.String())
	}
	outStr := out.String()
	if !strings.Contains(outStr, "<key>Label</key>\n\t<string>com.mas-bandwidth.nova-dealer</string>") {
		t.Errorf("inspect output missing dealer label:\n%s", outStr)
	}
	if !strings.Contains(outStr, "<key>KeepAlive</key>\n\t<true/>") {
		t.Errorf("inspect output missing KeepAlive true:\n%s", outStr)
	}
}

func TestFleetKeeperInstallLaunchd(t *testing.T) {
	mockRunner := &mockKeeperRunner{}
	oldRunner := keeperLaunchctlRunner
	keeperLaunchctlRunner = mockRunner
	defer func() { keeperLaunchctlRunner = oldRunner }()

	tmpDir := t.TempDir()
	plistDir := filepath.Join(tmpDir, "LaunchAgents")
	logDir := filepath.Join(tmpDir, "logs")

	var out, errb bytes.Buffer
	code := run([]string{
		"fleet", "keeper",
		"--install-launchd",
		"--dir", plistDir,
		"--log-dir", logDir,
		"--work-dir", tmpDir,
	}, &out, &errb, time.Now().UTC())

	if code != 0 {
		t.Fatalf("exit = %d, want 0; err=%s", code, errb.String())
	}

	if len(mockRunner.loads) != 4 {
		t.Fatalf("expected 4 loads, got %d", len(mockRunner.loads))
	}

	for _, name := range []string{"dealer", "backpressure", "harvest", "sprint"} {
		f := filepath.Join(plistDir, "com.mas-bandwidth.nova-"+name+".plist")
		if _, err := os.Stat(f); err != nil {
			t.Errorf("missing plist file for %s at %s", name, f)
		}
	}
}

func TestFleetKeeperPositionalSubverbs(t *testing.T) {
	mockRunner := &mockKeeperRunner{}
	oldRunner := keeperLaunchctlRunner
	keeperLaunchctlRunner = mockRunner
	defer func() { keeperLaunchctlRunner = oldRunner }()

	tmpDir := t.TempDir()
	plistDir := filepath.Join(tmpDir, "LaunchAgents")

	var out, errb bytes.Buffer
	// Test generate positional
	code := run([]string{"fleet", "keeper", "generate", "--dir", plistDir, "--unit", "harvest"}, &out, &errb, time.Now().UTC())
	if code != 0 {
		t.Fatalf("generate exit = %d, want 0; err=%s", code, errb.String())
	}
	harvestPlist := filepath.Join(plistDir, "com.mas-bandwidth.nova-harvest.plist")
	if _, err := os.Stat(harvestPlist); err != nil {
		t.Errorf("generate did not write harvest plist: %v", err)
	}

	// Test status positional
	out.Reset()
	code = run([]string{"fleet", "keeper", "status", "--dir", plistDir, "--unit", "harvest"}, &out, &errb, time.Now().UTC())
	if code != 0 {
		t.Fatalf("status exit = %d, want 0; err=%s", code, errb.String())
	}
	if !strings.Contains(out.String(), "KEEPER STATUS unit=harvest") {
		t.Errorf("status output missing harvest: %s", out.String())
	}

	// Test unload positional
	out.Reset()
	code = run([]string{"fleet", "keeper", "unload", "--dir", plistDir, "--unit", "harvest"}, &out, &errb, time.Now().UTC())
	if code != 0 {
		t.Fatalf("unload exit = %d, want 0; err=%s", code, errb.String())
	}
	if len(mockRunner.unloads) != 1 {
		t.Fatalf("expected 1 unload, got %d", len(mockRunner.unloads))
	}
}

func TestFleetKeeperRefusesUnknownUnit(t *testing.T) {
	var out, errb bytes.Buffer
	code := run([]string{"fleet", "keeper", "--inspect", "--unit", "bogus"}, &out, &errb, time.Now().UTC())
	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if !strings.Contains(errb.String(), "unknown unit \"bogus\"") {
		t.Errorf("refusal does not name unknown unit:\n%s", errb.String())
	}
}
