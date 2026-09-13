package secrets

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

var envVarRegex = regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)

// IsValidAgePublicKey verifies an age public key string (bech32).
func IsValidAgePublicKey(s string) bool {
	if len(s) != 62 || !strings.HasPrefix(s, "age1") {
		return false
	}
	for _, r := range s[4:] {
		if (r >= '0' && r <= '9' && r != '1') ||
			(r >= 'a' && r <= 'z' && r != 'b' && r != 'i' && r != 'o') {
			continue
		}
		return false
	}
	return true
}

// IsValidEnvVar verifies an environment variable name: ^[A-Z][A-Z0-9_]*$
func IsValidEnvVar(s string) bool {
	return envVarRegex.MatchString(s)
}

// ReadRecoveryPub reads and validates the recovery.pub file at store root.
func ReadRecoveryPub(storeDir string) (string, error) {
	path := filepath.Join(storeDir, "recovery.pub")
	fi, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("recovery.pub is absent")
		}
		return "", fmt.Errorf("recovery.pub is unreadable: %w", err)
	}
	if fi.IsDir() {
		return "", fmt.Errorf("recovery.pub is a directory; expected a file")
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("recovery.pub is unreadable: %w", err)
	}

	trimmed := strings.TrimRight(string(data), "\r\n")
	if len(strings.TrimSpace(trimmed)) == 0 {
		return "", fmt.Errorf("recovery.pub is empty")
	}

	lines := strings.Split(trimmed, "\n")
	if len(lines) > 1 {
		return "", fmt.Errorf("recovery.pub contains multiple lines; expected exactly one recovery public key")
	}

	key := strings.TrimSpace(lines[0])
	if !IsValidAgePublicKey(key) {
		return "", fmt.Errorf("recovery.pub contains invalid age public key: %q", key)
	}

	return key, nil
}

// ParseSopsConfig parses creation rules from .sops.yaml in storeDir.
func ParseSopsConfig(storeDir string) (*SopsConfig, error) {
	configPath := filepath.Join(storeDir, ".sops.yaml")
	f, err := os.Open(configPath)
	if err != nil {
		return nil, fmt.Errorf(".sops.yaml is absent or unreadable: %w", err)
	}
	defer f.Close()

	var cfg SopsConfig
	scanner := bufio.NewScanner(f)
	var currentRule *CreationRule
	inCreationRules := false
	inAgeBlock := false

	flushRule := func() {
		if currentRule != nil {
			cfg.CreationRules = append(cfg.CreationRules, *currentRule)
			currentRule = nil
		}
	}

	lineNum := 0
	for scanner.Scan() {
		lineNum++
		raw := scanner.Text()
		trimmed := strings.TrimSpace(raw)
		if strings.HasPrefix(trimmed, "#") || trimmed == "" {
			continue
		}

		if strings.HasPrefix(trimmed, "creation_rules:") {
			inCreationRules = true
			continue
		}

		if !inCreationRules {
			continue
		}

		// Check for new rule item: '  - path_regex:' or '  -'
		if strings.HasPrefix(trimmed, "- path_regex:") {
			flushRule()
			inAgeBlock = false
			val := strings.TrimSpace(strings.TrimPrefix(trimmed, "- path_regex:"))
			val = strings.Trim(val, `"'`)
			currentRule = &CreationRule{PathRegex: val}
			continue
		} else if strings.HasPrefix(trimmed, "-") && (len(trimmed) == 1 || trimmed[1] == ' ') {
			flushRule()
			inAgeBlock = false
			currentRule = &CreationRule{}
			rest := strings.TrimSpace(strings.TrimPrefix(trimmed, "-"))
			if strings.HasPrefix(rest, "path_regex:") {
				val := strings.TrimSpace(strings.TrimPrefix(rest, "path_regex:"))
				val = strings.Trim(val, `"'`)
				currentRule.PathRegex = val
			}
			continue
		}

		if currentRule == nil {
			continue
		}

		if strings.HasPrefix(trimmed, "path_regex:") {
			inAgeBlock = false
			val := strings.TrimSpace(strings.TrimPrefix(trimmed, "path_regex:"))
			val = strings.Trim(val, `"'`)
			currentRule.PathRegex = val
			continue
		}

		if strings.HasPrefix(trimmed, "unencrypted_regex:") {
			inAgeBlock = false
			val := strings.TrimSpace(strings.TrimPrefix(trimmed, "unencrypted_regex:"))
			val = strings.Trim(val, `"'`)
			currentRule.UnencryptedRegex = val
			continue
		}

		if strings.HasPrefix(trimmed, "age:") {
			val := strings.TrimSpace(strings.TrimPrefix(trimmed, "age:"))
			val = strings.Trim(val, `"'`)
			if val == ">-" || val == "|" || val == "" {
				inAgeBlock = true
			} else {
				inAgeBlock = false
				parts := strings.Split(val, ",")
				for _, p := range parts {
					p = strings.TrimSpace(p)
					if p != "" {
						currentRule.Recipients = append(currentRule.Recipients, p)
					}
				}
			}
			continue
		}

		if inAgeBlock {
			// If line starts with another key or rule, stop inAgeBlock
			if strings.Contains(trimmed, ":") && !strings.HasPrefix(trimmed, "-") {
				inAgeBlock = false
				// Reprocess line outside age block
			} else {
				cleanLine := strings.TrimPrefix(trimmed, "-")
				cleanLine = strings.TrimSpace(cleanLine)
				parts := strings.Split(cleanLine, ",")
				for _, p := range parts {
					p = strings.TrimSpace(p)
					if p != "" {
						currentRule.Recipients = append(currentRule.Recipients, p)
					}
				}
				continue
			}
		}
	}

	flushRule()

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("error reading .sops.yaml: %w", err)
	}

	if !inCreationRules || len(cfg.CreationRules) == 0 {
		return nil, fmt.Errorf(".sops.yaml carries no creation_rules")
	}

	return &cfg, nil
}

