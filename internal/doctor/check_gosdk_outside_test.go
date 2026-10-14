package doctor

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRunLocalOutsideACheckoutRefusesNamingTheWantedCheckout pins the gosdk
// check outside a checkout: with a go on PATH whose `go env GOMOD` answers
// /dev/null, `run --local` declines at exit 2 naming the wanted checkout (its
// go.mod module path) and --dir, the flag that points at one, instead of
// blaming a Go toolchain that is fine (docs/SPEC-DOCTOR.md, "The command").
func TestRunLocalOutsideACheckoutRefusesNamingTheWantedCheckout(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	require.NoError(t, os.MkdirAll(bin, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(bin, "go"), []byte("#!/bin/sh\n"), 0o755))
	fe := fakeEnv{
		env:  map[string]string{"PATH": "bin", "GOCACHE": "cache/go-build", "GOFLAGS": "-mod=readonly"},
		root: root,
	}
	fe.exec = func(name string, args ...string) (string, error) {
		if filepath.Base(name) != "go" {
			return "", errors.New("unexpected command: " + name + " " + strings.Join(args, " "))
		}
		switch args[0] {
		case "version":
			return "go version go1.24.5 linux/amd64\n", nil
		case "env":
			switch args[1] {
			case "GOMOD":
				return "/dev/null\n", nil // no go.mod here: outside a checkout
			case "GOCACHE":
				return "cache/go-build\n", nil
			case "GOFLAGS":
				return "-mod=readonly\n", nil
			}
		}
		return "", errors.New("unexpected go probe: " + strings.Join(args, " "))
	}
	reg := NewRegistry()
	reg.Register(Default.checks["gosdk"])

	var stdout, stderr strings.Builder
	code := Main(reg, fe, "", []string{"run", "--local"}, strings.NewReader(""), &stdout, &stderr)
	require.Equal(t, 2, code, "stdout=%s stderr=%s", stdout.String(), stderr.String())

	out := stdout.String()
	assert.Contains(t, out, wantedCheckout(), "names the wanted checkout's module path")
	assert.Contains(t, out, "--dir", "names the flag that points at a checkout")
	assert.NotContains(t, out, "toolchain", "does not send the reader to fix a toolchain that is fine")
}
