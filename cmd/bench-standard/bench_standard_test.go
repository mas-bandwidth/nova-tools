package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/fleet"
)

type fakeNodeRunner struct {
	answers map[string]string
	errs    map[string]error
}

func (r fakeNodeRunner) Run(ctx context.Context, target, script string) (string, error) {
	if err, ok := r.errs[target]; ok {
		return "", err
	}
	if ans, ok := r.answers[target]; ok {
		return ans, nil
	}
	return "STANDARD OK go=go1.26.6 bins=test seats=1 free=50G toolchains=all-legs\n", nil
}

func TestBenchStandardHelp(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"--help"}, &stdout, &stderr, defaultNewRunner)
	if code != 2 {
		t.Fatalf("help exit code = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "bench-standard") {
		t.Fatalf("help output missing command name:\n%s", stderr.String())
	}
}

func TestBenchStandardFleetAllOK(t *testing.T) {
	machinesPath := filepath.Join("..", "..", "internal", "fleet", "testdata", "machines.tsv")
	var stdout, stderr bytes.Buffer

	fakeRunner := func(program string) nodeRunner {
		return fakeNodeRunner{
			answers: map[string]string{
				"hulk":             "STANDARD OK go=go1.26.6 bins=test seats=1 free=50G toolchains=all-legs\n",
				"vision":           "STANDARD OK go=go1.26.6 bins=test seats=1 free=50G toolchains=all-legs\n",
				"threadripper-wsl": "STANDARD OK go=go1.26.6 bins=test seats=1 free=50G toolchains=all-legs\n",
				"space":            "STANDARD OK go=go1.26.6 bins=test seats=1 free=50G toolchains=all-legs\n",
				"studio":           "STANDARD OK go=go1.26.6 bins=test seats=1 free=50G\n",
				"mini":             "STANDARD OK go=go1.26.6 bins=test seats=1 free=50G\n",
				"batman":           "STANDARD OK go=go1.26.6 bins=test seats=1 free=50G\n",
				"superman":         "STANDARD OK go=go1.26.6 bins=test seats=1 free=50G\n",
			},
		}
	}

	code := run([]string{"--fleet", "--machines", machinesPath}, &stdout, &stderr, fakeRunner)
	if code != 0 {
		t.Fatalf("run exit code = %d, want 0\nstderr: %s\nstdout: %s", code, stderr.String(), stdout.String())
	}

	out := stdout.String()
	if !strings.Contains(out, "FLEET STANDARD OK") {
		t.Errorf("stdout missing FLEET STANDARD OK:\n%s", out)
	}
	if !strings.Contains(out, "hulk") || !strings.Contains(out, "vision") {
		t.Errorf("stdout missing node rows:\n%s", out)
	}
}

func TestBenchStandardFleetWithDriftAndDown(t *testing.T) {
	machinesPath := filepath.Join("..", "..", "internal", "fleet", "testdata", "machines.tsv")
	var stdout, stderr bytes.Buffer

	tmpDir := t.TempDir()
	outFile := filepath.Join(tmpDir, "fleet-standard-result.txt")

	fakeRunner := func(program string) nodeRunner {
		return fakeNodeRunner{
			answers: map[string]string{
				"hulk":             "STANDARD OK go=go1.26.6 bins=test seats=1 free=50G toolchains=all-legs\n",
				"vision":           "STANDARD OK go=go1.26.6 bins=test seats=1 free=50G toolchains=all-legs\n",
				"threadripper-wsl": "STANDARD OK go=go1.26.6 bins=test seats=1 free=50G toolchains=all-legs\n",
				"space":            "STANDARD OK go=go1.26.6 bins=test seats=1 free=50G toolchains=all-legs\n",
				"studio":           "STANDARD OK go=go1.26.6 bins=test seats=1 free=50G\n",
				"mini":             "STANDARD OK go=go1.26.6 bins=test seats=1 free=50G\n",
				"batman": "DRIFT toolchain go: go is [go version go1.25.5] want go1.26.6\n" +
					"DRIFT /usr/local/bin/go shadows /Users/glenn/sdk/bin/go on PATH\n" +
					"STANDARD DRIFT (see lines above)\n",
			},
			errs: map[string]error{
				"superman": errors.New("ssh: connect to host superman port 22: Connection refused"),
			},
		}
	}

	code := run([]string{
		"--fleet",
		"--machines", machinesPath,
		"--out", outFile,
	}, &stdout, &stderr, fakeRunner)

	// DOWN nodes must cause exit code 3
	if code != 3 {
		t.Fatalf("run exit code = %d, want 3 for unreachable node", code)
	}

	out := stdout.String()

	// Issue #2054 requirement: prints one table with DOWN as a named cell!
	if !strings.Contains(out, "DOWN") {
		t.Errorf("stdout missing named cell DOWN:\n%s", out)
	}
	if !strings.Contains(out, "DRIFT") {
		t.Errorf("stdout missing named cell DRIFT:\n%s", out)
	}
	if !strings.Contains(out, "OK") {
		t.Errorf("stdout missing named cell OK:\n%s", out)
	}
	if !strings.Contains(out, "FLEET STANDARD DRIFT") {
		t.Errorf("stdout missing summary line FLEET STANDARD DRIFT:\n%s", out)
	}

	// Verify outFile written beside fleet-state
	written, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatalf("failed to read --out file: %v", err)
	}
	if !strings.Contains(string(written), "DOWN") {
		t.Errorf("--out file missing DOWN content:\n%s", string(written))
	}
}

