package ci

import (
	"strings"
	"testing"
)

// THE CLASS RULE: EVERY TEST REDIS SERVER REGISTERS t.Cleanup AND REGISTRY CLEANUP (#4368).
//
// A test redis-server must not outlive its test. When a test binary dies before
// cleanups run (killed, timeout, shard cancellation), the server is recorded in
// the test-redis registry so that the next run sweeps it. When a test ends
// normally, its t.Cleanup must both terminate the server and remove its registry
// entry, leaving no orphaned process and no stale registry file.
//
// Every helper that starts a test redis-server (internal/testredis and
// internal/nsprint/testutil) must register t.Cleanup to stop/kill the server and
// must unregister from the testredis registry. Furthermore, each helper must run
// the orphan sweep before starting a new server.
func TestEveryRedisStartHasCleanup(t *testing.T) {
	t.Parallel()

	tree := repoTree(t)

	// Check 1: internal/testredis/testredis.go
	fTestredis := tree.ByRel("internal/testredis/testredis.go")
	if fTestredis == nil {
		t.Fatal("internal/testredis/testredis.go not found in repo tree")
	}
	srcTestredis := string(fTestredis.Src)
	hasCleanup, hasRegistry, hasSweep := inspectRedisHelper(srcTestredis)
	if !hasCleanup {
		t.Errorf("internal/testredis/testredis.go must register t.Cleanup to stop servers")
	}
	if !hasRegistry {
		t.Errorf("internal/testredis/testredis.go must register started server in registry")
	}
	if !hasSweep {
		t.Errorf("internal/testredis/testredis.go must run SweepOrphans before starting servers")
	}
	if !strings.Contains(srcTestredis, "s.unregister()") && !strings.Contains(srcTestredis, "unregister()") {
		t.Errorf("internal/testredis/testredis.go must invoke unregister callback on server stop/cleanup")
	}

	// Check 2: internal/nsprint/testutil/redis.go
	fTestutil := tree.ByRel("internal/nsprint/testutil/redis.go")
	if fTestutil == nil {
		t.Fatal("internal/nsprint/testutil/redis.go not found in repo tree")
	}
	srcTestutil := string(fTestutil.Src)
	hasCleanup, hasRegistry, hasSweep = inspectRedisHelper(srcTestutil)
	if !hasCleanup {
		t.Errorf("internal/nsprint/testutil/redis.go must register t.Cleanup to stop servers")
	}
	if !hasRegistry {
		t.Errorf("internal/nsprint/testutil/redis.go must register started server in registry")
	}
	if !hasSweep {
		t.Errorf("internal/nsprint/testutil/redis.go must run SweepOrphans before starting servers")
	}
	if !strings.Contains(srcTestutil, "unregister()") {
		t.Errorf("internal/nsprint/testutil/redis.go must invoke unregister callback on cleanup")
	}

	// Check 3: No other test file spawns redis-server directly without t.Cleanup
	for _, f := range tree.Files {
		if !f.Go || !f.Test || f.HasDirNamed("testdata") || f.HasDirNamed("vendor") || isDeprecatedDir(repoRoot(t), f.Path) {
			continue
		}
		if strings.HasPrefix(f.Rel, "internal/ci/") || f.Rel == "internal/testredis/testredis_test.go" || f.Rel == "internal/testredis/testredis_functional_test.go" {
			continue
		}
		src := string(f.Src)
		if strings.Contains(src, `"redis-server"`) && strings.Contains(src, "exec.Command") {
			if !strings.Contains(src, "t.Cleanup") {
				t.Errorf("%s executes redis-server directly without registering t.Cleanup", f.Rel)
			}
		}
	}
}

// inspectRedisHelper checks whether src registers cleanup, registry entry, and sweep.
func inspectRedisHelper(src string) (hasCleanup, hasRegistry, hasSweep bool) {
	hasCleanup = strings.Contains(src, "t.Cleanup(")
	hasRegistry = strings.Contains(src, "Register(")
	hasSweep = strings.Contains(src, "SweepOrphans(")
	return
}

func TestEveryRedisStartHasCleanupCatchesMissingCleanup(t *testing.T) {
	t.Parallel()

	// Negative control 1: missing t.Cleanup
	srcNoCleanup := `package foo
func start() {
	Register(pid, port, ppid)
	SweepOrphans(os.Stderr)
}`
	hasCleanup, hasRegistry, hasSweep := inspectRedisHelper(srcNoCleanup)
	if hasCleanup || !hasRegistry || !hasSweep {
		t.Errorf("inspectRedisHelper failed to detect missing t.Cleanup: cleanup=%v registry=%v sweep=%v", hasCleanup, hasRegistry, hasSweep)
	}

	// Negative control 2: missing Register
	srcNoReg := `package foo
func start(t *testing.T) {
	SweepOrphans(os.Stderr)
	t.Cleanup(func() {})
}`
	hasCleanup, hasRegistry, hasSweep = inspectRedisHelper(srcNoReg)
	if !hasCleanup || hasRegistry || !hasSweep {
		t.Errorf("inspectRedisHelper failed to detect missing Register: cleanup=%v registry=%v sweep=%v", hasCleanup, hasRegistry, hasSweep)
	}

	// Negative control 3: missing SweepOrphans
	srcNoSweep := `package foo
func start(t *testing.T) {
	Register(pid, port, ppid)
	t.Cleanup(func() {})
}`
	hasCleanup, hasRegistry, hasSweep = inspectRedisHelper(srcNoSweep)
	if !hasCleanup || !hasRegistry || hasSweep {
		t.Errorf("inspectRedisHelper failed to detect missing SweepOrphans: cleanup=%v registry=%v sweep=%v", hasCleanup, hasRegistry, hasSweep)
	}
}
