//go:build functional

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

type keyPair struct {
	privPath string
	pubKey   string
}

func findSops(t *testing.T) string {
	t.Helper()
	if p, err := exec.LookPath("sops"); err == nil {
		return p
	}
	if _, err := os.Stat("/opt/homebrew/bin/sops"); err == nil {
		return "/opt/homebrew/bin/sops"
	}
	t.Skip("sops binary not found")
	return ""
}

func findAgeKeygen(t *testing.T) string {
	t.Helper()
	if p, err := exec.LookPath("age-keygen"); err == nil {
		return p
	}
	if _, err := os.Stat("/opt/homebrew/bin/age-keygen"); err == nil {
		return "/opt/homebrew/bin/age-keygen"
	}
	t.Skip("age-keygen binary not found")
	return ""
}

func genKey(t *testing.T, dir, name string) keyPair {
	t.Helper()
	ageKeygen := findAgeKeygen(t)
	keyDir := filepath.Join(dir, "keys")
	require.NoError(t, os.MkdirAll(keyDir, 0700))
	privPath := filepath.Join(keyDir, name+".key")
	cmd := exec.Command(ageKeygen, "-o", privPath)
	cmd.Env = []string{"PATH=/usr/bin:/bin"}
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "age-keygen failed: %v, out: %s", err, out)
	require.NoError(t, os.Chmod(privPath, 0600))
	data, err := os.ReadFile(privPath)
	require.NoError(t, err)
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "# public key: ") {
			return keyPair{
				privPath: privPath,
				pubKey:   strings.TrimSpace(strings.TrimPrefix(line, "# public key: ")),
			}
		}
	}
	t.Fatalf("public key comment missing in %s", privPath)
	return keyPair{}
}

func sealFileWithSops(t *testing.T, sopsPath string, filePath string, ageKeys []string, content string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filePath, []byte(content), 0600))
	args := []string{"-e", "--age", strings.Join(ageKeys, ","), filePath}
	cmd := exec.Command(sopsPath, args...)
	cmd.Env = []string{"PATH=/usr/bin:/bin"}
	out, err := cmd.Output()
	require.NoError(t, err, "sops encrypt failed: %v", err)
	require.NoError(t, os.WriteFile(filePath, out, 0600))
}
