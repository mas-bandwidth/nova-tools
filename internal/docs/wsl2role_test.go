package docs

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// wsl2role_test.go verifies the wsl2_bench_runner and bench-wsl2 Ansible roles
// for Windows WSL2 machines (#1458 Item 14 / PR #4601).
//
// The specification:
// A bench role in the ansible plays for a Windows machine running WSL2 (runner user,
// toolchains: Go/Rust/Nova tools/.NET, redis, the tailnet name as identity),
// check mode validation, idempotency guards, Windows path validation, and no hardcoded homes.

const (
	wsl2PlaybookPath     = "../../playbooks/wsl2_bench_runner.yml"
	wsl2RolePath         = "../../roles/wsl2_bench_runner"
	fleetPlaybookPath    = "../../fleet/bench-wsl2.yml"
	fleetRolePath        = "../../fleet/roles/bench-wsl2"
	wsl2VerifyScriptPath = "../../scripts/verify-wsl2-bench-role.sh"
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

	for _, roleDir := range []string{wsl2RolePath, fleetRolePath} {
		for _, rel := range requiredFiles {
			fullPath := filepath.Join(roleDir, rel)
			data, err := os.ReadFile(fullPath)
			if err != nil {
				t.Fatalf("missing required role file %s: %v", fullPath, err)
			}
			if len(strings.TrimSpace(string(data))) == 0 {
				t.Errorf("role file %s is empty", fullPath)
			}
		}
	}

	// Verify both playbooks exist and target the respective roles
	for pbPath, roleName := range map[string]string{
		wsl2PlaybookPath:  "wsl2_bench_runner",
		fleetPlaybookPath: "bench-wsl2",
	} {
		playbookData, err := os.ReadFile(pbPath)
		if err != nil {
			t.Fatalf("missing playbook %s: %v", pbPath, err)
		}
		if !strings.Contains(string(playbookData), roleName) {
			t.Errorf("playbook %s does not reference role %s", pbPath, roleName)
		}
	}
}

func TestWSL2RoleCoversPreflightWindowsAndWSL(t *testing.T) {
	t.Parallel()

	for _, roleDir := range []string{wsl2RolePath, fleetRolePath} {
		preflightFile := filepath.Join(roleDir, "tasks/preflight.yml")
		data, err := os.ReadFile(preflightFile)
		if err != nil {
			t.Fatalf("cannot read %s: %v", preflightFile, err)
		}
		content := string(data)

		for _, want := range []string{
			"ansible_system == 'Linux'",
			"bench_wsl2_detected",
			"bench_wsl2_windows_mount",
			"bench_wsl2_windows_system32",
			"wsl.exe -l -v",
			"/etc/wsl.conf",
		} {
			if !strings.Contains(content, want) {
				t.Errorf("%s missing preflight validation check: %q", preflightFile, want)
			}
		}
	}
}

func TestWSL2RoleCoversRunnerUserAndWSLConfig(t *testing.T) {
	t.Parallel()

	for _, roleDir := range []string{wsl2RolePath, fleetRolePath} {
		runnerFile := filepath.Join(roleDir, "tasks/runner.yml")
		data, err := os.ReadFile(runnerFile)
		if err != nil {
			t.Fatalf("cannot read %s: %v", runnerFile, err)
		}
		content := string(data)

		// Runner user configuration without hardcoded paths
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
				t.Errorf("%s missing required configuration: %q", runnerFile, want)
			}
		}
	}
}

func TestWSL2RoleCoversToolchains(t *testing.T) {
	t.Parallel()

	for _, roleDir := range []string{wsl2RolePath, fleetRolePath} {
		toolchainsFile := filepath.Join(roleDir, "tasks/toolchains.yml")
		data, err := os.ReadFile(toolchainsFile)
		if err != nil {
			t.Fatalf("cannot read %s: %v", toolchainsFile, err)
		}
		content := string(data)

		// Go, Rust, Nova tools, and .NET toolchains
		for _, want := range []string{
			"bench_wsl2_go_version",
			"bench_wsl2_go_sdk_root",
			"go/bin/go",
			"rustc",
			"cargo",
			"bench_wsl2_cargo_home",
			"bench_wsl2_nova_tools",
			"dotnet",
			"dotnet-sdk",
			"dotnet-install",
		} {
			if !strings.Contains(content, want) {
				t.Errorf("%s missing required toolchain step: %q", toolchainsFile, want)
			}
		}
	}
}

