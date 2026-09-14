package secrets

import (
	"bufio"
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
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

		// Check recipients
		seenRecipients := make(map[string]bool)
		for _, rec := range rule.Recipients {
			if !IsValidAgePublicKey(rec) {
				failures = append(failures, CheckFailure{
					Kind:   "rule-shape",
					File:   ".sops.yaml",
					Reason: fmt.Sprintf("rule for %s recipient %q is not a valid age public key", label, rec),
				})
			}
			if seenRecipients[rec] {
				failures = append(failures, CheckFailure{
					Kind:   "rule-shape",
					File:   ".sops.yaml",
					Reason: fmt.Sprintf("rule for %s has duplicate recipient %s", label, rec),
				})
			}
			seenRecipients[rec] = true
		}

		if len(rule.Recipients) != 2 {
			failures = append(failures, CheckFailure{
				Kind:   "rule-shape",
				File:   ".sops.yaml",
				Reason: fmt.Sprintf("rule for %s has %d recipients; expected exactly 2 (one seat key and declared recovery key)", label, len(rule.Recipients)),
			})
		} else if recoveryKey != "" {
			hasRecovery := false
			for _, rec := range rule.Recipients {
				if rec == recoveryKey {
					hasRecovery = true
					break
				}
			}
			if !hasRecovery {
				failures = append(failures, CheckFailure{
					Kind:   "rule-shape",
					File:   ".sops.yaml",
					Reason: fmt.Sprintf("rule for %s does not contain declared recovery key %s", label, recoveryKey),
				})
			}
		}
	}

	return failures
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

		fileSet := make(map[string]bool)
		for _, r := range fileRecipients {
			fileSet[r] = true
		}
		ruleSet := make(map[string]bool)
		for _, r := range rule.Recipients {
			ruleSet[r] = true
		}

		differ := false
		if len(fileSet) != len(ruleSet) {
			differ = true
		} else {
			for r := range fileSet {
				if !ruleSet[r] {
					differ = true
					break
				}
			}
		}

		if differ {
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

		isMine := false
		for _, r := range fileRecipients {
			if r == seatPubKey {
				isMine = true
				break
			}
		}

		if isMine {
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

// CheckInvariant5 verifies that no private key is stored under storeDir.
func CheckInvariant5(storeDir, keyPath string) []CheckFailure {
	var failures []CheckFailure

	// Check if keyPath is inside storeDir
	absStore, errStore := filepath.Abs(storeDir)
	absKey, errKey := filepath.Abs(keyPath)
	if errStore == nil && errKey == nil {
		rel, err := filepath.Rel(absStore, absKey)
		if err == nil && !strings.HasPrefix(rel, "..") && rel != "." {
			failures = append(failures, CheckFailure{
				Kind:   "store-private-key",
				File:   keyPath,
				Reason: "key file is inside store directory",
			})
		}
	}

	_ = filepath.WalkDir(storeDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		data, err := os.ReadFile(path)
		if err == nil && bytes.Contains(data, []byte("AGE-SECRET-KEY-1")) {
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

	_ = filepath.WalkDir(storeDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
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
			return nil
		}
		defer f.Close()

		scanner := bufio.NewScanner(f)
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

		if hasPlaintext {
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

// CheckInvariant8 inspects .git directly for remote tracking ref sync.
func CheckInvariant8(storeDir string) (status GitRefStatus, failure *CheckFailure, refusal error) {
	st, err := CheckGitWorkingCopy(storeDir)
	if err != nil {
		msg := err.Error()
		if strings.Contains(msg, "detached HEAD") || strings.Contains(msg, "no upstream") || strings.Contains(msg, "worktree or submodule") || strings.Contains(msg, "is not a git repository") {
			return st, nil, err
		}
		return st, &CheckFailure{
			Kind:   "stale-working-copy",
			File:   "working copy",
			Reason: msg,
		}, nil
	}
	return st, nil, nil
}

// RunCheck executes the complete verification suite for 'check'.
func RunCheck(storeDir, asName, keyPath, sopsPath string, maxShown int) (okLine string, failLines []string, moreLines []string, summaryLine string, exitCode int, err error) {
	if maxShown < 0 {
		return "", nil, nil, "", 2, fmt.Errorf("--max %d is negative; expected non-negative integer", maxShown)
	}

	// 0. Set RLIMIT_CORE to 0 immediately (M5)
	if err := setRlimitCoreZero(); err != nil {
		return "", nil, nil, "", 2, fmt.Errorf("failed to set RLIMIT_CORE to 0: %w", err)
	}

	if asName == "" {
		return "", nil, nil, "", 2, fmt.Errorf("missing --as <name>")
	}
	if !IsValidAsName(asName) {
		return "", nil, nil, "", 2, fmt.Errorf("invalid seat name %q: must match [A-Za-z0-9_-]+", asName)
	}

	// 1. Refusal checks
	storeFi, err := os.Stat(storeDir)
	if err != nil || !storeFi.IsDir() {
		return "", nil, nil, "", 2, fmt.Errorf("store %s is not a directory", storeDir)
	}

	gitDir := filepath.Join(storeDir, ".git")
	gitFi, err := os.Stat(gitDir)
	if err != nil {
		return "", nil, nil, "", 2, fmt.Errorf("store %s has no .git directory; run: git clone <url> %s", storeDir, storeDir)
	}
	if !gitFi.IsDir() {
		return "", nil, nil, "", 2, fmt.Errorf("store %s: .git is a file (a worktree or submodule); expected a directory working copy", storeDir)
	}

	sopsConfigPath := filepath.Join(storeDir, ".sops.yaml")
	if _, err := os.Stat(sopsConfigPath); err != nil {
		return "", nil, nil, "", 2, fmt.Errorf("store %s carries no .sops.yaml", storeDir)
	}

	targetFile := filepath.Join(storeDir, asName+".yaml")
	if _, err := os.Stat(targetFile); err != nil {
		// List available files in store
		entries, _ := os.ReadDir(storeDir)
		var names []string
		for _, e := range entries {
			if strings.HasSuffix(e.Name(), ".yaml") && e.Name() != ".sops.yaml" {
				names = append(names, strings.TrimSuffix(e.Name(), ".yaml"))
			}
		}
		sort.Strings(names)
		return "", nil, nil, "", 2, fmt.Errorf("seat file %s.yaml is absent in store; available names: %s", asName, strings.Join(names, ", "))
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

	gitStatus, gitFail, gitRefusal := CheckInvariant8(storeDir)
	if gitRefusal != nil {
		return "", nil, nil, "", 2, gitRefusal
	}

	// 2. Invariant checks
	var allFailures []CheckFailure
	if gitFail != nil {
		allFailures = append(allFailures, *gitFail)
	}

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

	indexData, err := ReadGitIndex(storeDir)
	if err != nil {
		allFailures = append(allFailures, CheckFailure{
			Kind:   "stale-working-copy",
			File:   ".git/index",
			Reason: fmt.Sprintf("failed to read git index: %v", err),
		})
	} else {
		// Invariant 7: untracked plaintext
		trackedMap := make(map[string]bool, len(indexData.Entries))
		for k := range indexData.Entries {
			trackedMap[k] = true
		}
		inv7Fails := CheckInvariant7(storeDir, trackedMap)
		allFailures = append(allFailures, inv7Fails...)

		// Invariant 8 / working copy check: verify tracked *.yaml, .sops.yaml, recovery.pub match index blob SHA1
		for p := range indexData.Entries {
			if strings.HasSuffix(p, ".yaml") || p == "recovery.pub" {
				filePath := filepath.Join(storeDir, filepath.FromSlash(p))
				if _, err := os.Stat(filePath); err == nil {
					if err := VerifyFileMatchesIndex(storeDir, filePath, indexData); err != nil {
						allFailures = append(allFailures, CheckFailure{
							Kind:   "stale-working-copy",
							File:   p,
							Reason: "working copy differs from git index; uncommitted changes in store",
						})
					}
				}
			}
		}

		// Verify seat file is tracked in git index
		targetRel, _ := filepath.Rel(storeDir, targetFile)
		targetRel = filepath.Clean(filepath.ToSlash(targetRel))
		if _, ok := indexData.Entries[targetRel]; !ok {
			allFailures = append(allFailures, CheckFailure{
				Kind:   "stale-working-copy",
				File:   targetRel,
				Reason: fmt.Sprintf("seat file %s is untracked in git; commit it to the store", targetRel),
			})
		}
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
		okLine = fmt.Sprintf("SECRETS CHECK OK  as=%s recipients=%d files=%d sealed=%d mine=%d foreign=%d clear=%d head=%s",
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
