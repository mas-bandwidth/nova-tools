package secrets

import (
	"bufio"
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// ExtractPublicKeyFromKeyFile parses '# public key: (age1...)' from an age private key file.
func ExtractPublicKeyFromKeyFile(keyPath string) (string, error) {
	f, err := os.Open(keyPath)
	if err != nil {
		return "", err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	re := regexp.MustCompile(`^#\s*public key:\s*(age1[a-z0-9]+)`)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		match := re.FindStringSubmatch(line)
		if len(match) > 1 {
			return match[1], nil
		}
	}
	return "", fmt.Errorf("public key comment missing in %s; run: age-keygen -y %s", keyPath, keyPath)
}

// CheckInvariant1 checks .sops.yaml syntax and shape against the declared recovery key.
func CheckInvariant1(storeDir string, sopsCfg *SopsConfig, recoveryKey string) []CheckFailure {
	var failures []CheckFailure
	if sopsCfg == nil || len(sopsCfg.CreationRules) == 0 {
		failures = append(failures, CheckFailure{
			Kind:   "rule-shape",
			File:   ".sops.yaml",
			Reason: "carries no creation_rules",
		})
		return failures
	}

	for _, rule := range sopsCfg.CreationRules {
		label := rule.PathRegex
		if label == "" {
			label = "unnamed"
		}

		if !strings.HasPrefix(rule.PathRegex, "^") || !strings.HasSuffix(rule.PathRegex, "$") {
			failures = append(failures, CheckFailure{
				Kind:   "rule-shape",
				File:   ".sops.yaml",
				Reason: fmt.Sprintf("rule path_regex %q is not anchored at both ends (^...$)", rule.PathRegex),
			})
		} else if _, err := regexp.Compile(rule.PathRegex); err != nil {
			failures = append(failures, CheckFailure{
				Kind:   "rule-shape",
				File:   ".sops.yaml",
				Reason: fmt.Sprintf("rule path_regex %q is not a valid regular expression: %v", rule.PathRegex, err),
			})
		}

		// Check non-age recipients
		for _, nonAge := range rule.NonAgeRecipients {
			failures = append(failures, CheckFailure{
				Kind:   "rule-shape",
				File:   ".sops.yaml",
				Reason: fmt.Sprintf("rule for %s carries non-age recipient key %q; only age recipients are permitted", label, nonAge),
			})
		}

		for _, rec := range rule.Recipients {
			if !IsValidAgePublicKey(rec) {
				failures = append(failures, CheckFailure{
					Kind:   "rule-shape",
					File:   ".sops.yaml",
					Reason: fmt.Sprintf("rule for %s recipient %q is not a valid age public key", label, rec),
				})
			}
		}
		if problem := ruleRecipientsProblem(rule.Recipients, recoveryKey); problem != "" {
			failures = append(failures, CheckFailure{
				Kind:   "rule-shape",
				File:   ".sops.yaml",
				Reason: fmt.Sprintf("rule for %s %s", label, problem),
			})
		}
	}

	return failures
}

// ruleRecipientsProblem is the one judgement of a seat's recipients: exactly two,
// distinct, one of them the declared recovery key, so the other is the seat's own key.
// check (CheckInvariant1) and the store's gate (RunGate) hold a creation rule's
// recipients to it, seat inject (seatInjectTarget) the seat file's own, and seat add
// (RunSeatAdd) the rule it is about to write. It returns why they are not, worded to
// follow its subject ("rule ...", "seat file ..."), or "" when they are. An empty
// recoveryKey is one check could not read (recovery.pub is reported on its own), and
// the recovery key is then not looked for.
func ruleRecipientsProblem(recipients []string, recoveryKey string) string {
	if len(recipients) != 2 {
		return fmt.Sprintf("has %d recipients; expected exactly 2 (one seat key and declared recovery key)", len(recipients))
	}
	if recipients[0] == recipients[1] {
		return fmt.Sprintf("has duplicate recipient %s; a seat is one seat key and the declared recovery key, distinct", recipients[0])
	}
	if recoveryKey != "" && !slices.Contains(recipients, recoveryKey) {
		return fmt.Sprintf("does not contain declared recovery key %s (recovery.pub)", recoveryKey)
	}
	return ""
}

// CheckInvariant2 verifies that each file's sops recipient block matches its creation rule.
func CheckInvariant2(storeDir string, sopsCfg *SopsConfig, files []string) []CheckFailure {
	var failures []CheckFailure
	for _, file := range files {
		rule, err := FindMatchingRule(sopsCfg, file)
		if err != nil {
			failures = append(failures, CheckFailure{
				Kind:   "recipients-drift",
				File:   file,
				Reason: fmt.Sprintf("recipients differ from .sops.yaml; run: sops updatekeys %s", file),
			})
			continue
		}

		filePath := filepath.Join(storeDir, file)
		_, fileRecipients, _, err := ParseStoreFileWithoutDecrypting(filePath)
		if err != nil {
			failures = append(failures, CheckFailure{
				Kind:   "recipients-drift",
				File:   file,
				Reason: fmt.Sprintf("unable to parse file: %v", err),
			})
			continue
		}

		if !sameKeySet(fileRecipients, rule.Recipients) {
			failures = append(failures, CheckFailure{
				Kind:   "recipients-drift",
				File:   file,
				Reason: fmt.Sprintf("recipients differ from .sops.yaml; run: sops updatekeys %s", file),
			})
		}
	}
	return failures
}

// CheckInvariant3 verifies that every file is sealed and only permitted keys are in the clear.
func CheckInvariant3(storeDir string, sopsCfg *SopsConfig, files []string) (failures []CheckFailure, sealedCount int, clearCount int) {
	for _, file := range files {
		filePath := filepath.Join(storeDir, file)
		keys, _, hasSops, err := ParseStoreFileWithoutDecrypting(filePath)
		if err != nil || !hasSops {
			failures = append(failures, CheckFailure{
				Kind:   "unsealed",
				File:   file,
				Reason: "file is not sealed (missing sops metadata)",
			})
			continue
		}

		var unencRe *regexp.Regexp
		rule, _ := FindMatchingRule(sopsCfg, file)
		if rule != nil && rule.UnencryptedRegex != "" {
			unencRe, _ = regexp.Compile(rule.UnencryptedRegex)
		}

		fileUnsealed := false
		for _, k := range keys {
			if unencRe != nil && unencRe.MatchString(k.Name) {
				clearCount++
			} else if k.Clear {
				fileUnsealed = true
				failures = append(failures, CheckFailure{
					Kind:   "unsealed",
					File:   file,
					Reason: fmt.Sprintf("unencrypted key %s outside unencrypted_regex", k.Name),
				})
			}
		}

		if !fileUnsealed {
			sealedCount++
		}
	}
	return failures, sealedCount, clearCount
}

// CheckInvariant4 tests that the seat key opens exactly the files listing its public key.
func CheckInvariant4(storeDir, sopsPath, keyPath, seatPubKey string, files []string) (failures []CheckFailure, mineCount int, foreignCount int) {
	for _, file := range files {
		filePath := filepath.Join(storeDir, file)
		_, fileRecipients, _, err := ParseStoreFileWithoutDecrypting(filePath)
		if err != nil {
			continue
		}

		if slices.Contains(fileRecipients, seatPubKey) {
			mineCount++
			_, decErr := DecryptFile(sopsPath, keyPath, filePath)
			if decErr != nil {
				failures = append(failures, CheckFailure{
					Kind:   "decrypt-failed",
					File:   file,
					Reason: "file should open with this key and does not",
				})
			}
		} else {
			foreignCount++
			_, decErr := DecryptFile(sopsPath, keyPath, filePath)
			if decErr == nil {
				failures = append(failures, CheckFailure{
					Kind:   "foreign-openable",
					File:   file,
					Reason: "file opened with this key but recipients do not list it",
				})
			}
		}
	}
	return failures, mineCount, foreignCount
}

// resolveLoose is the absolute path of p with symlinks resolved, where p need not exist:
// the deepest existing ancestor is resolved and the missing tail is appended to it.
func resolveLoose(p string) (string, error) {
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", err
	}
	tail := ""
	for cur := abs; ; cur = filepath.Dir(cur) {
		if real, err := filepath.EvalSymlinks(cur); err == nil {
			return filepath.Join(real, tail), nil
		}
		if filepath.Dir(cur) == cur {
			return abs, nil
		}
		tail = filepath.Join(filepath.Base(cur), tail)
	}
}

