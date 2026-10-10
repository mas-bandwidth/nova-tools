package secrets

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/pkg/oneline"
	"github.com/mas-bandwidth/nova-tools/pkg/safepath"
)

const MinSopsVersion = "3.13.3"
const MinAgeKeygenVersion = "1.3.2"

var sopsVersionRegex = regexp.MustCompile(`^sops (\d+)\.(\d+)\.(\d+)`)
var ageKeygenVersionRegex = regexp.MustCompile(`^v?(\d+)\.(\d+)\.(\d+)`)

// runOr is the one seam every helper process runs through: the scripted fake a verb
// supplies, or the real child. The fake travels as a field of the verb's options (or a
// parameter), never as a package variable, so no test can leave one armed for another.
func runOr(run execCommand) execCommand {
	if run != nil {
		return run
	}
	return realExecCommand
}

// exitCoder is the exit code of a child error: an *exec.ExitError from the real runner,
// or any error carrying ExitCode() from a scripted fake whose codes are the real tool's.
type exitCoder interface{ ExitCode() int }

// exitCodeOf reads a child error's exit code, or def when it carries none.
func exitCodeOf(err error, def int) int {
	var ec exitCoder
	if errors.As(err, &ec) {
		return ec.ExitCode()
	}
	return def
}

// childStderr is the stderr a child error carried, or "" when it carried none.
func childStderr(err error) string {
	var carried stderrCarrier
	if errors.As(err, &carried) {
		return carried.Stderr()
	}
	return ""
}

// sopsDecryptFailure names the class of a sops -d failure from the stderr the child wrote,
// with the one-turn remedy for each (STEP 3, "wrong key"; SPEC-SECRETS). The classes are
// the ones sops prints: a key that matches none of the recipients, an absent identity
// file, a file that carries no sops metadata, and a file no creation rule names. An
// unrecognised transcript returns "", which keeps the inspect remedy. The transcript is
// never returned: it can hold a value.
func sopsDecryptFailure(keyPath, filePath, stderr string) string {
	switch {
	case strings.Contains(stderr, "no identity matched"):
		return fmt.Sprintf("the key %s matches none of the recipients of %s; check the key path, or for a seat without one run: nova-secrets keygen", oneline.Field(keyPath), oneline.Field(filePath))
	case strings.Contains(stderr, "no identity file"):
		return fmt.Sprintf("the key file %s is absent or unreadable; check the key path, or for a seat without one run: nova-secrets keygen", oneline.Field(keyPath))
	case strings.Contains(stderr, "sops metadata not found"):
		return fmt.Sprintf("%s is not a sops-encrypted file (sops metadata not found); re-seal it with: nova-secrets seal", oneline.Field(filePath))
	case strings.Contains(stderr, "no matching creation rules"):
		return fmt.Sprintf("no creation rule in .sops.yaml matches %s; add one, then run: sops updatekeys %s", oneline.Field(filePath), oneline.Field(filePath))
	}
	return ""
}

// CheckSopsVersion probes the sops binary with --disable-version-check to prevent network calls.
func CheckSopsVersion(run execCommand, sopsPath string) (string, error) {
	if !filepathIsExecutable(sopsPath) {
		return "", fmt.Errorf("sops binary %s is absent or not executable; run: brew install sops", sopsPath)
	}

	out, err := runOr(run)(nil, []string{"PATH=/usr/bin:/bin"}, "", sopsPath, "--version", "--disable-version-check")
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
func CheckAgeKeygenVersion(run execCommand, ageKeygenPath string) (string, error) {
	if !filepathIsExecutable(ageKeygenPath) {
		return "", fmt.Errorf("age-keygen binary %s is absent or not executable; run: brew install age", ageKeygenPath)
	}

	out, err := runOr(run)(nil, []string{"PATH=/usr/bin:/bin"}, "", ageKeygenPath, "--version")
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
func DecryptFile(run execCommand, sopsPath, keyPath, filePath string) ([]byte, error) {
	tmpDir, err := os.MkdirTemp("", "nova-secrets-sops-*")
	if err != nil {
		return nil, fmt.Errorf("failed to create temporary isolation directory: %w", err)
	}
	defer func() { _ = safepath.RemoveUnder(os.TempDir(), tmpDir) }() // ignored: the temporary directory may already be gone

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

	out, err := runOr(run)(nil, cleanEnv, "", sopsPath, "-d", filePath)
	if err != nil {
		exitCode := exitCodeOf(err, 1)
		// The class sops's stderr names, when the seam carried it; the transcript is
		// never echoed (STEP 3, "wrong key").
		if cause := sopsDecryptFailure(keyPath, filePath, childStderr(err)); cause != "" {
			return nil, fmt.Errorf("sops failed: %s", cause)
		}
		// Sanitize sops error: do NOT pass raw stderr through
		return nil, fmt.Errorf("sops failed: exit %d (transcript withheld: run 'sops -d %s' to inspect)", exitCode, filePath)
	}

	return out, nil
}