// FindMatchingRule locates the creation rule that governs filePath.
func FindMatchingRule(cfg *SopsConfig, relPath string) (*CreationRule, error) {
	base := filepath.Base(relPath)
	for i := range cfg.CreationRules {
		r := &cfg.CreationRules[i]
		if r.PathRegex == "" {
			continue
		}
		re, err := regexp.Compile(r.PathRegex)
		if err != nil {
			continue
		}
		if re.MatchString(relPath) || re.MatchString(base) {
			return r, nil
		}
	}
	return nil, fmt.Errorf("no creation rule in .sops.yaml matches %s", relPath)
}

// StoreFileKey represents a top-level key and clear status in a store file.
type StoreFileKey struct {
	Name  string
	Value string
	Clear bool
}

// ParseStoreFileWithoutDecrypting extracts keys, clear status, and recipients from a sealed yaml.
func ParseStoreFileWithoutDecrypting(filePath string) (keys []StoreFileKey, recipients []string, hasSops bool, err error) {
	f, err := os.Open(filePath)
	if err != nil {
		return nil, nil, false, err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	inSops := false
	inAge := false

	recipientRegex := regexp.MustCompile(`recipient:\s*([a-z0-9]+)`)

	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") || trimmed == "" {
			continue
		}

		if !inSops {
			// Check if we hit top-level sops:
			if strings.HasPrefix(line, "sops:") {
				inSops = true
				hasSops = true
				continue
			}

			// Must be at root indentation (no leading space)
			if !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "\t") && strings.Contains(line, ":") {
				parts := strings.SplitN(line, ":", 2)
				keyName := strings.TrimSpace(parts[0])
				val := strings.TrimSpace(parts[1])
				isClear := !strings.HasPrefix(val, "ENC[")
				keys = append(keys, StoreFileKey{
					Name:  keyName,
					Value: val,
					Clear: isClear,
				})
			}
			continue
		}

		// Inside sops block
		if strings.HasPrefix(trimmed, "age:") {
			inAge = true
			continue
		}

		if inAge {
			match := recipientRegex.FindStringSubmatch(trimmed)
			if len(match) > 1 {
				recipients = append(recipients, match[1])
			}
		}
	}

	return keys, recipients, hasSops, scanner.Err()
}

