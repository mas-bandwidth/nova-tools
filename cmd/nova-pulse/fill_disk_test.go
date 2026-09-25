package main

// The capacity formula's disk term measures the volume the CARDS land on (#1476). On a
// bench whose swarm root is a symlink onto another disk -- antman's `~/rowan-swarm-root ->
// /data/swarm` -- `df $HOME` measures the root LV, which is not the volume that fills up.
// The term resolves the swarm root through its symlinks and measures THAT path, and falls
// back to $HOME when no swarm root is configured or the configured one is not there.
//
// The prelude is shell because it runs on the bench over ssh, so the test runs it with
// /bin/sh over tmp directories: no ssh, no network, no real bench.

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// resolveRoot runs the prelude with HOME set to a fixture home and answers what it resolved
// the swarm root to.
func resolveRoot(t *testing.T, home, root string) string {
	t.Helper()
	cmd := exec.Command("/bin/sh", "-c", swarmRootScript(root)+`; echo "$r"`)
	cmd.Env = append(os.Environ(), "HOME="+home)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("the prelude failed: %v; output=%q", err, out)
	}
	return strings.TrimSpace(string(out))
}

// real is the path with every symlink resolved, which is what the prelude's `pwd -P`
// answers (on darwin /var is itself a symlink to /private/var).
func real(t *testing.T, path string) string {
	t.Helper()
	p, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// TestDiskTermFollowsTheSwarmRootSymlink: the term measures the volume behind the link, not
// the home the link lives in.
func TestDiskTermFollowsTheSwarmRootSymlink(t *testing.T) {
	dir := t.TempDir()
	home, data := filepath.Join(dir, "home"), filepath.Join(dir, "data", "swarm")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(data, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(data, filepath.Join(home, "rowan-swarm-root")); err != nil {
		t.Fatal(err)
	}
	if got, want := resolveRoot(t, home, defaultSwarmRoot), real(t, data); got != want {
		t.Errorf("the disk term measures %q, want the swarm root's own volume %q", got, want)
	}
}

// TestDiskTermFallsBackToHome: no swarm root configured, and one configured but absent,
// both measure $HOME -- the behaviour every bench has today.
func TestDiskTermFallsBackToHome(t *testing.T) {
	dir := t.TempDir()
	home := filepath.Join(dir, "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	want := real(t, home)
	if got := resolveRoot(t, home, ""); got != want {
		t.Errorf("with no swarm root the disk term measures %q, want %q", got, want)
	}
	if got := resolveRoot(t, home, "$HOME/rowan-swarm-root"); got != want {
		t.Errorf("with an absent swarm root the disk term measures %q, want %q", got, want)
	}
}

// TestCapacityScriptMeasuresTheResolvedRoot: the formula's df reads the resolved path and
// never $HOME directly, so the three terms are read off the same machine the cards use.
func TestCapacityScriptMeasuresTheResolvedRoot(t *testing.T) {
	script := capacityScript(defaultSwarmRoot)
	if !strings.Contains(script, `df -BG "$r"`) {
		t.Errorf("the capacity script does not df the resolved swarm root: %s", script)
	}
	if strings.Contains(script, `df -BG "$HOME"`) {
		t.Errorf("the capacity script still measures $HOME directly: %s", script)
	}
}
