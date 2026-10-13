package doctor

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// gosdkRig is the machine the gosdk check reads: a PATH holding go (or not),
// a go.mod under t.TempDir(), and the answers `go version` and `go env` give.
// Nothing real runs and no file is written.
type gosdkRig struct {
	noGo     bool   // go is not on PATH
	version  string // the token after `go version go`, e.g. "1.24.5"
	gomod    string // go.mod's contents; "" means there is no module here
	gocache  string // `go env GOCACHE`
	goflags  string // `go env GOFLAGS`
	writable bool   // whether `test -w <GOCACHE>` answers true
	coord    bool   // this machine holds the coordinator's seat
}

// runGoSDK runs only the gosdk check over the rig.
func runGoSDK(t *testing.T, r gosdkRig) Result {
	t.Helper()
	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	require.NoError(t, os.MkdirAll(bin, 0o755))
	if !r.noGo {
		require.NoError(t, os.WriteFile(filepath.Join(bin, "go"), []byte("#!/bin/sh\n"), 0o755))
	}
	if r.gomod != "" {
		require.NoError(t, os.WriteFile(filepath.Join(root, "go.mod"), []byte(r.gomod), 0o644))
	}
	env := map[string]string{"PATH": "bin", "GOCACHE": r.gocache, "GOFLAGS": r.goflags}
	if r.coord {
		env["NOVA_SECRETS_SEAT"] = "coordinator"
	}
	fe := fakeEnv{env: env, root: root}
	fe.exec = func(name string, args ...string) (string, error) {
		switch filepath.Base(name) {
		case "go":
			switch args[0] {
			case "version":
				if r.version == "" {
					return "", errors.New("go: no version")
				}
				return "go version go" + r.version + " linux/amd64\n", nil
			case "env":
				switch args[1] {
				case "GOMOD":
					if r.gomod == "" {
						return "/dev/null\n", nil
					}
					return "go.mod\n", nil
				case "GOCACHE":
					return r.gocache + "\n", nil
				case "GOFLAGS":
					return r.goflags + "\n", nil
				}
			}
		case "sh":
			if r.writable {
				return "", nil
			}
			return "", errors.New("test: not writable")
		}
		return "", errors.New("unexpected command: " + name + " " + strings.Join(args, " "))
	}
	reg := NewRegistry()
	reg.Register(Default.checks["gosdk"])
	res, _, err := reg.Run(context.Background(), fe, Options{})
	require.NoError(t, err)
	require.Len(t, res, 1)
	return res[0]
}

const rightGoMod = "module example.invalid/x\n\ngo 1.24\n\ntoolchain go1.24.5\n"

// TestDoctorGoCheckRefusesTheWrongToolchainAndAGoOnTheCoordinator pins the
// gosdk check: on a bench go must be the version go.mod's toolchain line
// names with a writable GOCACHE, and on the coordinator's machine go on PATH
// is a warn naming the bench rule (docs/SETUP.md, dep-go-sdk-b.w2).
func TestDoctorGoCheckRefusesTheWrongToolchainAndAGoOnTheCoordinator(t *testing.T) {
	t.Parallel()

	t.Run("a bench with the named toolchain is ok", func(t *testing.T) {
		t.Parallel()
		r := runGoSDK(t, gosdkRig{version: "1.24.5", gomod: rightGoMod, gocache: "cache/go-build", writable: true, goflags: "-mod=readonly"})
		assert.Equal(t, OK, r.Status, r)
		assert.Contains(t, r.Evidence, "1.24.5")
		assert.Empty(t, r.Fix)
	})

	t.Run("a bench on the wrong toolchain is a fail", func(t *testing.T) {
		t.Parallel()
		r := runGoSDK(t, gosdkRig{version: "1.23.4", gomod: rightGoMod, gocache: "cache/go-build", writable: true, goflags: "-mod=readonly"})
		assert.Equal(t, Fail, r.Status, r)
		assert.Contains(t, r.Evidence, "1.23.4")
		assert.Contains(t, r.Evidence, "1.24.5")
		assert.Contains(t, r.Fix, "docs/SETUP.md, dep-go-sdk-b.w2")
	})

	t.Run("a bench with no go is a fail", func(t *testing.T) {
		t.Parallel()
		r := runGoSDK(t, gosdkRig{noGo: true, gomod: rightGoMod, gocache: "cache/go-build", writable: true, goflags: "-mod=readonly"})
		assert.Equal(t, Fail, r.Status, r)
		assert.Contains(t, r.Evidence, "no go on PATH")
		assert.Contains(t, r.Fix, "docs/SETUP.md, dep-go-sdk-b.w2")
	})

	t.Run("a bench whose GOCACHE is not writable is a fail", func(t *testing.T) {
		t.Parallel()
		r := runGoSDK(t, gosdkRig{version: "1.24.5", gomod: rightGoMod, gocache: "cache/go-build", writable: false, goflags: "-mod=readonly"})
		assert.Equal(t, Fail, r.Status, r)
		assert.Contains(t, r.Evidence, "GOCACHE")
		assert.Contains(t, r.Evidence, "not writable")
		assert.Contains(t, r.Fix, "docs/SETUP.md, dep-go-sdk-b.w2")
	})

	t.Run("a bench without -mod=readonly warns", func(t *testing.T) {
		t.Parallel()
		r := runGoSDK(t, gosdkRig{version: "1.24.5", gomod: rightGoMod, gocache: "cache/go-build", writable: true, goflags: ""})
		assert.Equal(t, Warn, r.Status, r)
		assert.Contains(t, r.Evidence, "-mod=readonly")
		assert.Contains(t, r.Fix, "docs/SETUP.md, dep-go-sdk-b.w2")
	})

	t.Run("go on the coordinator's machine warns the bench rule", func(t *testing.T) {
		t.Parallel()
		r := runGoSDK(t, gosdkRig{version: "1.24.5", gomod: rightGoMod, gocache: "cache/go-build", writable: true, goflags: "-mod=readonly", coord: true})
		assert.Equal(t, Warn, r.Status, r)
		assert.Contains(t, r.Evidence, "bench rule")
		assert.Contains(t, r.Fix, "nova-update apply --file <manifest>")
	})

	t.Run("the coordinator's machine with no go is ok", func(t *testing.T) {
		t.Parallel()
		r := runGoSDK(t, gosdkRig{noGo: true, coord: true})
		assert.Equal(t, OK, r.Status, r)
		assert.Contains(t, r.Evidence, "no go on PATH")
		assert.Empty(t, r.Fix)
	})
}

