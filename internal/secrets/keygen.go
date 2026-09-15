package secrets

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// RunKeygen generates a new age private key and formats the .sops.yaml rule block.
func RunKeygen(asName, keyPath, ageKeygenPath, storeDir string) (okLine string, ruleLines []string, noteLine string, err error) {
	if asName == "" {
		return "", nil, "", fmt.Errorf("missing --as <name>")
	}
	if !IsValidAsName(asName) {
		return "", nil, "", fmt.Errorf("invalid seat name %q: must match [A-Za-z0-9_-]+", asName)
	}
	if keyPath == "" {
		return "", nil, "", fmt.Errorf("missing --key <path>")
	}
	if ageKeygenPath == "" {
		return "", nil, "", fmt.Errorf("missing --age-keygen <path>")
	}

	// 1. Directory and file existence checks
	dir := filepath.Dir(keyPath)
	dirFi, err := os.Stat(dir)
	if err != nil {
		return "", nil, "", fmt.Errorf("key directory %s is absent; run: mkdir -m 700 -p %s", dir, dir)
	}
	if !dirFi.IsDir() {
		return "", nil, "", fmt.Errorf("key directory path %s is not a directory", dir)
	}
	if dirFi.Mode().Perm() != 0700 {
		return "", nil, "", fmt.Errorf("key directory %s mode is %04o; expected 0700; run: chmod 700 %s", dir, dirFi.Mode().Perm(), dir)
	}

	if _, err := os.Stat(keyPath); err == nil {
		return "", nil, "", fmt.Errorf("key file %s already exists; refusing to overwrite", keyPath)
	}

	// 2. Binary version probe
	if _, err := CheckAgeKeygenVersion(ageKeygenPath); err != nil {
		return "", nil, "", err
	}

	// 3. Store validation (if provided)
	recoveryKey := "<recovery key>"
	if storeDir != "" {
		sFi, err := os.Stat(storeDir)
		if err != nil || !sFi.IsDir() {
			return "", nil, "", fmt.Errorf("store %s is not a directory", storeDir)
		}
		gitDir := filepath.Join(storeDir, ".git")
		gFi, err := os.Stat(gitDir)
		if err != nil || !gFi.IsDir() {
			return "", nil, "", fmt.Errorf("store %s is not a git repository", storeDir)
		}
		sopsPath := filepath.Join(storeDir, ".sops.yaml")
		if _, err := os.Stat(sopsPath); err != nil {
			return "", nil, "", fmt.Errorf("store %s carries no .sops.yaml", storeDir)
		}

		recKey, err := ReadRecoveryPub(storeDir)
		if err != nil {
			return "", nil, "", fmt.Errorf("store %s: %w", storeDir, err)
		}
		recoveryKey = recKey
	} else {
		noteLine = "SECRETS RULE NOTE  placeholder: no --store, so <recovery key> stands unfilled"
	}

	// 4. Generate key
	cmd := exec.Command(ageKeygenPath, "-o", keyPath)
	cmd.Env = []string{"PATH=/usr/bin:/bin"}
	_, err = cmd.CombinedOutput()
	if err != nil {
		exitCode := 1
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		}
		return "", nil, "", fmt.Errorf("age-keygen failed: exit %d (transcript withheld)", exitCode)
	}
	if err := os.Chmod(keyPath, 0600); err != nil {
		return "", nil, "", fmt.Errorf("failed to chmod 0600 %s: %w", keyPath, err)
	}

	// 5. Read back public key
	pubKey, err := ExtractPublicKeyFromKeyFile(keyPath)
	if err != nil {
		return "", nil, "", err
	}

	okLine = fmt.Sprintf("SECRETS KEYGEN OK as=%s key=%s mode=0600 pub=%s",
		oneline.Field(asName), oneline.Field(keyPath), oneline.Field(pubKey))
	ruleLines = []string{
		"SECRETS RULE   creation_rules:",
		fmt.Sprintf("SECRETS RULE     - path_regex: ^%s\\.yaml$", regexp.QuoteMeta(asName)),
		fmt.Sprintf("SECRETS RULE       age: %s,%s", pubKey, recoveryKey),
	}

	return okLine, ruleLines, noteLine, nil
}