func TestBenchStandardFleetJSONOutput(t *testing.T) {
	machinesPath := filepath.Join("..", "..", "internal", "fleet", "testdata", "machines.tsv")
	var stdout, stderr bytes.Buffer

	fakeRunner := func(program string) nodeRunner {
		return fakeNodeRunner{
			answers: map[string]string{
				"hulk":             "STANDARD OK go=go1.26.6 bins=test seats=1 free=50G toolchains=all-legs\n",
				"vision":           "STANDARD OK go=go1.26.6 bins=test seats=1 free=50G toolchains=all-legs\n",
				"threadripper-wsl": "STANDARD OK go=go1.26.6 bins=test seats=1 free=50G toolchains=all-legs\n",
				"space":            "STANDARD OK go=go1.26.6 bins=test seats=1 free=50G toolchains=all-legs\n",
				"studio":           "STANDARD OK go=go1.26.6 bins=test seats=1 free=50G\n",
				"mini":             "STANDARD OK go=go1.26.6 bins=test seats=1 free=50G\n",
				"batman":           "STANDARD OK go=go1.26.6 bins=test seats=1 free=50G\n",
				"superman":         "STANDARD OK go=go1.26.6 bins=test seats=1 free=50G\n",
			},
		}
	}

	code := run([]string{"--fleet", "--machines", machinesPath, "--json"}, &stdout, &stderr, fakeRunner)
	if code != 0 {
		t.Fatalf("run exit code = %d, want 0\nstderr: %s", code, stderr.String())
	}

	var report fleet.FleetValidationReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("failed to unmarshal JSON output: %v\noutput:\n%s", err, stdout.String())
	}

	if report.Total != 8 || report.OK != 8 {
		t.Fatalf("report total=%d ok=%d, want 8/8", report.Total, report.OK)
	}
}

func TestBenchStandardSingleBenchFilter(t *testing.T) {
	machinesPath := filepath.Join("..", "..", "internal", "fleet", "testdata", "machines.tsv")
	var stdout, stderr bytes.Buffer

	fakeRunner := func(program string) nodeRunner {
		return fakeNodeRunner{
			answers: map[string]string{
				"hulk": "STANDARD OK go=go1.26.6 bins=test seats=1 free=50G toolchains=all-legs\n",
			},
		}
	}

	code := run([]string{"--fleet", "--machines", machinesPath, "--bench", "hulk"}, &stdout, &stderr, fakeRunner)
	if code != 0 {
		t.Fatalf("run exit code = %d, want 0\nstderr: %s", code, stderr.String())
	}

	out := stdout.String()
	if !strings.Contains(out, "hulk") {
		t.Errorf("output missing hulk:\n%s", out)
	}
	if strings.Contains(out, "vision") {
		t.Errorf("output should not contain vision when filtered to hulk:\n%s", out)
	}
}
