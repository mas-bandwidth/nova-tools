//go:build functional

package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// csharp_functional_test.go compiles the C# that the role's PowerShell scripts hand to Add-Type, with a test program
// from testdata, at the language version Windows PowerShell 5.1 compiles (C# 5) and with warnings as errors, and runs
// it on fake inputs: the security decisions are tested without Windows. It needs the dotnet SDK (AGENTS.md, functional
// prerequisites) and skips without it, except on Linux under NOVA_CONTRACT_STRICT=1 (CI's Linux functional job, on
// benches whose runner-packages list carries dotnet-sdk-10.0), where it fails.

// csharpParts: the C# of every Add-Type -TypeDefinition here-string in the script.
func csharpParts(t *testing.T, script string) []string {
	t.Helper()
	b, err := os.ReadFile(script)
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	var out []string
	const open = "-TypeDefinition @'\n"
	for {
		i := strings.Index(s, open)
		if i < 0 {
			break
		}
		s = s[i+len(open):]
		j := strings.Index(s, "\n'@")
		if j < 0 {
			t.Fatalf("%s: a here-string with no end", script)
		}
		out = append(out, s[:j]+"\n")
		s = s[j:]
	}
	if len(out) == 0 {
		t.Fatalf("%s: no C#", script)
	}
	return out
}

const csharpProject = `<Project Sdk="Microsoft.NET.Sdk">
  <PropertyGroup>
    <TargetFramework>net%s</TargetFramework>
    <OutputType>Exe</OutputType>
    <LangVersion>5</LangVersion>
    <ImplicitUsings>disable</ImplicitUsings>
    <Nullable>disable</Nullable>
    <TreatWarningsAsErrors>true</TreatWarningsAsErrors>
    <NoWarn>CA1416</NoWarn>
  </PropertyGroup>
</Project>
`

// runCSharp builds the C# of scripts with program (a testdata path) in a temp directory and runs it with args; it
// fails the test unless the program exits 0 and prints a line "ok". The program prints a FAIL line for each failed case.
func runCSharp(t *testing.T, program string, scripts []string, args ...string) {
	t.Helper()
	dotnet, err := exec.LookPath("dotnet")
	if err != nil {
		if os.Getenv("NOVA_CONTRACT_STRICT") == "1" && runtime.GOOS == "linux" {
			t.Fatal("no dotnet SDK on PATH; run: nova-fleet runner-packages -- -l <this bench>")
		}
		t.Skip("no dotnet SDK on PATH (AGENTS.md, functional prerequisites)")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	tmp := t.TempDir()
	env := append(os.Environ(), "DOTNET_CLI_HOME="+filepath.Join(tmp, "home"), "DOTNET_NOLOGO=1",
		"DOTNET_CLI_TELEMETRY_OPTOUT=1", "DOTNET_SKIP_FIRST_TIME_EXPERIENCE=1")
	ver, err := exec.CommandContext(ctx, dotnet, "--version").Output()
	if err != nil {
		t.Fatalf("dotnet --version: %v", err)
	}
	major, _, _ := strings.Cut(strings.TrimSpace(string(ver)), ".")
	proj := filepath.Join(tmp, "cs")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(proj, "cs.csproj"), fmt.Appendf(nil, csharpProject, major+".0"), 0o644); err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, script := range scripts {
		for _, p := range csharpParts(t, script) {
			n++
			if err := os.WriteFile(filepath.Join(proj, fmt.Sprintf("part%d.cs", n)), []byte(p), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	prog, err := os.ReadFile(program)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(proj, "Program.cs"), prog, 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(ctx, dotnet, append([]string{"run", "--project", proj, "--"}, args...)...)
	cmd.Dir = proj
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	passed := strings.Contains(string(out), "\nok\n") || strings.HasPrefix(string(out), "ok\n")
	if err != nil || !passed {
		t.Fatalf("%s (%v):\n%s", program, err, out)
	}
}

// TestFunctionalStageCheck: the stage check's decisions (files/stage-check.ps1) on fake ACLs: testdata/stagecheck.
func TestFunctionalStageCheck(t *testing.T) {
	t.Parallel()
	runCSharp(t, "testdata/stagecheck/Program.cs", []string{"roles/github-runner/files/stage-check.ps1"})
}