// CheckInvariant5 verifies that no private key is stored under storeDir.
func CheckInvariant5(storeDir, keyPath string) []CheckFailure {
	var failures []CheckFailure

	// Check if keyPath is inside storeDir
	// Both paths are resolved through symlinks first: a key reached through a link, or a
	// store named through one, is inside the store by where it lands, not by how it is spelled.
	absStore, errStore := resolveLoose(storeDir)
	absKey, errKey := resolveLoose(keyPath)
	if errStore == nil && errKey == nil {
		rel, err := filepath.Rel(absStore, absKey)
		// filepath.IsLocal, not a ".." prefix test: a key file under a
		// directory named "..cache" is inside the store.
		if err == nil && rel != "." && filepath.IsLocal(rel) {
			failures = append(failures, CheckFailure{
				Kind:   "store-private-key",
				File:   keyPath,
				Reason: "key file is inside store directory",
			})
		}
	}

	// ignored: the callback returns nil for every path, and records what it cannot read as a failure
	_ = filepath.WalkDir(storeDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			// Never silent: a path the walk cannot read was not checked, and a
			// pass over it would read as "no private key here".
			failures = append(failures, unreadableFailure("store-private-key", storeDir, path, err))
			return nil
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			failures = append(failures, unreadableFailure("store-private-key", storeDir, path, err))
			return nil
		}
		if bytes.Contains(data, []byte("AGE-SECRET-KEY-1")) {
			rel, _ := filepath.Rel(storeDir, path)
			failures = append(failures, CheckFailure{
				Kind:   "store-private-key",
				File:   rel,
				Reason: "private key found in store",
			})
		}
		return nil
	})

	return failures
}

