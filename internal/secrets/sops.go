package secrets

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

const MinSopsVersion = "3.13.3"
const MinAgeKeygenVersion = "1.3.2"

var sopsVersionRegex = regexp.MustCompile(`^sops (\d+)\.(\d+)\.(\d+)`)
var ageKeygenVersionRegex = regexp.MustCompile(`^v?(\d+)\.(\d+)\.(\d+)`)

// CheckSopsVersion probes the sops binary with --disable-version-check to prevent network calls.
func CheckSopsVersion(sopsPath string) (string, error) {
	if !filepathIsExecutable(sopsPath) {
		return "", fmt.Errorf("sops binary %s is absent or not executable; run: brew install sops", sopsPath)
	}

	cmd := exec.Command(sopsPath, "--version", "--disable-version-check")
	cmd.Env = []string{"PATH=/usr/bin:/bin"} // Isolated environment with no proxies or egress helpers
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("sops version probe failed: %w", err)
	}

	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) == 0 {
		return "", fmt.Errorf("sops version probe produced empty output")
	}

	match := sopsVersionRegex.FindStringSubmatch(lines[0])
	if match == nil {
		return "", fmt.Errorf("unable to parse sops version from %q; expected format 'sops X.Y.Z'", lines[0])
	}

	major, _ := strconv.Atoi(match[1])
	minor, _ := strconv.Atoi(match[2])
	patch, _ := strconv.Atoi(match[3])

	if major < 3 || (major == 3 && minor < 13) || (major == 3 && minor == 13 && patch < 3) {
		return fmt.Sprintf("%d.%d.%d", major, minor, patch), fmt.Errorf("sops version %d.%d.%d is too old; minimum required is %s; run: brew upgrade sops", major, minor, patch, MinSopsVersion)
	}

	return fmt.Sprintf("%d.%d.%d", major, minor, patch), nil
}

// CheckAgeKeygenVersion probes the age-keygen binary.
func CheckAgeKeygenVersion(ageKeygenPath string) (string, error) {
	if !filepathIsExecutable(ageKeygenPath) {
		return "", fmt.Errorf("age-keygen binary %s is absent or not executable; run: brew install age", ageKeygenPath)
	}

	cmd := exec.Command(ageKeygenPath, "--version")
	cmd.Env = []string{"PATH=/usr/bin:/bin"}
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("age-keygen version probe failed: %w", err)
	}

	trimmed := strings.TrimSpace(string(out))
	match := ageKeygenVersionRegex.FindStringSubmatch(trimmed)
	if match == nil {
		return "", fmt.Errorf("unable to parse age-keygen version from %q; expected format 'vX.Y.Z'", trimmed)
	}

	major, _ := strconv.Atoi(match[1])
	minor, _ := strconv.Atoi(match[2])
	patch, _ := strconv.Atoi(match[3])

	if major < 1 || (major == 1 && minor < 3) || (major == 1 && minor == 3 && patch < 2) {
		return fmt.Sprintf("%d.%d.%d", major, minor, patch), fmt.Errorf("age-keygen version %d.%d.%d is too old; minimum required is %s; run: brew upgrade age", major, minor, patch, MinAgeKeygenVersion)
	}

	return fmt.Sprintf("%d.%d.%d", major, minor, patch), nil
}

func filepathIsExecutable(path string) bool {
	fi, err := os.Stat(path)
	if err != nil {
		return false
	}
	if fi.IsDir() {
		return false
	}
	return fi.Mode()&0111 != 0
}

// DecryptFile invokes sops -d on a file using an isolated environment.
// It sets SOPS_AGE_KEY_FILE to keyPath, strips all other SOPS_* variables,
// and sets HOME and XDG_CONFIG_HOME to an empty temporary directory.
func DecryptFile(sopsPath, keyPath, filePath string) ([]byte, error) {
	tmpDir, err := os.MkdirTemp("", "nova-secrets-sops-*")
	if err != nil {
		return nil, fmt.Errorf("failed to create temporary isolation directory: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	cmd := exec.Command(sopsPath, "-d", filePath)

	// Filter out all SOPS_* variables from current environment
	var cleanEnv []string
	for _, env := range os.Environ() {
		if strings.HasPrefix(env, "SOPS_") {
			continue
		}
		if strings.HasPrefix(env, "HOME=") || strings.HasPrefix(env, "XDG_CONFIG_HOME=") {
			continue
		}
		cleanEnv = append(cleanEnv, env)
	}

	cleanEnv = append(cleanEnv,
		"SOPS_AGE_KEY_FILE="+keyPath,
		"HOME="+tmpDir,
		"XDG_CONFIG_HOME="+tmpDir,
	)
	cmd.Env = cleanEnv

	var stdoutBuf, stderrBuf bytes.Buffer
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf

	err = cmd.Run()
	if err != nil {
		exitCode := 1
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		}
		// Sanitize sops error: do NOT pass raw stderr through
		return nil, fmt.Errorf("sops failed: exit %d (transcript withheld: run 'sops -d %s' to inspect)", exitCode, filePath)
	}

	return stdoutBuf.Bytes(), nil
}
