package secrets

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/subproc"
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

	cmd, cancel := subproc.Command(context.Background(), subproc.Tool, sopsPath, "--version", "--disable-version-check")
	defer cancel()
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

	cmd, cancel := subproc.Command(context.Background(), subproc.Tool, ageKeygenPath, "--version")
	defer cancel()
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

	cmd, cancel := subproc.Command(context.Background(), subproc.Tool, sopsPath, "-d", filePath)
	defer cancel()

	// Build isolated environment: do not inherit caller's AWS_*, VAULT_*, GNUPGHOME, etc.
	cleanEnv := []string{
		"PATH=" + os.Getenv("PATH"),
		"SOPS_AGE_KEY_FILE=" + keyPath,
		"HOME=" + tmpDir,
		"XDG_CONFIG_HOME=" + tmpDir,
	}
	if tmp := os.Getenv("TMPDIR"); tmp != "" {
		cleanEnv = append(cleanEnv, "TMPDIR="+tmp)
	}
	if sysroot := os.Getenv("SYSTEMROOT"); sysroot != "" {
		cleanEnv = append(cleanEnv, "SYSTEMROOT="+sysroot)
	}
	cmd.Env = cleanEnv

	var stdoutBuf, stderrBuf bytes.Buffer
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf

	err = cmd.Run()
	if err != nil {
		return nil, sopsFailed(err, sopsPath, keyPath, filePath)
	}

	return stdoutBuf.Bytes(), nil
}

// sopsFailed is a failed `sops -d`, its stderr withheld (it may quote the file):
// when the key's public half is not among the file's recipients that is the
// cause, named with the recipients that would open it; otherwise the remedy
// reproduces the decrypt with the key it was given, since a bare `sops -d` has
// no identity and fails for another reason.
func sopsFailed(err error, sopsPath, keyPath, filePath string) error {
	exitCode := 1
	if exitErr, ok := err.(*exec.ExitError); ok {
		exitCode = exitErr.ExitCode()
	}
	pub, pubErr := ExtractPublicKeyFromKeyFile(keyPath)
	_, recipients, _, recErr := ParseStoreFileWithoutDecrypting(filePath)
	if pubErr == nil && recErr == nil && len(recipients) > 0 && !slices.Contains(recipients, pub) {
		return fmt.Errorf("sops failed: exit %d: --key %s is %s, not a recipient of %s (its recipients: %s); pass --key the private key of one of them",
			exitCode, keyPath, pub, filePath, strings.Join(recipients, ", "))
	}
	// Every path is one shell word (oneline.ShellWord), so the command runs as
	// written. The program stands in command position after an assignment, where
	// a bare word holding = would be read as another assignment: it is quoted.
	prog := oneline.ShellWord(sopsPath)
	if strings.Contains(prog, "=") && !strings.HasPrefix(prog, "'") {
		prog = "'" + prog + "'"
	}
	return fmt.Errorf("sops failed: exit %d, transcript withheld (it may quote the file); to see it, run: SOPS_AGE_KEY_FILE=%s %s -d %s",
		exitCode, oneline.ShellWord(keyPath), prog, oneline.ShellWord(filePath))
}
