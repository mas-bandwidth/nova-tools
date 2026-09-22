package pulse

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

type fakeLaunchctlRunner struct {
	loads   []string
	unloads []string
	lists   []string
	listOut string
	loadErr error
}

func (f *fakeLaunchctlRunner) Load(ctx context.Context, plistPath string) (string, error) {
	f.loads = append(f.loads, plistPath)
	return "loaded", f.loadErr
}

func (f *fakeLaunchctlRunner) Unload(ctx context.Context, plistPath string) (string, error) {
	f.unloads = append(f.unloads, plistPath)
	return "unloaded", nil
}

func (f *fakeLaunchctlRunner) List(ctx context.Context, label string) (string, error) {
	f.lists = append(f.lists, label)
	if f.listOut != "" {
		return f.listOut, nil
	}
	return "PID = 12345;\nLastExitStatus = 0;\n", nil
}

func TestKeeperUnitsContainDealerBackpressureHarvestSprint(t *testing.T) {
	cfg := KeeperLaunchdConfig{
		HomeDir:     "/Users/glenn",
		WorkDir:     "/Users/glenn/rowan-working",
		BinDir:      "/Users/glenn/rowan-working/bin",
		LogDir:      "/Users/glenn/rowan-working/tmp/keeper-logs",
		LabelPrefix: "com.mas-bandwidth.nova-",
		PlistDir:    "/Users/glenn/Library/LaunchAgents",
	}

	units := DefaultKeeperUnits(cfg)
	if len(units) != 4 {
		t.Fatalf("expected 4 keeper units, got %d", len(units))
	}

	names := make(map[string]KeeperUnit)
	for _, u := range units {
		names[u.Name] = u
		if !u.KeepAlive {
			t.Errorf("unit %s must have KeepAlive=true", u.Name)
		}
		if !u.RunAtLoad {
			t.Errorf("unit %s must have RunAtLoad=true", u.Name)
		}
		expectedLabel := "com.mas-bandwidth.nova-" + u.Name
		if u.Label != expectedLabel {
			t.Errorf("unit %s label = %q, want %q", u.Name, u.Label, expectedLabel)
		}
		if len(u.ProgramArguments) < 2 {
			t.Errorf("unit %s has empty or short ProgramArguments: %v", u.Name, u.ProgramArguments)
		} else {
			if u.ProgramArguments[0] != "/bin/bash" {
				t.Errorf("unit %s ProgramArguments[0] = %q, want /bin/bash (never zsh)", u.Name, u.ProgramArguments[0])
			}
			if u.ProgramArguments[1] != "-c" {
				t.Errorf("unit %s ProgramArguments[1] = %q, want -c", u.Name, u.ProgramArguments[1])
			}
			for _, arg := range u.ProgramArguments {
				if strings.Contains(arg, "zsh") {
					t.Errorf("unit %s ProgramArguments contains forbidden zsh: %v", u.Name, u.ProgramArguments)
				}
			}
		}
		if u.WorkingDirectory != cfg.WorkDir {
			t.Errorf("unit %s WorkingDirectory = %q, want %q", u.Name, u.WorkingDirectory, cfg.WorkDir)
		}
		if !strings.HasPrefix(u.StandardOutPath, cfg.LogDir) {
			t.Errorf("unit %s StandardOutPath = %q, want prefix %q", u.Name, u.StandardOutPath, cfg.LogDir)
		}
	}

	for _, expectedName := range []string{"dealer", "backpressure", "harvest", "sprint"} {
		if _, ok := names[expectedName]; !ok {
			t.Errorf("missing expected unit %q", expectedName)
		}
	}
}

func TestKeeperUnitsPlistXMLValidation(t *testing.T) {
	cfg := KeeperLaunchdConfig{
		HomeDir:     "/Users/glenn",
		WorkDir:     "/Users/glenn/rowan-working",
		BinDir:      "/Users/glenn/rowan-working/bin",
		LogDir:      "/Users/glenn/rowan-working/tmp/keeper-logs",
		LabelPrefix: "com.mas-bandwidth.nova-",
	}

	units := DefaultKeeperUnits(cfg)
	if len(units) == 0 {
		t.Fatal("no units returned")
	}

	hasPlutil := false
	if runtime.GOOS == "darwin" {
		if _, err := exec.LookPath("plutil"); err == nil {
			hasPlutil = true
		}
	}

	for _, u := range units {
		xmlContent := u.PlistXML()
		if !strings.Contains(xmlContent, "<!DOCTYPE plist PUBLIC \"-//Apple//DTD PLIST 1.0//EN\"") {
			t.Errorf("unit %s xml missing DOCTYPE plist header: %s", u.Name, xmlContent)
		}
		if !strings.Contains(xmlContent, "<key>KeepAlive</key>\n\t<true/>") && !strings.Contains(xmlContent, "<key>KeepAlive</key><true/>") {
			t.Errorf("unit %s xml missing KeepAlive true: %s", u.Name, xmlContent)
		}
		if !strings.Contains(xmlContent, "<key>RunAtLoad</key>\n\t<true/>") && !strings.Contains(xmlContent, "<key>RunAtLoad</key><true/>") {
			t.Errorf("unit %s xml missing RunAtLoad true: %s", u.Name, xmlContent)
		}
		if !strings.Contains(xmlContent, "<key>Label</key>\n\t<string>"+u.Label+"</string>") && !strings.Contains(xmlContent, "<key>Label</key><string>"+u.Label+"</string>") {
			t.Errorf("unit %s xml missing Label %s: %s", u.Name, u.Label, xmlContent)
		}
		if strings.Contains(xmlContent, "zsh") {
			t.Errorf("unit %s xml contains forbidden zsh: %s", u.Name, xmlContent)
		}
		if !strings.Contains(xmlContent, "<string>/bin/bash</string>") {
			t.Errorf("unit %s xml missing <string>/bin/bash</string>: %s", u.Name, xmlContent)
		}

		if hasPlutil {
			tmpFile := filepath.Join(t.TempDir(), u.PlistFileName())
			if err := os.WriteFile(tmpFile, []byte(xmlContent), 0o644); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command("plutil", "-lint", tmpFile)
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Errorf("plutil -lint failed for %s: %v, output:\n%s", u.Name, err, string(out))
			}
		}
	}
}

