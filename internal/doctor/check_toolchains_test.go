package doctor

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDoctorToolchainsCheckReadsFactsAndNamesMissingTools(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	home := filepath.Join(root, "home")
	factsPath := filepath.Join(home, ".config", "nova", "toolchains.json")
	paths := map[string]map[string]string{}
	for _, name := range requiredToolchains {
		path := filepath.Join(root, "sdk", name, "bin", name)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte("fixture"), 0o755))
		paths[name] = map[string]string{"path": path, "version": "1.0.0"}
	}
	writeDoctorToolchainFacts(t, factsPath, paths)
	result := runDoctorToolchainCheck(t, home)
	assert.Equal(t, OK, result.Status, result)
	assert.Contains(t, result.Evidence, "node 1.0.0")
	assert.Contains(t, result.Evidence, "gh 1.0.0")
	assert.Empty(t, result.Fix)

	t.Run("missing fact and non-executable path name their tools", func(t *testing.T) {
		paths["dart"] = nil
		badPath := filepath.Join(root, "sdk", "sbcl", "bin", "sbcl")
		require.NoError(t, os.Chmod(badPath, 0o644))
		writeDoctorToolchainFacts(t, factsPath, paths)
		result := runDoctorToolchainCheck(t, home)
		assert.Equal(t, Fail, result.Status, result)
		assert.Contains(t, result.Evidence, "missing tool=dart")
		assert.Contains(t, result.Evidence, "missing tool=sbcl")
		assert.Contains(t, result.Fix, "ansible-playbook fleet/tools.yml --tags toolchains")
	})

	t.Run("unavailable facts name every required tool", func(t *testing.T) {
		require.NoError(t, os.Remove(factsPath))
		result := runDoctorToolchainCheck(t, home)
		assert.Equal(t, Fail, result.Status, result)
		for _, name := range requiredToolchains {
			assert.Contains(t, result.Evidence, "missing tool="+name)
		}
	})
}

func writeDoctorToolchainFacts(t *testing.T, path string, tools map[string]map[string]string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	b, err := json.Marshal(map[string]any{"schema": 1, "toolchains": tools})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, b, 0o600))
}

func runDoctorToolchainCheck(t *testing.T, home string) Result {
	t.Helper()
	fe := fakeEnv{env: map[string]string{"HOME": home}, exec: func(name string, args ...string) (string, error) {
		if name != "test" || len(args) != 2 || args[0] != "-x" {
			return "", errors.New("unexpected command: " + name + " " + strings.Join(args, " "))
		}
		info, err := os.Stat(args[1])
		if err != nil || info.Mode()&0o111 == 0 {
			return "", errors.New("not executable")
		}
		return "", nil
	}}
	return checkToolchains(context.Background(), fe)
}
