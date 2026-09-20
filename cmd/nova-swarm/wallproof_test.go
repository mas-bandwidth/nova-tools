package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestInWallTheProductionArgvBuildsAndLinks is THE PROOF, and it is deliberately not a unit
// test: it takes the argv the runner itself builds -- nativeSandboxArgv, the production
// path, with no grant added by hand -- keeps every wall flag, and swaps only the command
// after `--` for a one-file build. Anything a hand-written nova-sandbox line would prove is
// a proof about the hand-written line.
//
// It is skipped unless NOVA_WALL_PROOF=1, because it needs a bench provisioned to the
// standard (a toolchain under ~/sdk, and /tmp/.dotnet for the cs leg). RED on origin/dev and
// GREEN on this branch, run on hulk and on batman/superman, 2026-09-20.
func TestInWallTheProductionArgvBuildsAndLinks(t *testing.T) {
	if os.Getenv("NOVA_WALL_PROOF") == "" {
		t.Skip("the in-wall proof runs on a provisioned bench: NOVA_WALL_PROOF=1 go test ./cmd/nova-swarm/ -run TestInWallTheProductionArgv")
	}
	wall, err := exec.LookPath("nova-sandbox")
	if err != nil {
		t.Skipf("no nova-sandbox on PATH: %v", err)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	legs := []struct {
		name string
		os   string
		file string
		body string
		sh   string
		tok  string
	}{
		{name: "cs", file: "Program.cs", body: "System.Console.WriteLine(\"WALL-CS-OK\");\n",
			sh: "dotnet build p.csproj", tok: "Build succeeded"},
		{name: "cc", os: "darwin", file: "a.c", body: "#include <stdio.h>\nint main(void){puts(\"WALL-C-OK\");return 0;}\n",
			sh: "cc -std=c99 -Wall -Werror a.c -o a && ./a", tok: "WALL-C-OK"},
	}
	for _, leg := range legs {
		if leg.os != "" && leg.os != runtime.GOOS {
			continue
		}
		t.Run(leg.name, func(t *testing.T) {
			bin := nativeHarness(t)
			_, slot := aSlot(t)
			jobDir := filepath.Join(slot, "jobs", "wall-proof")
			dataHome := filepath.Join(slot, "data")
			tmpDir := filepath.Join(slot, "tmp", "wall-proof")
			for _, d := range []string{jobDir, dataHome, tmpDir} {
				if err := os.MkdirAll(d, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.WriteFile(filepath.Join(jobDir, leg.file), []byte(leg.body), 0o644); err != nil {
				t.Fatal(err)
			}
			if leg.name == "cs" {
				csproj := `<Project Sdk="Microsoft.NET.Sdk"><PropertyGroup><OutputType>Exe</OutputType><TargetFramework>net10.0</TargetFramework></PropertyGroup></Project>`
				if err := os.WriteFile(filepath.Join(jobDir, "p.csproj"), []byte(csproj), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			cfg := nativeRunConfig{slotDir: slot, benchHome: home, benchOS: runtime.GOOS, noSharedCaches: true}
			argv := nativeSandboxArgv(bin, cfg, dataHome, jobDir, tmpDir)
			cut := -1
			for i, a := range argv {
				if a == "--" {
					cut = i
					break
				}
			}
			if cut < 0 {
				t.Fatalf("the production argv carries no --:\n%s", strings.Join(argv, " "))
			}
			// The wall's own flags, verbatim, and then OUR command instead of the harness.
			// The settings a cs card would set for itself go through `env`; everything that
			// is the RUNNER's to decide -- HOME, TMPDIR, PATH, and on darwin DEVELOPER_DIR,
			// which is what replaces a /var/db read root -- comes from nativeChildEnv below,
			// the same function the real run uses. Nothing here is a grant typed by hand,
			// which is the whole point: this file compiles on the commit before the change
			// too, so its RED there is this test and not a different one.
			run := append(append([]string{}, argv[:cut+1]...), "env",
				"DOTNET_CLI_HOME="+filepath.Join(home, "sdk", "dotnet-home"),
				"DOTNET_CLI_TELEMETRY_OPTOUT=1", "DOTNET_NOLOGO=1",
				"NUGET_PACKAGES="+filepath.Join(dataHome, ".nuget", "packages"),
				"sh", "-c", leg.sh)
			cmd := exec.Command(wall, run...)
			cmd.Env = nativeChildEnv(dataHome, jobDir, tmpDir, "", "", "", "")
			out, _ := cmd.CombinedOutput()
			if !strings.Contains(string(out), leg.tok) {
				t.Errorf("the %s leg did not %s inside the wall built by the production argv.\nargv: %s\noutput:\n%s",
					leg.name, leg.tok, strings.Join(run, " "), out)
			}
		})
	}
}