// unreadableFailure is a path an invariant could not read, as a failure of that
// invariant: a check that skipped a file has not cleared it.
func unreadableFailure(kind, storeDir, path string, err error) CheckFailure {
	rel, relErr := filepath.Rel(storeDir, path)
	if relErr != nil {
		rel = path
	}
	return CheckFailure{
		Kind:   kind,
		File:   rel,
		Reason: fmt.Sprintf("could not be read, so it was not checked: %v; fix its permissions and rerun the check", err),
	}
}

// CheckInvariant6 verifies that the key file mode is 0600 and directory is 0700.
func CheckInvariant6(keyPath string) error {
	fi, err := os.Stat(keyPath)
	if err != nil {
		return fmt.Errorf("key file %s: %w", keyPath, err)
	}
	if fi.Mode().Perm() != 0600 {
		return fmt.Errorf("key file %s mode is %04o; expected 0600; run: chmod 600 %s", keyPath, fi.Mode().Perm(), keyPath)
	}

	dir := filepath.Dir(keyPath)
	dirFi, err := os.Stat(dir)
	if err != nil {
		return fmt.Errorf("key directory %s: %w", dir, err)
	}
	if dirFi.Mode().Perm() != 0700 {
		return fmt.Errorf("key directory %s mode is %04o; expected 0700; run: chmod 700 %s", dir, dirFi.Mode().Perm(), dir)
	}
	return nil
}

