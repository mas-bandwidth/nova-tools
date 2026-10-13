package doctor

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDoctorRunDirFlag is card doctor-run-dir-flag (ratings triage
// 2026-10-13 Part 2, ordinal 19): `run --dir <path>` scopes the gosdk
// check to <path>/go.mod without changing the working directory, and a
// missing target directory is refused at exit 2.
func TestDoctorRunDirFlag(t *testing.T) {
	t.Parallel()
	bin := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(bin, "go"), []byte("#!/bin/sh\n"), 0o755))
	repo := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(repo, "go.mod"), []byte("module example\n\ngo 1.27\n"), 0o644))
	cache := t.TempDir()
	env := fakeEnv{
		env: map[string]string{"PATH": bin},
		exec: func(name string, args ...string) (string, error) {
			switch {
			case name == "sh":
				return "", nil // dirWritable probe
			case len(args) == 1 && args[0] == "version":
				return "go version go1.27.1 linux/amd64", nil
			case len(args) == 2 && args[0] == "env" && args[1] == "GOCACHE":
				return cache, nil
			case len(args) == 2 && args[0] == "env" && args[1] == "GOFLAGS":
				return "-mod=readonly", nil
			case len(args) == 2 && args[0] == "env" && args[1] == "GOMOD":
				return "/dev/null", nil // no go.mod at the working directory
			}
			return "", nil
		},
	}
	reg := NewRegistry()
	reg.Register(Check{Name: "gosdk", Dependency: "the Go toolchain", Run: checkGoSDK})
	run := func(args ...string) (int, string, string) {
		var out, errb bytes.Buffer
		code := Main(reg, env, "", append([]string{"run"}, args...), strings.NewReader(""), &out, &errb)
		return code, out.String(), errb.String()
	}

	code, out, _ := run("--check", "gosdk")
	assert.Equal(t, 2, code, "without --dir the cwd has no go.mod: %s", out)
	assert.Contains(t, out, "no go.mod here")

	code, out, _ = run("--check", "gosdk", "--dir", repo)
	assert.Equal(t, 0, code, "with --dir the check reads <dir>/go.mod: %s", out)
	assert.Contains(t, out, "DOCTOR gosdk ok")

	code, _, errb := run("--check", "gosdk", "--dir", filepath.Join(repo, "missing"))
	assert.Equal(t, 2, code)
	assert.Contains(t, errb, "--dir")
}