func TestKeeperGenerateAndInstall(t *testing.T) {
	tmpDir := t.TempDir()
	logDir := filepath.Join(tmpDir, "logs")
	plistDir := filepath.Join(tmpDir, "LaunchAgents")
	cfg := KeeperLaunchdConfig{
		HomeDir:     tmpDir,
		WorkDir:     tmpDir,
		BinDir:      filepath.Join(tmpDir, "bin"),
		LogDir:      logDir,
		LabelPrefix: "com.mas-bandwidth.nova-",
		PlistDir:    plistDir,
	}

	fakeRunner := &fakeLaunchctlRunner{}
	var out, errb bytes.Buffer
	code := FleetKeeper(FleetKeeperInput{
		Action: "install",
		Unit:   "all",
		Config: cfg,
		Runner: fakeRunner,
		Stdout: &out,
		Stderr: &errb,
	})

	if code != 0 {
		t.Fatalf("FleetKeeper install exit = %d, want 0; err=%s", code, errb.String())
	}

	if len(fakeRunner.loads) != 4 {
		t.Fatalf("expected 4 loads, got %d", len(fakeRunner.loads))
	}

	for _, unitName := range []string{"dealer", "backpressure", "harvest", "sprint"} {
		plistFile := filepath.Join(plistDir, "com.mas-bandwidth.nova-"+unitName+".plist")
		if _, err := os.Stat(plistFile); err != nil {
			t.Errorf("plist file not created for %s at %s", unitName, plistFile)
		}
	}

	outStr := out.String()
	for _, unitName := range []string{"dealer", "backpressure", "harvest", "sprint"} {
		if !strings.Contains(outStr, "KEEPER INSTALL unit="+unitName) {
			t.Errorf("install output missing line for %s:\n%s", unitName, outStr)
		}
	}
}

func TestKeeperStatusAndUnload(t *testing.T) {
	tmpDir := t.TempDir()
	plistDir := filepath.Join(tmpDir, "LaunchAgents")
	cfg := KeeperLaunchdConfig{
		HomeDir:     tmpDir,
		WorkDir:     tmpDir,
		BinDir:      filepath.Join(tmpDir, "bin"),
		LogDir:      filepath.Join(tmpDir, "logs"),
		LabelPrefix: "com.mas-bandwidth.nova-",
		PlistDir:    plistDir,
	}

	fakeRunner := &fakeLaunchctlRunner{}
	var out, errb bytes.Buffer
	code := FleetKeeper(FleetKeeperInput{
		Action: "status",
		Unit:   "dealer",
		Config: cfg,
		Runner: fakeRunner,
		Stdout: &out,
		Stderr: &errb,
	})

	if code != 0 {
		t.Fatalf("status exit = %d, want 0", code)
	}
	if !strings.Contains(out.String(), "KEEPER STATUS unit=dealer") {
		t.Errorf("status output missing dealer status:\n%s", out.String())
	}

	out.Reset()
	code = FleetKeeper(FleetKeeperInput{
		Action: "unload",
		Unit:   "dealer",
		Config: cfg,
		Runner: fakeRunner,
		Stdout: &out,
		Stderr: &errb,
	})
	if code != 0 {
		t.Fatalf("unload exit = %d, want 0", code)
	}
	if len(fakeRunner.unloads) != 1 {
		t.Fatalf("expected 1 unload, got %d", len(fakeRunner.unloads))
	}
	if !strings.Contains(out.String(), "KEEPER UNLOAD unit=dealer") {
		t.Errorf("unload output missing dealer unload:\n%s", out.String())
	}
}

func TestKeeperRefusesUnknownUnit(t *testing.T) {
	var out, errb bytes.Buffer
	code := FleetKeeper(FleetKeeperInput{
		Action: "inspect",
		Unit:   "unknown-engine",
		Stdout: &out,
		Stderr: &errb,
	})
	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if !strings.Contains(errb.String(), "unknown unit \"unknown-engine\"") {
		t.Errorf("stderr does not name unknown unit:\n%s", errb.String())
	}
}
