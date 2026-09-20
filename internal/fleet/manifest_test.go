package fleet_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/fleet"
)

func TestDefaultManifestForBenchNode(t *testing.T) {
	m := fleet.Machine{
		Name:  "hulk",
		SSH:   "hulk",
		OS:    "linux",
		Arch:  "x64",
		Roles: []string{fleet.RoleBench, fleet.RoleRunner},
		Seat:  "swarm-hulk",
		Cores: 64,
	}

	manifest := fleet.DefaultManifestForNode(m)
	if manifest.GoVersion != fleet.StandardGoVersion {
		t.Fatalf("GoVersion = %q, want %q", manifest.GoVersion, fleet.StandardGoVersion)
	}
	if manifest.Seat != "swarm-hulk" {
		t.Fatalf("Seat = %q, want swarm-hulk", manifest.Seat)
	}

	// Verify all 16 required tools are present
	requiredTools := []string{
		"go", "sbcl", "cargo", "rustc", "dotnet", "elixir", "erl", "node",
		"javac", "dart", "cc", "c++", "make", "cmake", "git", "sqlite3",
	}
	for _, tool := range requiredTools {
		req, ok := manifest.Tools[tool]
		if !ok {
			t.Errorf("manifest missing tool %q", tool)
		} else if !req.Required {
			t.Errorf("tool %q should be marked required", tool)
		}
	}

	// Verify required paths on Linux
	pathFound := map[string]bool{}
	for _, p := range manifest.Paths {
		pathFound[p.Path] = true
	}
	for _, wantPath := range []string{"sdk", "go/pkg/mod", "sdk/env.sh", "/tmp/.dotnet"} {
		if !pathFound[wantPath] {
			t.Errorf("manifest missing path %q", wantPath)
		}
	}
}

func TestFixManifestDiscrepancies(t *testing.T) {
	m := fleet.Machine{
		Name:  "vision",
		SSH:   "vision",
		OS:    "linux",
		Arch:  "x64",
		Roles: []string{fleet.RoleBench},
		Seat:  "swarm-vision",
		Cores: 64,
	}

	// Create an incomplete/outdated manifest missing tools and paths, and with obsolete go version
	partial := &fleet.NodeManifest{
		NodeName:  "vision",
		OS:        "linux",
		Arch:      "x64",
		Roles:     []string{fleet.RoleBench},
		GoVersion: "go1.26.5", // Outdated Go version
		Seat:      "wrong-seat",
		Tools: map[string]fleet.ToolRequirement{
			"go":   {Name: "go", Pattern: "go1.26.5", Required: true},
			"sbcl": {Name: "sbcl", Pattern: "-", Required: true},
			// missing cargo, rustc, dotnet, sqlite3, dart, etc.
		},
		Paths: []fleet.PathRequirement{
			{Path: "sdk", IsDir: true, Required: true},
			// missing go/pkg/mod, sdk/env.sh, /tmp/.dotnet
		},
	}

	repairs := fleet.FixManifestDiscrepancies(m, partial)
	if len(repairs) == 0 {
		t.Fatal("expected repairs for incomplete manifest, got none")
	}

	// Verify Go version updated to go1.26.6
	if partial.GoVersion != fleet.StandardGoVersion {
		t.Fatalf("GoVersion was not updated: %q", partial.GoVersion)
	}

	// Verify seat fixed
	if partial.Seat != "swarm-vision" {
		t.Fatalf("Seat was not corrected: %q", partial.Seat)
	}

	// Verify all required tools now present
	for _, tool := range []string{"cargo", "rustc", "dotnet", "sqlite3", "dart", "elixir", "erl", "node", "javac", "cc", "c++", "make", "cmake", "git"} {
		if _, ok := partial.Tools[tool]; !ok {
			t.Errorf("tool %q was not restored by repair", tool)
		}
	}

	// Verify paths now present
	pathSet := map[string]bool{}
	for _, p := range partial.Paths {
		pathSet[p.Path] = true
	}
	for _, wantPath := range []string{"sdk", "go/pkg/mod", "sdk/env.sh", "/tmp/.dotnet"} {
		if !pathSet[wantPath] {
			t.Errorf("path %q was not restored by repair", wantPath)
		}
	}
}

