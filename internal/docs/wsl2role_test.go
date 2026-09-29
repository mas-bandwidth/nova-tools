package docs

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// wsl2role_test.go verifies the bench-wsl2 Ansible role for Windows WSL2 machines (#1458 Item 14).
//
// The specification:
// A bench role in the ansible plays for a Windows machine running WSL2 (runner user,
// toolchain, redis, the tailnet name as identity), converged on one such machine when
// Glenn names it; until then the role and its check mode.

const (
	wsl2PlaybookPath = "../../fleet/bench-wsl2.yml"
	wsl2RolePath     = "../../fleet/roles/bench-wsl2"
)

func TestWSL2RoleStructureAndDeclarations(t *testing.T) {
	t.Parallel()

	requiredFiles := []string{
		"defaults/main.yml",
		"vars/main.yml",
		"handlers/main.yml",
		"meta/main.yml",
		"tasks/main.yml",
		"tasks/preflight.yml",
		"tasks/runner.yml",
		"tasks/packages.yml",
		"tasks/toolchains.yml",
		"tasks/redis.yml",
		"tasks/tailnet.yml",
		"tasks/check_mode.yml",
	}

	for _, rel := range requiredFiles {
		fullPath := filepath.Join(wsl2RolePath, rel)
		data, err := os.ReadFile(fullPath)
		if err != nil {
			t.Fatalf("missing required role file %s: %v", fullPath, err)
		}
		if len(strings.TrimSpace(string(data))) == 0 {
			t.Errorf("role file %s is empty", fullPath)
		}
	}

	// Verify playbook exists and targets bench-wsl2 role
	playbookData, err := os.ReadFile(wsl2PlaybookPath)
	if err != nil {
		t.Fatalf("missing playbook %s: %v", wsl2PlaybookPath, err)
	}
	playbook := string(playbookData)
	if !strings.Contains(playbook, "bench-wsl2") {
		t.Errorf("playbook %s does not reference bench-wsl2 role", wsl2PlaybookPath)
	}
}

func TestWSL2RoleCoversRunnerUserAndWSLConfig(t *testing.T) {
	t.Parallel()

	runnerFile := filepath.Join(wsl2RolePath, "tasks/runner.yml")
	data, err := os.ReadFile(runnerFile)
	if err != nil {
		t.Fatalf("cannot read %s: %v", runnerFile, err)
	}
	content := string(data)

	// Runner user configuration
	for _, want := range []string{
		"bench_wsl2_user",
		"/etc/sudoers.d",
		"NOPASSWD:ALL",
		"/etc/wsl.conf",
		"systemd=true",
		"default=",
		"/etc/environment",
		"PATH=",
		".gitconfig",
	} {
		if !strings.Contains(content, want) {
			t.Errorf("tasks/runner.yml missing required configuration: %q", want)
		}
	}
}

func TestWSL2RoleCoversToolchains(t *testing.T) {
	t.Parallel()

	toolchainsFile := filepath.Join(wsl2RolePath, "tasks/toolchains.yml")
	data, err := os.ReadFile(toolchainsFile)
	if err != nil {
		t.Fatalf("cannot read %s: %v", toolchainsFile, err)
	}
	content := string(data)

	// Go SDK and .NET SDK toolchain installation
	for _, want := range []string{
		"bench_wsl2_go_version",
		"bench_wsl2_go_sdk_root",
		"linux-",
		"go/bin/go",
		"dotnet",
		"dotnet-sdk",
		"dotnet-install",
	} {
		if !strings.Contains(content, want) {
			t.Errorf("tasks/toolchains.yml missing required toolchain step: %q", want)
		}
	}
}

func TestWSL2RoleCoversRedisServer(t *testing.T) {
	t.Parallel()

	redisFile := filepath.Join(wsl2RolePath, "tasks/redis.yml")
	data, err := os.ReadFile(redisFile)
	if err != nil {
		t.Fatalf("cannot read %s: %v", redisFile, err)
	}
	content := string(data)

	for _, want := range []string{
		"redis-server",
		"redis-tools",
		"/etc/redis/redis.conf",
		"bench_wsl2_redis_service",
		"notify: restart redis",
	} {
		if !strings.Contains(content, want) {
			t.Errorf("tasks/redis.yml missing required redis step: %q", want)
		}
	}
}

func TestWSL2RoleResolvesTailnetMachineIdentity(t *testing.T) {
	t.Parallel()

	tailnetFile := filepath.Join(wsl2RolePath, "tasks/tailnet.yml")
	data, err := os.ReadFile(tailnetFile)
	if err != nil {
		t.Fatalf("cannot read %s: %v", tailnetFile, err)
	}
	content := string(data)

	for _, want := range []string{
		"tailscale ip -4",
		"tailscale status",
		"bench_wsl2_tailnet_ip",
		"bench_wsl2_tailnet_name",
		"bench_wsl2_identity",
		"identity.tsv",
		"identity.env",
		"owner\tname\temail",
	} {
		if !strings.Contains(content, want) {
			t.Errorf("tasks/tailnet.yml missing required identity step: %q", want)
		}
	}
}

func TestWSL2RoleSupportsCheckModeValidation(t *testing.T) {
	t.Parallel()

	checkFile := filepath.Join(wsl2RolePath, "tasks/check_mode.yml")
	data, err := os.ReadFile(checkFile)
	if err != nil {
		t.Fatalf("cannot read %s: %v", checkFile, err)
	}
	content := string(data)

	for _, want := range []string{
		"ansible_check_mode",
		"BENCH-WSL2 CHECK",
		"mode=dry-run",
		"BENCH-WSL2 OK",
		"mode=converged",
	} {
		if !strings.Contains(content, want) {
			t.Errorf("tasks/check_mode.yml missing check mode validation element: %q", want)
		}
	}
}

func TestWSL2AnsiblePlaybookSyntax(t *testing.T) {
	t.Parallel()

	ansibleBin, err := exec.LookPath("ansible-playbook")
	if err != nil {
		t.Skip("ansible-playbook not on PATH; skipping syntax-check execution")
	}

	cmd := exec.Command(ansibleBin, "--syntax-check", "bench-wsl2.yml")
	cmd.Dir = "../../fleet"
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("ansible-playbook --syntax-check bench-wsl2.yml failed: %v\nOutput:\n%s", err, string(out))
	}

	if !strings.Contains(string(out), "playbook: bench-wsl2.yml") {
		t.Errorf("expected playbook confirmation in syntax check output, got: %s", string(out))
	}
}