func TestWSL2RoleHasNoHardcodedUserHomes(t *testing.T) {
	t.Parallel()

	// Every path in roles must be dynamic, using {{ bench_wsl2_user_home }} rather than /home/nova
	for _, roleDir := range []string{wsl2RolePath, fleetRolePath} {
		err := filepath.Walk(roleDir, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if info.IsDir() {
				return nil
			}
			if !strings.HasSuffix(path, ".yml") && !strings.HasSuffix(path, ".yaml") {
				return nil
			}

			data, readErr := os.ReadFile(path)
			if readErr != nil {
				return readErr
			}
			content := string(data)
			if strings.Contains(content, "/home/nova") {
				t.Errorf("file %s contains hardcoded user home '/home/nova'; must use dynamic {{ bench_wsl2_user_home }}", path)
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walking %s: %v", roleDir, err)
		}
	}
}

func TestWSL2RoleCoversRedisServer(t *testing.T) {
	t.Parallel()

	for _, roleDir := range []string{wsl2RolePath, fleetRolePath} {
		redisFile := filepath.Join(roleDir, "tasks/redis.yml")
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
				t.Errorf("%s missing required redis step: %q", redisFile, want)
			}
		}
	}
}

func TestWSL2RoleResolvesTailnetMachineIdentity(t *testing.T) {
	t.Parallel()

	for _, roleDir := range []string{wsl2RolePath, fleetRolePath} {
		tailnetFile := filepath.Join(roleDir, "tasks/tailnet.yml")
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
				t.Errorf("%s missing required identity step: %q", tailnetFile, want)
			}
		}
	}
}

func TestWSL2RoleSupportsCheckModeValidation(t *testing.T) {
	t.Parallel()

	for _, roleDir := range []string{wsl2RolePath, fleetRolePath} {
		checkFile := filepath.Join(roleDir, "tasks/check_mode.yml")
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
				t.Errorf("%s missing check mode validation element: %q", checkFile, want)
			}
		}
	}
}

func TestWSL2AnsiblePlaybookSyntax(t *testing.T) {
	t.Parallel()

	ansibleBin, err := exec.LookPath("ansible-playbook")
	if err != nil {
		t.Skip("ansible-playbook not on PATH; skipping syntax-check execution")
	}

	for _, pb := range []struct {
		relDir string
		file   string
	}{
		{relDir: "../../playbooks", file: "wsl2_bench_runner.yml"},
		{relDir: "../../fleet", file: "bench-wsl2.yml"},
	} {
		cmd := exec.Command(ansibleBin, "--syntax-check", pb.file)
		cmd.Dir = pb.relDir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("ansible-playbook --syntax-check %s failed: %v\nOutput:\n%s", pb.file, err, string(out))
		}
		if !strings.Contains(string(out), "playbook: "+pb.file) {
			t.Errorf("expected playbook confirmation for %s in syntax check output, got: %s", pb.file, string(out))
		}
	}
}

func TestWSL2VerificationScriptRunsCleanly(t *testing.T) {
	t.Parallel()

	absScript, err := filepath.Abs(wsl2VerifyScriptPath)
	if err != nil {
		t.Fatalf("failed to resolve script path: %v", err)
	}

	cmd := exec.Command(absScript)
	cmd.Dir = "../.."
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("verification script failed: %v\nOutput:\n%s", err, string(out))
	}
	if !strings.Contains(string(out), "WSL2 BENCH ROLE VERIFIED OK") {
		t.Errorf("expected clean verification banner, got:\n%s", string(out))
	}
}