func TestValidateNodeManifest_OK(t *testing.T) {
	m := fleet.Machine{
		Name:  "space",
		SSH:   "space",
		OS:    "linux",
		Arch:  "x64",
		Roles: []string{fleet.RoleBench, fleet.RoleServices},
		Seat:  "swarm-space",
		Cores: 32,
	}

	manifest := fleet.DefaultManifestForNode(m)
	state := fleet.NodeState{
		Name:   "space",
		OS:     "linux",
		Arch:   "x64",
		Status: "OK",
		ToolVersions: map[string]string{
			"go":      "go version go1.26.6 linux/amd64",
			"sbcl":    "SBCL 2.5.8",
			"cargo":   "cargo 1.98.1",
			"rustc":   "rustc 1.98.1",
			"dotnet":  "10.0.100",
			"elixir":  "Elixir 1.20.4",
			"erl":     "29",
			"node":    "v26.0.0",
			"javac":   "javac 21.0.2",
			"dart":    "Dart SDK version: 3.13.2",
			"cc":      "cc (Ubuntu 13.2.0-23ubuntu4) 13.2.0",
			"c++":     "c++ (Ubuntu 13.2.0-23ubuntu4) 13.2.0",
			"make":    "GNU Make 4.3",
			"cmake":   "cmake version 3.28.3",
			"git":     "git version 2.43.0",
			"sqlite3": "3.45.1 2024-01-30",
		},
		PathsPresent: map[string]bool{
			"sdk":                                 true,
			"go/pkg/mod":                          true,
			"sdk/env.sh":                          true,
			"/tmp/.dotnet":                        true,
			".config/nova-secrets/swarm-space.key": true,
		},
		PathModes: map[string]string{
			"/tmp/.dotnet": "1777",
		},
		FreeGB:   50,
		SeatKeys: []string{"swarm-space.key"},
	}

	result := fleet.ValidateNodeManifest(manifest, state)
	if result.Status != "OK" {
		t.Fatalf("expected status OK, got %s (discrepancies: %v)", result.Status, result.Discrepancies)
	}
	if len(result.Discrepancies) != 0 {
		t.Fatalf("expected no discrepancies, got %v", result.Discrepancies)
	}
}

func TestValidateNodeManifest_DRIFT(t *testing.T) {
	m := fleet.Machine{
		Name:  "batman",
		SSH:   "batman",
		OS:    "darwin",
		Arch:  "amd64",
		Roles: []string{fleet.RoleRunner},
		Cores: 8,
	}

	manifest := fleet.DefaultManifestForNode(m)
	state := fleet.NodeState{
		Name:   "batman",
		OS:     "darwin",
		Arch:   "amd64",
		Status: "OK",
		ToolVersions: map[string]string{
			"go":   "go version go1.25.5 darwin/amd64", // Outdated version: want go1.26.6
			"git":  "git version 2.39.5",
			"make": "GNU Make 3.81",
			"cc":   "Apple clang version 15.0.0",
			"c++":  "Apple clang version 15.0.0",
		},
		ShadowedTools: map[string]string{
			"go": "/usr/local/bin/go", // Shadowing Homebrew go
		},
	}

	result := fleet.ValidateNodeManifest(manifest, state)
	if result.Status != "DRIFT" {
		t.Fatalf("expected status DRIFT, got %s", result.Status)
	}
	// Verify version drift and shadowing detected
	foundVersionDrift, foundShadow := false, false
	for _, d := range result.Discrepancies {
		if strings.Contains(d, "version") && strings.Contains(d, "go1.25.5") {
			foundVersionDrift = true
		}
		if strings.Contains(d, "shadowed") {
			foundShadow = true
		}
	}
	if !foundVersionDrift {
		t.Errorf("expected version drift in discrepancies, got %v", result.Discrepancies)
	}
	if !foundShadow {
		t.Errorf("expected shadow warning in discrepancies, got %v", result.Discrepancies)
	}
}