// CheckInvariant7 verifies that untracked files hold no plaintext secrets.
func CheckInvariant7(storeDir string, trackedFiles map[string]bool) []CheckFailure {
	var failures []CheckFailure

	envLineRegex := regexp.MustCompile(`^[A-Z][A-Z0-9_]*:\s*(.*)$`)

	// ignored: the callback returns nil for every path, and records what it cannot read as a failure
	_ = filepath.WalkDir(storeDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			failures = append(failures, unreadableFailure("untracked-plaintext", storeDir, path, err))
			return nil
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}

		rel, err := filepath.Rel(storeDir, path)
		if err != nil {
			return nil
		}
		rel = filepath.Clean(rel)

		if trackedFiles[rel] {
			return nil
		}

		f, err := os.Open(path)
		if err != nil {
			failures = append(failures, unreadableFailure("untracked-plaintext", storeDir, path, err))
			return nil
		}
		defer f.Close()

		scanner := bufio.NewScanner(f)
		scanner.Buffer(make([]byte, 64*1024), 1024*1024)
		hasPlaintext := false
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			match := envLineRegex.FindStringSubmatch(line)
			if len(match) > 1 {
				val := strings.TrimSpace(match[1])
				if !strings.HasPrefix(val, "ENC[") {
					hasPlaintext = true
					break
				}
			}
		}

		if err := scanner.Err(); err != nil {
			failures = append(failures, CheckFailure{
				Kind:   "untracked-plaintext",
				File:   rel,
				Reason: fmt.Sprintf("untracked file cannot be scanned: %v", err),
			})
		} else if hasPlaintext {
			failures = append(failures, CheckFailure{
				Kind:   "untracked-plaintext",
				File:   rel,
				Reason: "untracked file contains unencrypted secret",
			})
		}

		return nil
	})

	return failures
}

