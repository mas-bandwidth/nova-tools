package merge

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/testbin"
)

func TestExecExplicitPathChild(t *testing.T) {
	t.Parallel()
	if os.Getenv("NOVA_MERGE_PATH_HELPER") == "1" {
		fmt.Fprint(os.Stdout, "owned-executable")
		os.Exit(0)
	}
}

func TestExecExplicitPathFindsOwnedExecutable(t *testing.T) {
	t.Parallel()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	name := "gh"
	pathKey := "PATH"
	if runtime.GOOS == "windows" {
		name += ".exe"
		pathKey = "Path" // Windows environment names are case-insensitive.
	}
	if err := testbin.Place(self, filepath.Join(dir, name)); err != nil {
		t.Fatal(err)
	}
	e := Exec{Env: []string{pathKey + "=" + dir, "NOVA_MERGE_PATH_HELPER=1"}}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, run := range []struct {
		name string
		call func(context.Context, string, string, ...string) (string, error)
	}{{"Run", e.Run}, {"RunUncapped", e.RunUncapped}} {
		got, err := run.call(ctx, "", "gh", "-test.run=^TestExecExplicitPathChild$")
		if err != nil || got != "owned-executable" {
			t.Errorf("%s = %q, %v; want owned executable", run.name, got, err)
		}
	}
}

func TestExecExplicitPathMissNeverUsesAmbientExecutable(t *testing.T) {
	t.Parallel()
	name, args := "sh", []string{"-c", "printf ambient"}
	if runtime.GOOS == "windows" {
		name, args = "cmd", []string{"/C", "echo ambient"}
	}
	if _, err := exec.LookPath(name); err != nil {
		t.Skipf("no ambient %s to demonstrate the forbidden fallback: %v", name, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, env := range [][]string{{"PATH=" + t.TempDir()}, {"UNRELATED=value"}} {
		e := Exec{Env: env}
		for _, run := range []struct {
			name string
			call func(context.Context, string, string, ...string) (string, error)
		}{{"Run", e.Run}, {"RunUncapped", e.RunUncapped}} {
			got, err := run.call(ctx, "", name, args...)
			if got != "" || !errors.Is(err, exec.ErrNotFound) {
				t.Errorf("%s with env %q = %q, %v; want exact-PATH not-found", run.name, env, got, err)
			}
		}
	}
}

func TestExecNilEnvKeepsAmbientLookup(t *testing.T) {
	t.Parallel()
	name, args := "sh", []string{"-c", "printf ambient"}
	if runtime.GOOS == "windows" {
		name, args = "cmd", []string{"/C", "echo ambient"}
	}
	if _, err := exec.LookPath(name); err != nil {
		t.Skipf("no ambient %s: %v", name, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, run := range []struct {
		name string
		call func(context.Context, string, string, ...string) (string, error)
	}{{"Run", (Exec{}).Run}, {"RunUncapped", (Exec{}).RunUncapped}} {
		got, err := run.call(ctx, "", name, args...)
		if err != nil || got == "" {
			t.Errorf("%s nil Env = %q, %v; want ambient executable", run.name, got, err)
		}
	}
}

func TestResolveBinaryRefusesRelativePathEntry(t *testing.T) {
	t.Parallel()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	name := "gh"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	if err := testbin.Place(self, filepath.Join(dir, name)); err != nil {
		t.Fatal(err)
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	rel, err := filepath.Rel(cwd, dir)
	if err != nil {
		t.Skipf("temporary executable is on another volume: %v", err)
	}
	_, err = resolveBinary("gh", []string{"PATH=" + rel})
	if !errors.Is(err, exec.ErrDot) {
		t.Fatalf("relative PATH executable = %v, want exec.ErrDot", err)
	}
}
