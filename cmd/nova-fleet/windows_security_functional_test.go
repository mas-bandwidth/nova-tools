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

// windows_security_functional_test.go runs the admin runner's security decisions, the C# inside runner-windows.ps1
// (NovaTree: sealed trees, the PATH and tool folders, the folders above the admin root, the zip's extract and verify)
// and nova-ci-rules.ps1 (NovaCiAdmin: which jobs an admin runner takes), on fake ACLs and a real zip in a temp dir:
// testdata/winsec/Program.cs, compiled with the scripts' own C# at the language version Windows PowerShell's Add-Type
// compiles (C# 5), warnings as errors. It needs the dotnet SDK (AGENTS.md, functional prerequisites) and skips without
// it; nothing reaches Windows.

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
	return out
}

const winsecProject = `<Project Sdk="Microsoft.NET.Sdk">
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

func TestFunctionalWindowsSecurityDecisions(t *testing.T) {
	dotnet, err := exec.LookPath("dotnet")
	if err != nil {
		// CI's Linux functional job runs on benches whose runner-packages list carries dotnet-sdk-10.0: there it fails
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
		t.Skipf("dotnet --version: %v", err)
	}
	major, _, _ := strings.Cut(strings.TrimSpace(string(ver)), ".")
	proj := filepath.Join(tmp, "winsec")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(proj, "winsec.csproj"), fmt.Appendf(nil, winsecProject, major+".0"), 0o644); err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, script := range []string{"roles/github-runner/files/runner-windows.ps1", "roles/github-runner/files/ci-rules/nova-ci-rules.ps1"} {
		parts := csharpParts(t, script)
		if len(parts) == 0 {
			t.Fatalf("%s: no C#", script)
		}
		for _, p := range parts {
			n++
			if err := os.WriteFile(filepath.Join(proj, fmt.Sprintf("part%d.cs", n)), []byte(p), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	prog, err := os.ReadFile("testdata/winsec/Program.cs")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(proj, "Program.cs"), prog, 0o644); err != nil {
		t.Fatal(err)
	}
	zips := filepath.Join(tmp, "zips")
	if err := os.MkdirAll(zips, 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(ctx, dotnet, "run", "--project", proj, "--", zips)
	cmd.Dir = proj
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	passed := strings.Contains(string(out), "\nok\n") || strings.HasPrefix(string(out), "ok\n")
	if err != nil || !passed {
		t.Fatalf("the security decisions (%v):\n%s", err, out)
	}
}