// RunCheck executes the complete verification suite for 'check'.
func RunCheck(storeDir, asName, keyPath, sopsPath string, maxShown int) (okLine string, failLines []string, moreLines []string, summaryLine string, exitCode int, err error) {
	if maxShown < 0 {
		return "", nil, nil, "", 2, fmt.Errorf("--max %d is negative; expected non-negative integer", maxShown)
	}

	// No core file: a crash after a decrypt must not write a value to disk.
	if err := setRlimitCoreZero(); err != nil {
		return "", nil, nil, "", 2, fmt.Errorf("failed to set RLIMIT_CORE to 0: %w", err)
	}

	// 1. Refusal checks: the invocation and the store's shape, every problem at once
	if err := preflight(storeDir, need{storeDir, "--store <dir>", false}, need{asName, "--as <name>", true}, need{keyPath, "--key <path>", false}, need{sopsPath, "--sops <path>", false}); err != nil {
		return "", nil, nil, "", 2, err
	}

	targetFile := filepath.Join(storeDir, asName+".yaml")
	if _, err := os.Stat(targetFile); err != nil {
		return "", nil, nil, "", 2, seatAbsent(storeDir, asName)
	}

	if err := CheckInvariant6(keyPath); err != nil {
		return "", nil, nil, "", 2, err
	}

	seatPubKey, err := ExtractPublicKeyFromKeyFile(keyPath)
	if err != nil {
		return "", nil, nil, "", 2, err
	}

	if _, err := CheckSopsVersion(sopsPath); err != nil {
		return "", nil, nil, "", 2, err
	}

	gitStatus, _, indexData, storeFailures, gitRefusal := ValidateAdmissibleStore(storeDir)
	if gitRefusal != nil {
		return "", nil, nil, "", 2, gitRefusal
	}

	// 2. Invariant checks
	var allFailures []CheckFailure
	allFailures = append(allFailures, storeFailures...)

	recoveryKey, recErr := ReadRecoveryPub(storeDir)
	if recErr != nil {
		allFailures = append(allFailures, CheckFailure{
			Kind:   "rule-shape",
			File:   "recovery.pub",
			Reason: recErr.Error(),
		})
	}

	sopsCfg, sopsErr := ParseSopsConfig(storeDir)
	if sopsErr != nil {
		allFailures = append(allFailures, CheckFailure{
			Kind:   "rule-shape",
			File:   ".sops.yaml",
			Reason: sopsErr.Error(),
		})
	} else {
		inv1Fails := CheckInvariant1(storeDir, sopsCfg, recoveryKey)
		allFailures = append(allFailures, inv1Fails...)
	}

	inv5Fails := CheckInvariant5(storeDir, keyPath)
	allFailures = append(allFailures, inv5Fails...)

	if indexData != nil {
		// Invariant 7: untracked plaintext
		trackedMap := make(map[string]bool, len(indexData.Entries))
		for k := range indexData.Entries {
			trackedMap[k] = true
		}
		inv7Fails := CheckInvariant7(storeDir, trackedMap)
		allFailures = append(allFailures, inv7Fails...)
	}

	// List tracked *.yaml files across the store (including subdirectories, excluding .sops.yaml)
	var yamlFiles []string
	if indexData != nil {
		for p := range indexData.Entries {
			if strings.HasSuffix(p, ".yaml") && filepath.Base(p) != ".sops.yaml" {
				yamlFiles = append(yamlFiles, p)
			}
		}
	} else {
		entries, _ := os.ReadDir(storeDir)
		for _, e := range entries {
			if strings.HasSuffix(e.Name(), ".yaml") && e.Name() != ".sops.yaml" {
				yamlFiles = append(yamlFiles, e.Name())
			}
		}
	}
	sort.Strings(yamlFiles)

	if sopsCfg != nil {
		inv2Fails := CheckInvariant2(storeDir, sopsCfg, yamlFiles)
		allFailures = append(allFailures, inv2Fails...)
	}

	inv3Fails, sealedCount, clearCount := CheckInvariant3(storeDir, sopsCfg, yamlFiles)
	allFailures = append(allFailures, inv3Fails...)

	inv4Fails, mineCount, foreignCount := CheckInvariant4(storeDir, sopsPath, keyPath, seatPubKey, yamlFiles)
	allFailures = append(allFailures, inv4Fails...)

	// Count unique recipients across rules
	recipientsSet := make(map[string]bool)
	if sopsCfg != nil {
		for _, rule := range sopsCfg.CreationRules {
			for _, r := range rule.Recipients {
				recipientsSet[r] = true
			}
		}
	}
	recipientsCount := len(recipientsSet)

	if len(allFailures) == 0 {
		okLine = fmt.Sprintf("SECRETS CHECK OK as=%s recipients=%d files=%d sealed=%d mine=%d foreign=%d clear=%d head=%s",
			oneline.Field(asName), recipientsCount, len(yamlFiles), sealedCount, mineCount, foreignCount, clearCount, oneline.Field(gitStatus.HeadSHA))
		return okLine, nil, nil, "", 0, nil
	}

	// Group failures by kind and cap per kind
	grouped := make(map[string][]CheckFailure)
	for _, f := range allFailures {
		grouped[f.Kind] = append(grouped[f.Kind], f)
	}

	kindOrder := []string{
		"rule-shape",
		"recipients-drift",
		"unsealed",
		"decrypt-failed",
		"foreign-openable",
		"store-private-key",
		"untracked-plaintext",
		"stale-working-copy",
	}

	totalShown := 0
	for _, kind := range kindOrder {
		fails, ok := grouped[kind]
		if !ok {
			continue
		}
		totalInKind := len(fails)
		shownInKind := totalInKind
		if maxShown > 0 && totalInKind > maxShown {
			shownInKind = maxShown
		}

		for i := 0; i < shownInKind; i++ {
			failLines = append(failLines, fmt.Sprintf("SECRETS CHECK FAIL %s: %s", oneline.Escape(fails[i].File), oneline.Escape(fails[i].Reason)))
			totalShown++
		}

		if maxShown > 0 && totalInKind > maxShown {
			moreLines = append(moreLines, fmt.Sprintf("SECRETS CHECK MORE kind=%s shown=%d total=%d run: nova-secrets check --store %s --as %s --key %s --sops %s --max 0",
				oneline.Field(kind), shownInKind, totalInKind, oneline.Field(storeDir), oneline.Field(asName), oneline.Field(keyPath), oneline.Field(sopsPath)))
		}
	}

	summaryLine = fmt.Sprintf("SECRETS CHECK FAIL as=%s files=%d failed=%d shown=%d",
		oneline.Field(asName), len(yamlFiles), len(allFailures), totalShown)
	return "", failLines, moreLines, summaryLine, 1, nil
}