func TestValidateFleetMultiNodeReporting(t *testing.T) {
	reg, err := fleet.ReadRegistry(filepath.Join("testdata", "machines.tsv"))
	if err != nil {
		t.Fatalf("ReadRegistry: %v", err)
	}

	// Prepare mock node states for multi-node reporting:
	// - studio: OK
	// - hulk: OK
	// - vision: OK
	// - threadripper-wsl: OK
	// - space: OK
	// - mini: OK
	// - batman: DRIFT (shadowed sqlite3 and wrong go)
	// - superman: DOWN (unreachable)
	states := map[string]fleet.NodeState{
		"studio": {
			Name:   "studio",
			OS:     "darwin",
			Arch:   "arm64",
			Status: "OK",
			ToolVersions: map[string]string{
				"go": "go version go1.26.6 darwin/arm64", "git": "git version 2.45.0",
				"make": "GNU Make 3.81", "cc": "Apple clang 15", "c++": "Apple clang 15", "sbcl": "SBCL 2.5.8",
			},
			PathsPresent: map[string]bool{".config/nova-secrets/studio.key": true},
			SeatKeys:     []string{"studio.key"},
		},
		"hulk": {
			Name:   "hulk",
			OS:     "linux",
			Arch:   "x64",
			Status: "OK",
			ToolVersions: map[string]string{
				"go": "go version go1.26.6 linux/amd64", "sbcl": "SBCL 2.5.8",
				"cargo": "cargo 1.98.1", "rustc": "rustc 1.98.1", "dotnet": "10.0.100",
				"elixir": "Elixir 1.20.4", "erl": "29", "node": "v26.0.0", "javac": "javac 21.0.2",
				"dart": "Dart SDK 3.13.2", "cc": "cc 13.2", "c++": "c++ 13.2", "make": "Make 4.3",
				"cmake": "cmake 3.28", "git": "git 2.43", "sqlite3": "sqlite 3.45",
			},
			PathsPresent: map[string]bool{
				"sdk": true, "go/pkg/mod": true, "sdk/env.sh": true, "/tmp/.dotnet": true,
				".config/nova-secrets/swarm-hulk.key": true,
			},
			PathModes: map[string]string{"/tmp/.dotnet": "1777"},
			SeatKeys:  []string{"swarm-hulk.key"},
			FreeGB:    50,
		},
		"vision": {
			Name:   "vision",
			OS:     "linux",
			Arch:   "x64",
			Status: "OK",
			ToolVersions: map[string]string{
				"go": "go version go1.26.6 linux/amd64", "sbcl": "SBCL 2.5.8",
				"cargo": "cargo 1.98.1", "rustc": "rustc 1.98.1", "dotnet": "10.0.100",
				"elixir": "Elixir 1.20.4", "erl": "29", "node": "v26.0.0", "javac": "javac 21.0.2",
				"dart": "Dart SDK 3.13.2", "cc": "cc 13.2", "c++": "c++ 13.2", "make": "Make 4.3",
				"cmake": "cmake 3.28", "git": "git 2.43", "sqlite3": "sqlite 3.45",
			},
			PathsPresent: map[string]bool{
				"sdk": true, "go/pkg/mod": true, "sdk/env.sh": true, "/tmp/.dotnet": true,
				".config/nova-secrets/swarm-vision.key": true,
			},
			PathModes: map[string]string{"/tmp/.dotnet": "0777"},
			SeatKeys:  []string{"swarm-vision.key"},
			FreeGB:    40,
		},
		"threadripper-wsl": {
			Name:   "threadripper-wsl",
			OS:     "linux",
			Arch:   "x64",
			Status: "OK",
			ToolVersions: map[string]string{
				"go": "go version go1.26.6 linux/amd64", "sbcl": "SBCL 2.5.8",
				"cargo": "cargo 1.98.1", "rustc": "rustc 1.98.1", "dotnet": "10.0.100",
				"elixir": "Elixir 1.20.4", "erl": "29", "node": "v26.0.0", "javac": "javac 21.0.2",
				"dart": "Dart SDK 3.13.2", "cc": "cc 13.2", "c++": "c++ 13.2", "make": "Make 4.3",
				"cmake": "cmake 3.28", "git": "git 2.43", "sqlite3": "sqlite 3.45",
			},
			PathsPresent: map[string]bool{
				"sdk": true, "go/pkg/mod": true, "sdk/env.sh": true, "/tmp/.dotnet": true,
				".config/nova-secrets/swarm-threadripper.key": true,
			},
			PathModes: map[string]string{"/tmp/.dotnet": "1777"},
			SeatKeys:  []string{"swarm-threadripper.key"},
			FreeGB:    60,
		},
		"space": {
			Name:   "space",
			OS:     "linux",
			Arch:   "x64",
			Status: "OK",
			ToolVersions: map[string]string{
				"go": "go version go1.26.6 linux/amd64", "sbcl": "SBCL 2.5.8",
				"cargo": "cargo 1.98.1", "rustc": "rustc 1.98.1", "dotnet": "10.0.100",
				"elixir": "Elixir 1.20.4", "erl": "29", "node": "v26.0.0", "javac": "javac 21.0.2",
				"dart": "Dart SDK 3.13.2", "cc": "cc 13.2", "c++": "c++ 13.2", "make": "Make 4.3",
				"cmake": "cmake 3.28", "git": "git 2.43", "sqlite3": "sqlite 3.45",
			},
			PathsPresent: map[string]bool{
				"sdk": true, "go/pkg/mod": true, "sdk/env.sh": true, "/tmp/.dotnet": true,
				".config/nova-secrets/swarm-space.key": true,
			},
			PathModes: map[string]string{"/tmp/.dotnet": "1777"},
			SeatKeys:  []string{"swarm-space.key"},
			FreeGB:    30,
		},
		"mini": {
			Name:   "mini",
			OS:     "linux",
			Arch:   "x64",
			Status: "OK",
			ToolVersions: map[string]string{
				"go": "go version go1.26.6 linux/amd64", "git": "git 2.43",
				"make": "Make 4.3", "cc": "cc 13.2", "c++": "c++ 13.2",
			},
			PathsPresent: map[string]bool{"sdk": true, "go/pkg/mod": true, "sdk/env.sh": true},
		},
		"batman": {
			Name:   "batman",
			OS:     "darwin",
			Arch:   "amd64",
			Status: "OK",
			ToolVersions: map[string]string{
				"go": "go version go1.25.5 darwin/amd64", // DRIFT
				"git": "git 2.39", "make": "Make 3.81", "cc": "clang 15", "c++": "clang 15",
			},
			ShadowedTools: map[string]string{"go": "/usr/local/bin/go"},
		},
		"superman": {
			Name:   "superman",
			OS:     "darwin",
			Arch:   "amd64",
			Status: "DOWN",
			Error:  "connection timed out (port 22)",
		},
	}

	report := fleet.ValidateFleet(reg, states)

	if report.Total != 8 {
		t.Fatalf("report.Total = %d, want 8", report.Total)
	}
	if report.OK != 6 {
		t.Fatalf("report.OK = %d, want 6", report.OK)
	}
	if report.Drift != 1 {
		t.Fatalf("report.Drift = %d, want 1", report.Drift)
	}
	if report.Down != 1 {
		t.Fatalf("report.Down = %d, want 1", report.Down)
	}

	// Exit code should be 3 when any node is DOWN
	if code := report.ExitCode(); code != 3 {
		t.Fatalf("report.ExitCode() = %d, want 3", code)
	}

	table := report.Table()
	// Check that table has column headers and named cells
	for _, expectedHeader := range []string{"NODE", "OS/ARCH", "ROLES", "STATUS", "DETAILS"} {
		if !strings.Contains(table, expectedHeader) {
			t.Errorf("table output missing header %q:\n%s", expectedHeader, table)
		}
	}

	// Issue #2054 requirement: DOWN as a named cell in the table!
	if !strings.Contains(table, "DOWN") {
		t.Errorf("table output missing DOWN status cell:\n%s", table)
	}
	if !strings.Contains(table, "DRIFT") {
		t.Errorf("table output missing DRIFT status cell:\n%s", table)
	}
	if !strings.Contains(table, "OK") {
		t.Errorf("table output missing OK status cell:\n%s", table)
	}

	summary := report.Summary()
	if !strings.Contains(summary, "FLEET STANDARD DRIFT") {
		t.Errorf("summary missing 'FLEET STANDARD DRIFT': %s", summary)
	}
	if !strings.Contains(summary, "drift=1 down=1") {
		t.Errorf("summary missing 'drift=1 down=1': %s", summary)
	}
}
