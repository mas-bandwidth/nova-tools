//go:build functional

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func findRealSops(t *testing.T) string {
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

func findRealAgeKeygen(t *testing.T) string {
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

func genRealKey(t *testing.T, dir, name string) keyPair {
	t.Helper()
	ageKeygen := findRealAgeKeygen(t)
	keyDir := filepath.Join(dir, "keys")
	if err := os.MkdirAll(keyDir, 0700); err != nil {
		t.Fatal(err)
	}
	privPath := filepath.Join(keyDir, name+".key")
	cmd := exec.Command(ageKeygen, "-o", privPath)
	cmd.Env = []string{"PATH=/usr/bin:/bin"}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("age-keygen failed: %v, out: %s", err, out)
	}
	if err := os.Chmod(privPath, 0600); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(privPath)
	if err != nil {
		t.Fatal(err)
	}
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

func sealFileWithRealSops(t *testing.T, sopsPath string, filePath string, ageKeys []string, content string) {
	t.Helper()
	if err := os.WriteFile(filePath, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	args := []string{"-e", "--age", strings.Join(ageKeys, ","), filePath}
	cmd := exec.Command(sopsPath, args...)
	cmd.Env = []string{"PATH=/usr/bin:/bin"}
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("sops encrypt failed: %v", err)
	}
	if err := os.WriteFile(filePath, out, 0600); err != nil {
		t.Fatal(err)
	}
}
