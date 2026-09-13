package secrets

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// RunExec validates all preflight invariants, decrypts <store>/<as>.yaml,
// applies --only and --require filters, prints the OK line to stderr,
// sets RLIMIT_CORE to 0, and replaces the current process with cmdArgs.
func RunExec(storeDir, asName, keyPath, sopsPath, onlyArg string, required []string, cmdArgs []string) (int, error) {
	if len(cmdArgs) == 0 {
		return 125, fmt.Errorf("no command specified after '--'")
	}
	if storeDir == "" {
		return 125, fmt.Errorf("missing --store <dir>")
	}
	if asName == "" {
		return 125, fmt.Errorf("missing --as <name>")
	}
	if keyPath == "" {
		return 125, fmt.Errorf("missing --key <path>")
	}
	if sopsPath == "" {
		return 125, fmt.Errorf("missing --sops <path>")
	}
	if onlyArg == "" {
		return 125, fmt.Errorf("missing --only <names|all>")
	}

	// 1. Store filesystem checks
	sFi, err := os.Stat(storeDir)
	if err != nil || !sFi.IsDir() {
		return 125, fmt.Errorf("store %s is not a directory", storeDir)
	}
	gitDir := filepath.Join(storeDir, ".git")
	gFi, err := os.Stat(gitDir)
	if err != nil || !gFi.IsDir() {
		return 125, fmt.Errorf("store %s has no .git directory", storeDir)
	}
	sopsConfigPath := filepath.Join(storeDir, ".sops.yaml")
	if _, err := os.Stat(sopsConfigPath); err != nil {
		return 125, fmt.Errorf("store %s carries no .sops.yaml", storeDir)
	}
	targetFile := filepath.Join(storeDir, asName+".yaml")
	if _, err := os.Stat(targetFile); err != nil {
		return 125, fmt.Errorf("store file %s is absent", targetFile)
	}

	// 2. Invariant 8 git check
	st, err := CheckGitWorkingCopy(storeDir)
	if err != nil {
		return 125, err
	}
	headSHA := st.HeadSHA

	// 3. Invariant 1 shape check (recovery.pub & .sops.yaml rules)
	recKey, err := ReadRecoveryPub(storeDir)
	if err != nil {
		return 125, fmt.Errorf("store %s: %w", storeDir, err)
	}
	sopsCfg, err := ParseSopsConfig(storeDir)
	if err != nil {
		return 125, fmt.Errorf("store %s: %w", storeDir, err)
	}
	inv1Fails := CheckInvariant1(storeDir, sopsCfg, recKey)
	if len(inv1Fails) > 0 {
		return 125, fmt.Errorf("store %s: %s", storeDir, inv1Fails[0].Reason)
	}

	// 4. Key file mode check
	if err := CheckInvariant6(keyPath); err != nil {
		return 125, err
	}

	// 5. Sops binary check
	if _, err := CheckSopsVersion(sopsPath); err != nil {
		return 125, err
	}

	// 6. Decrypt target file
	decData, err := DecryptFile(sopsPath, keyPath, targetFile)
	if err != nil {
		return 125, err
	}

	// 7. Parse decrypted secrets
	secretsMap, _, err := ParseDecryptedSecrets(decData)
	if err != nil {
		return 125, err
	}

	// 8. Apply --only
	selectedSecrets := make(map[string]Secret)
	onlyWord := ""
	if onlyArg == "all" {
		selectedSecrets = secretsMap
		onlyWord = "all"
	} else {
		parts := strings.Split(onlyArg, ",")
		for _, p := range parts {
			p = strings.TrimSpace(p)
			if p == "" {
				continue
			}
			sec, ok := secretsMap[p]
			if !ok {
				return 125, fmt.Errorf("--only names key %q which is not in %s", p, targetFile)
			}
			selectedSecrets[p] = sec
		}
		onlyWord = strconv.Itoa(len(selectedSecrets))
	}

	// 9. Verify --require
	var missingRequired []string
	for _, req := range required {
		req = strings.TrimSpace(req)
		if req == "" {
			continue
		}
		if _, ok := secretsMap[req]; !ok {
			missingRequired = append(missingRequired, req)
			continue
		}
		if _, ok := selectedSecrets[req]; !ok {
			return 125, fmt.Errorf("--require %q is excluded by --only %q", req, onlyArg)
		}
	}
	if len(missingRequired) > 0 {
		sort.Strings(missingRequired)
		return 125, fmt.Errorf("required key(s) missing from %s: %s; run: sops %s",
			targetFile, strings.Join(missingRequired, ", "), targetFile)
	}

	// 10. Print OK line to stderr
	fmt.Fprintf(os.Stderr, "SECRETS EXEC OK as=%s keys=%d only=%s required=%d file=%s head=%s cmd=%s\n",
		asName, len(selectedSecrets), onlyWord, len(required), targetFile, headSHA, cmdArgs[0])

	// 11. RLIMIT_CORE = 0
	if err := setRlimitCoreZero(); err != nil {
		return 125, fmt.Errorf("failed to set RLIMIT_CORE to 0: %w", err)
	}

	// 12. Build environment
	env := os.Environ()
	for k, sec := range selectedSecrets {
		_ = sec.Use(func(val string) error {
			env = append(env, k+"="+val)
			return nil
		})
	}

	// 13. Replace process
	if err := replaceProcess(cmdArgs, env); err != nil {
		return 125, fmt.Errorf("exec failed: %w", err)
	}

	return 0, nil
}