// ParseDecryptedSecrets parses the output of 'sops -d' into Secret values.
// Rejects multi-line values, NUL bytes, and illegal variable names.
func ParseDecryptedSecrets(data []byte) (map[string]Secret, []string, error) {
	if bytes.Contains(data, []byte{0}) {
		return nil, nil, fmt.Errorf("decrypted secret contains NUL byte")
	}

	scanner := bufio.NewScanner(bytes.NewReader(data))
	secrets := make(map[string]Secret)
	var keys []string
	var badKeys []string
	var multilineKeys []string

	var currentKey string
	var currentValue strings.Builder
	isMultiLine := false

	flushCurrent := func() {
		if currentKey != "" {
			if isMultiLine {
				multilineKeys = append(multilineKeys, currentKey)
			} else {
				val := currentValue.String()
				val = strings.Trim(val, `"'`)
				secrets[currentKey] = NewSecret(val)
				keys = append(keys, currentKey)
			}
			currentKey = ""
			currentValue.Reset()
			isMultiLine = false
		}
	}

	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") || trimmed == "" {
			continue
		}

		// Check for root level key
		if !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "\t") && strings.Contains(line, ":") {
			flushCurrent()

			parts := strings.SplitN(line, ":", 2)
			k := strings.TrimSpace(parts[0])
			v := strings.TrimSpace(parts[1])

			if !IsValidEnvVar(k) {
				badKeys = append(badKeys, k)
				continue
			}

			if v == "|" || v == ">" || v == "|-" || v == ">-" {
				currentKey = k
				isMultiLine = true
				continue
			}

			// Single line value
			currentKey = k
			currentValue.WriteString(v)
			continue
		}

		// Indented line continuing a value
		if currentKey != "" {
			isMultiLine = true
		}
	}
	flushCurrent()

	if len(badKeys) > 0 {
		sort.Strings(badKeys)
		return nil, nil, fmt.Errorf("illegal environment variable name(s): %s", strings.Join(badKeys, ", "))
	}

	if len(multilineKeys) > 0 {
		sort.Strings(multilineKeys)
		return nil, nil, fmt.Errorf("key=%s: value is multi-line; a file-shaped secret is not an environment variable.\n  generate it where it is used: this store holds no file-shaped secrets.", multilineKeys[0])
	}

	sort.Strings(keys)
	return secrets, keys, nil
}

// ReadGitIndexTrackedFiles reads .git/index directly to find all tracked files.
func ReadGitIndexTrackedFiles(storeDir string) (map[string]bool, error) {
	indexPath := filepath.Join(storeDir, ".git", "index")
	data, err := os.ReadFile(indexPath)
	if err != nil {
		return nil, fmt.Errorf("unable to read .git/index: %w", err)
	}

	if len(data) < 12 || string(data[:4]) != "DIRC" {
		return nil, fmt.Errorf("invalid git index header in %s", indexPath)
	}

	version := binary.BigEndian.Uint32(data[4:8])
	entries := binary.BigEndian.Uint32(data[8:12])
	tracked := make(map[string]bool, entries)

	if version == 2 || version == 3 {
		offset := 12
		for i := uint32(0); i < entries && offset+62 <= len(data); i++ {
			entryStart := offset
			nameStart := entryStart + 62
			nameEnd := bytes.IndexByte(data[nameStart:], 0)
			if nameEnd == -1 {
				break
			}
			path := string(data[nameStart : nameStart+nameEnd])
			tracked[filepath.Clean(path)] = true

			entryLen := ((62 + nameEnd + 8) / 8) * 8
			offset = entryStart + entryLen
		}
		return tracked, nil
	}

	// For version 4 or other, fallback: walk index entries if possible or return error
	return nil, fmt.Errorf("unsupported git index version %d", version)
}
