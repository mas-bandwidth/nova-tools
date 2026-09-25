package secrets

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// RunExec validates all preflight invariants, decrypts <store>/<as>.yaml,
// applies --only and --require filters, prints the OK line to stderr,
// sets RLIMIT_CORE to 0, and replaces the current process with cmdArgs.
func RunExec(storeDir, asName, keyPath, sopsPath, onlyArg string, required []string, cmdArgs []string) (int, error) {
	if len(cmdArgs) == 0 {
		return 125, fmt.Errorf("no command specified after '--'")
	}

	// 0. Set RLIMIT_CORE to 0 immediately (M5)
	if err := setRlimitCoreZero(); err != nil {
		return 125, fmt.Errorf("failed to set RLIMIT_CORE to 0: %w", err)
	}

	// Pre-validate command binary existence before printing OK (M4)
	if _, err := exec.LookPath(cmdArgs[0]); err != nil {
		return 125, fmt.Errorf("command not found: %s", cmdArgs[0])
	}

	var missing []string
	if storeDir == "" {
		missing = append(missing, "--store <dir>")
	}
	if asName == "" {
		missing = append(missing, "--as <name>")
	}
	if keyPath == "" {
		missing = append(missing, "--key <path>")
	}
	if sopsPath == "" {
		missing = append(missing, "--sops <path>")
	}
	if onlyArg == "" {
		missing = append(missing, "--only <names|all>")
	}
	if len(missing) > 0 {
		return 125, fmt.Errorf("missing required flags: %s; example: nova-secrets exec --store ./secrets --as rowan --key ~/.config/nova-secrets/rowan.key --sops /opt/homebrew/bin/sops --only GH_TOKEN -- gh api user",
			strings.Join(missing, ", "))
	}
	if !IsValidAsName(asName) {
		return 125, fmt.Errorf("invalid seat name %q: must match [A-Za-z0-9_-]+", asName)
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

	// 2. Invariant 8 git check and complete committed-artifact validation
	st, headBlobs, indexData, failures, refusal := ValidateAdmissibleStore(storeDir)
	if refusal != nil {
		return 125, refusal
	}
	if len(failures) > 0 {
		return 125, fmt.Errorf("store %s: %s", storeDir, failures[0].Reason)
	}
	headSHA := st.HeadSHA

	entries, err := os.ReadDir(storeDir)
	if err == nil {
		for _, e := range entries {
			if strings.HasSuffix(e.Name(), ".yaml") && e.Name() != ".sops.yaml" {
				if headBlobs != nil {
					if _, ok := headBlobs[e.Name()]; !ok {
						return 125, fmt.Errorf("uncommitted or untracked yaml file in store root: %s", e.Name())
					}
				}
				if indexData != nil {
					if _, ok := indexData.Entries[e.Name()]; !ok {
						return 125, fmt.Errorf("untracked yaml file in store root: %s", e.Name())
					}
				}
			}
		}
	}

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
		var missingOnly []string
		for _, p := range parts {
			p = strings.TrimSpace(p)
			if p == "" {
				continue
			}
			sec, ok := secretsMap[p]
			if !ok {
				missingOnly = append(missingOnly, p)
				continue
			}
			selectedSecrets[p] = sec
		}
		if len(missingOnly) > 0 {
			sort.Strings(missingOnly)
			return 125, fmt.Errorf("--only names key(s) not in %s: %s", targetFile, strings.Join(missingOnly, ", "))
		}
		onlyWord = strconv.Itoa(len(selectedSecrets))
	}

	// 9. Verify --require
	var missingRequired []string
	var excludedRequired []string
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
			excludedRequired = append(excludedRequired, req)
		}
	}
	if len(missingRequired) > 0 {
		sort.Strings(missingRequired)
		return 125, fmt.Errorf("required key(s) missing from %s: %s; run: sops %s",
			targetFile, strings.Join(missingRequired, ", "), targetFile)
	}
	if len(excludedRequired) > 0 {
		sort.Strings(excludedRequired)
		return 125, fmt.Errorf("--require %s is excluded by --only %q", strings.Join(excludedRequired, ", "), onlyArg)
	}

	// 10. Print OK line to stderr escaped via oneline.Field (H3)
	fmt.Fprintf(os.Stderr, "SECRETS EXEC OK as=%s keys=%d only=%s required=%d file=%s head=%s cmd=%s\n",
		oneline.Field(asName), len(selectedSecrets), oneline.Field(onlyWord), len(required),
		oneline.Field(targetFile), oneline.Field(headSHA), oneline.Field(cmdArgs[0]))

	// 11. Build environment dropping all store secrets (even if omitted by --only)
	// and SOPS_AGE_KEY / SOPS_AGE_KEY_FILE (H1, N4)
	scrubKeys := make(map[string]bool)
	for k := range secretsMap {
		scrubKeys[k] = true
	}
	scrubKeys["SOPS_AGE_KEY"] = true
	scrubKeys["SOPS_AGE_KEY_FILE"] = true

	var cleanEnv []string
	for _, envEntry := range os.Environ() {
		idx := strings.IndexByte(envEntry, '=')
		if idx == -1 {
			continue
		}
		envKey := envEntry[:idx]
		collides := false
		for sKey := range scrubKeys {
			if envKey == sKey || (runtime.GOOS == "windows" && strings.EqualFold(envKey, sKey)) {
				collides = true
				break
			}
		}
		if !collides {
			cleanEnv = append(cleanEnv, envEntry)
		}
	}
	env := cleanEnv
	for k, sec := range selectedSecrets {
		_ = sec.Use(func(val string) error {
			env = append(env, k+"="+val)
			return nil
		})
	}

	// 12. Replace process
	if err := replaceProcess(cmdArgs, env); err != nil {
		return 125, fmt.Errorf("exec failed: %w", err)
	}

	return 0, nil
}