func TestDoctorRunDirFlag(t *testing.T) {
	t.Parallel()

	t.Run("repository-scoped Go probes use the requested checkout", func(t *testing.T) {
		cwd, err := os.Getwd()
		require.NoError(t, err)
		for _, tc := range []struct {
			name, goMod string
			wantExit    int
			wantLine    string
		}{
			{name: "matching checkout", goMod: rightGoMod, wantExit: 0, wantLine: "DOCTOR gosdk ok"},
			{name: "different checkout", goMod: "module example.invalid/y\n\ngo 1.23\n\ntoolchain go1.23.0\n", wantExit: 2, wantLine: "DOCTOR gosdk fail"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()
				root := t.TempDir()
				bin := filepath.Join(root, "bin")
				target := filepath.Join(root, "checkout")
				require.NoError(t, os.MkdirAll(bin, 0o755))
				require.NoError(t, os.MkdirAll(target, 0o755))
				require.NoError(t, os.WriteFile(filepath.Join(bin, "go"), []byte("#!/bin/sh\n"), 0o755))
				require.NoError(t, os.WriteFile(filepath.Join(target, "go.mod"), []byte(tc.goMod), 0o644))
				fe := fakeEnv{root: root, env: map[string]string{"PATH": "bin"}}
				fe.exec = func(name string, args ...string) (string, error) {
					switch filepath.Base(name) {
					case "go":
						if len(args) < 3 || args[0] != "-C" || args[1] != target {
							return "", errors.New("Go probe was not scoped to --dir: " + strings.Join(args, " "))
						}
						args = args[2:]
						if len(args) == 1 && args[0] == "version" {
							return "go version go1.24.5 linux/amd64\n", nil
						}
						if len(args) == 2 && args[0] == "env" {
							switch args[1] {
							case "GOMOD":
								return filepath.Join(target, "go.mod"), nil
							case "GOCACHE":
								return filepath.Join(root, "cache"), nil
							case "GOFLAGS":
								return "-mod=readonly", nil
							}
						}
					case "sh":
						return "", nil
					}
					return "", errors.New("unexpected command: " + name + " " + strings.Join(args, " "))
				}
				var stdout, stderr strings.Builder
				code := Main(Default, fe, "", []string{"run", "--check", "gosdk", "--dir", target}, strings.NewReader(""), &stdout, &stderr)
				assert.Equal(t, tc.wantExit, code, "stdout=%s stderr=%s", stdout.String(), stderr.String())
				assert.Contains(t, stdout.String(), tc.wantLine)
				assert.Empty(t, stderr.String())
				assert.Equal(t, cwd, mustGetwd(t), "--dir never changes the process working directory")
			})
		}
	})

	t.Run("a missing repository is explicitly refused before any check runs", func(t *testing.T) {
		t.Parallel()
		called := false
		fe := fakeEnv{exec: func(string, ...string) (string, error) {
			called = true
			return "", nil
		}}
		var stdout, stderr strings.Builder
		code := Main(Default, fe, "", []string{"run", "--dir", filepath.Join(t.TempDir(), "missing")}, strings.NewReader(""), &stdout, &stderr)
		assert.Equal(t, 2, code)
		assert.Empty(t, stdout.String())
		assert.Contains(t, stderr.String(), "--dir ")
		assert.Contains(t, stderr.String(), "not a readable directory")
		assert.False(t, called, "refusal happens before probes")
	})
}

func mustGetwd(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	require.NoError(t, err)
	return wd
}
