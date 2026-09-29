package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/config"
)

// fleet_test.go validates the WSL2 bench Ansible role structure and machine integration
// with nova-config (Rowan Item 10 / ideas#820).

const (
	fleetBenchPlaybook = "../../fleet/bench-wsl2.yml"
	fleetBenchRole     = "../../fleet/roles/bench-wsl2"
)

func TestWSL2BenchRoleStructure(t *testing.T) {
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
		fullPath := filepath.Join(fleetBenchRole, rel)
		data, err := os.ReadFile(fullPath)
		if err != nil {
			t.Fatalf("missing required role file %s: %v", fullPath, err)
		}
		if len(strings.TrimSpace(string(data))) == 0 {
			t.Errorf("role file %s is empty", fullPath)
		}
	}

	// Verify fleet/bench-wsl2.yml playbook exists and targets bench-wsl2
	pbData, err := os.ReadFile(fleetBenchPlaybook)
	if err != nil {
		t.Fatalf("missing playbook %s: %v", fleetBenchPlaybook, err)
	}
	if !strings.Contains(string(pbData), "bench-wsl2") {
		t.Errorf("playbook %s does not reference bench-wsl2 role", fleetBenchPlaybook)
	}
}

func TestWSL2BenchRoleCoversPreflightAndWSLConfig(t *testing.T) {
	t.Parallel()

	preflightFile := filepath.Join(fleetBenchRole, "tasks/preflight.yml")
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

func TestWSL2BenchRoleCoversRunnerUserAndDynamicHomes(t *testing.T) {
	t.Parallel()

	runnerFile := filepath.Join(fleetBenchRole, "tasks/runner.yml")
	data, err := os.ReadFile(runnerFile)
	if err != nil {
		t.Fatalf("cannot read %s: %v", runnerFile, err)
	}
	content := string(data)

	for _, want := range []string{
		"bench_wsl2_user",
		"bench_wsl2_group",
		"bench_wsl2_user_home",
		"/etc/sudoers.d",
		"/etc/wsl.conf",
		".gitconfig",
	} {
		if !strings.Contains(content, want) {
			t.Errorf("%s missing required configuration: %q", runnerFile, want)
		}
	}

	// Verify no hardcoded /home/nova paths in any role YAML
	err = filepath.Walk(fleetBenchRole, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || (!strings.HasSuffix(path, ".yml") && !strings.HasSuffix(path, ".yaml")) {
			return nil
		}
		raw, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		if strings.Contains(string(raw), "/home/nova") {
			t.Errorf("file %s contains hardcoded '/home/nova'; must use dynamic {{ bench_wsl2_user_home }}", path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", fleetBenchRole, err)
	}
}

func TestWSL2BenchRoleCoversToolchains(t *testing.T) {
	t.Parallel()

	toolchainsFile := filepath.Join(fleetBenchRole, "tasks/toolchains.yml")
	data, err := os.ReadFile(toolchainsFile)
	if err != nil {
		t.Fatalf("cannot read %s: %v", toolchainsFile, err)
	}
	content := string(data)

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

func TestWSL2BenchRoleCoversRedisAndTailnetIdentity(t *testing.T) {
	t.Parallel()

	redisFile := filepath.Join(fleetBenchRole, "tasks/redis.yml")
	rData, err := os.ReadFile(redisFile)
	if err != nil {
		t.Fatalf("cannot read %s: %v", redisFile, err)
	}
	for _, want := range []string{
		"redis-server",
		"redis-tools",
		"/etc/redis/redis.conf",
		"bench_wsl2_redis_service",
		"notify: restart redis",
	} {
		if !strings.Contains(string(rData), want) {
			t.Errorf("%s missing required redis step: %q", redisFile, want)
		}
	}

	tailnetFile := filepath.Join(fleetBenchRole, "tasks/tailnet.yml")
	tData, err := os.ReadFile(tailnetFile)
	if err != nil {
		t.Fatalf("cannot read %s: %v", tailnetFile, err)
	}
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
		if !strings.Contains(string(tData), want) {
			t.Errorf("%s missing required identity step: %q", tailnetFile, want)
		}
	}
}

func TestWSL2BenchRoleAnsibleSyntaxCheck(t *testing.T) {
	t.Parallel()

	ansibleBin, err := exec.LookPath("ansible-playbook")
	if err != nil {
		t.Skip("ansible-playbook not on PATH; skipping syntax check")
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

func TestWSL2BenchMachineIntegration(t *testing.T) {
	t.Parallel()

	h := newHarness()
	h.env["NOVA_PG_DSN"] = dsn
	h.env["NOVA_SPRINT_REDIS"] = "127.0.0.1:6379"
	h.env["NOVA_FRIEND"] = "rowan"

	// 1. Add WSL2 bench to Postgres via nova-config machine add
	code, out, errs := h.run(t, "machine", "add", "wsl2-bench-1", "--user", "glenn", "--seat", "swarm-wsl2", "--slots", "32", "--runners", "1")
	if code != 0 {
		t.Fatalf("machine add failed (exit %d): stdout=%s stderr=%s", code, out, errs)
	}

	// 2. Synchronize machine to Redis using nova-config apply
	code, out, errs = h.run(t, "apply")
	if code != 0 {
		t.Fatalf("apply failed (exit %d): stdout=%s stderr=%s", code, out, errs)
	}
	if !strings.Contains(out, "APPLY ADD kind=machine name=wsl2-bench-1\n") {
		t.Errorf("expected APPLY ADD in stdout, got:\n%s", out)
	}

	// 3. Verify Redis view holds the synced WSL2 machine
	views, _, err := h.redis.Read(context.Background(), config.KindMachine)
	if err != nil {
		t.Fatalf("redis read error: %v", err)
	}
	row := views["wsl2-bench-1"]
	if row == nil {
		t.Fatal("expected wsl2-bench-1 in redis views, got nil")
	}
	if row["slots"] != "32" || row["seat"] != "swarm-wsl2" || row["user"] != "glenn" {
		t.Errorf("unexpected machine row values: %+v", row)
	}

	// 4. Show machine facts
	code, out, errs = h.run(t, "machine", "show", "wsl2-bench-1")
	if code != 0 {
		t.Fatalf("machine show failed (exit %d): stdout=%s stderr=%s", code, out, errs)
	}
	for _, expected := range []string{"user=glenn", "seat=swarm-wsl2", "slots=32"} {
		if !strings.Contains(out, expected) {
			t.Errorf("machine show output missing %q:\n%s", expected, out)
		}
	}
}
