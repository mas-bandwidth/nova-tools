package secrets

import (
	"bufio"
	"bytes"
	"crypto/sha1"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
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
	return parseSopsConfig(f)
}

// parseSopsConfig parses creation rules from any reader: a store file or a git blob.
func parseSopsConfig(f io.Reader) (*SopsConfig, error) {
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

		for _, nonAgeKey := range []string{"pgp:", "kms:", "gcp_kms:", "azure_kv:", "hc_vault:"} {
			if strings.HasPrefix(trimmed, nonAgeKey) {
				currentRule.NonAgeRecipients = append(currentRule.NonAgeRecipients, strings.TrimSuffix(nonAgeKey, ":"))
				break
			}
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
	cleanRel := filepath.ToSlash(filepath.Clean(relPath))
	for i := range cfg.CreationRules {
		r := &cfg.CreationRules[i]
		if r.PathRegex == "" {
			continue
		}
		re, err := regexp.Compile(r.PathRegex)
		if err != nil {
			continue
		}
		if re.MatchString(cleanRel) {
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

		// Root level check: not indented and contains colon
		if !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "\t") && strings.Contains(line, ":") {
			if strings.HasPrefix(line, "sops:") {
				inSops = true
				hasSops = true
				inAge = false
				continue
			}
			// Root key outside sops (could be before or after sops block)
			inSops = false
			inAge = false
			parts := strings.SplitN(line, ":", 2)
			keyName := strings.TrimSpace(parts[0])
			val := strings.TrimSpace(parts[1])
			isClear := !strings.HasPrefix(val, "ENC[")
			keys = append(keys, StoreFileKey{
				Name:  keyName,
				Value: val,
				Clear: isClear,
			})
			continue
		}

		if inSops {
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
	}

	return keys, recipients, hasSops, scanner.Err()
}

func unquoteYAML(val string) (string, error) {
	val = strings.TrimSpace(val)
	if len(val) >= 2 {
		if val[0] == '"' && val[len(val)-1] == '"' {
			inner := val[1 : len(val)-1]
			var b strings.Builder
			for i := 0; i < len(inner); i++ {
				if inner[i] != '\\' {
					b.WriteByte(inner[i])
					continue
				}
				if i+1 >= len(inner) {
					return "", fmt.Errorf("unterminated backslash escape")
				}
				i++
				switch inner[i] {
				case '0':
					b.WriteByte(0)
				case 'a':
					b.WriteByte('\a')
				case 'b':
					b.WriteByte('\b')
				case 't', '\t':
					b.WriteByte('\t')
				case 'n':
					b.WriteByte('\n')
				case 'v':
					b.WriteByte('\v')
				case 'f':
					b.WriteByte('\f')
				case 'r':
					b.WriteByte('\r')
				case 'e':
					b.WriteByte(0x1b)
				case ' ':
					b.WriteByte(' ')
				case '"':
					b.WriteByte('"')
				case '/':
					b.WriteByte('/')
				case '\\':
					b.WriteByte('\\')
				case 'N':
					b.WriteRune(0x85)
				case '_':
					b.WriteRune(0xa0)
				case 'L':
					b.WriteRune(0x2028)
				case 'P':
					b.WriteRune(0x2029)
				case 'x':
					if i+2 >= len(inner) {
						return "", fmt.Errorf("truncated \\x escape")
					}
					hexStr := inner[i+1 : i+3]
					i += 2
					hb, err := hex.DecodeString(hexStr)
					if err != nil || len(hb) != 1 {
						return "", fmt.Errorf("invalid \\x escape: \\x%s", hexStr)
					}
					b.WriteByte(hb[0])
				case 'u':
					if i+4 >= len(inner) {
						return "", fmt.Errorf("truncated \\u escape")
					}
					hexStr := inner[i+1 : i+5]
					i += 4
					hb, err := hex.DecodeString(hexStr)
					if err != nil || len(hb) != 2 {
						return "", fmt.Errorf("invalid \\u escape: \\u%s", hexStr)
					}
					rVal := binary.BigEndian.Uint16(hb)
					b.WriteRune(rune(rVal))
				case 'U':
					if i+8 >= len(inner) {
						return "", fmt.Errorf("truncated \\U escape")
					}
					hexStr := inner[i+1 : i+9]
					i += 8
					hb, err := hex.DecodeString(hexStr)
					if err != nil || len(hb) != 4 {
						return "", fmt.Errorf("invalid \\U escape: \\U%s", hexStr)
					}
					rVal := binary.BigEndian.Uint32(hb)
					if rVal > 0x10FFFF || (rVal >= 0xD800 && rVal <= 0xDFFF) {
						return "", fmt.Errorf("invalid Unicode scalar in \\U%s", hexStr)
					}
					b.WriteRune(rune(rVal))
				default:
					return "", fmt.Errorf("unrecognised escape sequence \\%c", inner[i])
				}
			}
			return b.String(), nil
		}
		if val[0] == '\'' && val[len(val)-1] == '\'' {
			inner := val[1 : len(val)-1]
			return strings.ReplaceAll(inner, "''", "'"), nil
		}
	}
	return val, nil
}

// ParseDecryptedSecrets parses the output of 'sops -d' into Secret values.
// Rejects multi-line values, NUL bytes, and illegal variable names.
func ParseDecryptedSecrets(data []byte) (map[string]Secret, []string, error) {
	if bytes.Contains(data, []byte{0}) {
		return nil, nil, fmt.Errorf("decrypted secret contains NUL byte")
	}

	scanner := bufio.NewScanner(bytes.NewReader(data))
	const maxLineLen = 1024 * 1024 // 1MB buffer
	scanner.Buffer(make([]byte, 64*1024), maxLineLen)

	secrets := make(map[string]Secret)
	var keys []string
	var badKeys []string
	var multilineKeys []string
	var decodeErr error

	var currentKey string
	var currentValue strings.Builder
	isMultiLine := false

	flushCurrent := func() {
		if currentKey != "" {
			if isMultiLine {
				multilineKeys = append(multilineKeys, currentKey)
			} else {
				val := currentValue.String()
				unquoted, err := unquoteYAML(val)
				if err != nil {
					if decodeErr == nil {
						decodeErr = fmt.Errorf("key=%s: %w", currentKey, err)
					}
				} else if strings.Contains(unquoted, "\x00") {
					if decodeErr == nil {
						decodeErr = fmt.Errorf("decrypted secret %s contains NUL byte", currentKey)
					}
				} else if strings.Contains(unquoted, "\n") || strings.Contains(unquoted, "\r") {
					multilineKeys = append(multilineKeys, currentKey)
				} else {
					secrets[currentKey] = NewSecret(unquoted)
					keys = append(keys, currentKey)
				}
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

	if err := scanner.Err(); err != nil {
		return nil, nil, fmt.Errorf("error reading decrypted secrets: %w", err)
	}

	if decodeErr != nil {
		return nil, nil, decodeErr
	}

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

func readGitVarint(data []byte, offset int) (int, int, error) {
	if offset >= len(data) {
		return 0, offset, fmt.Errorf("unexpected EOF reading varint")
	}
	v := data[offset]
	offset++
	stripLen := int(v & 0x7f)
	for v&0x80 != 0 {
		if offset >= len(data) {
			return 0, offset, fmt.Errorf("unexpected EOF reading varint")
		}
		stripLen++
		v = data[offset]
		offset++
		stripLen = (stripLen << 7) + int(v&0x7f)
	}
	return stripLen, offset, nil
}

// ReadGitIndex reads .git/index directly to find all tracked files and their blob SHAs.
// Supports index formats v2, v3, and v4 (prefix compression).
func ReadGitIndex(storeDir string) (*GitIndexData, error) {
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
	result := &GitIndexData{
		Entries: make(map[string]GitIndexEntry, entries),
	}

	if version == 2 || version == 3 {
		offset := 12
		var parsedCount uint32
		for i := uint32(0); i < entries && offset+62 <= len(data); i++ {
			entryStart := offset
			var blobSHA1 [20]byte
			copy(blobSHA1[:], data[entryStart+40:entryStart+60])

			flags := binary.BigEndian.Uint16(data[entryStart+60 : entryStart+62])
			headerLen := 62
			if version >= 3 && (flags&0x4000) != 0 {
				headerLen = 64
			}
			if entryStart+headerLen > len(data) {
				break
			}

			nameStart := entryStart + headerLen
			nameEnd := bytes.IndexByte(data[nameStart:], 0)
			if nameEnd == -1 {
				break
			}
			path := string(data[nameStart : nameStart+nameEnd])
			cleanPath := filepath.Clean(filepath.ToSlash(path))
			result.Entries[cleanPath] = GitIndexEntry{
				Path:     cleanPath,
				BlobSHA1: blobSHA1,
			}
			parsedCount++

			entryLen := ((headerLen + nameEnd + 8) / 8) * 8
			offset = entryStart + entryLen
		}
		if parsedCount != entries {
			return nil, fmt.Errorf("corrupt git index %s: expected %d entries, parsed %d", indexPath, entries, parsedCount)
		}
		return result, nil
	}

	if version == 4 {
		offset := 12
		prevPath := ""
		var parsedCount uint32
		for i := uint32(0); i < entries && offset+62 <= len(data); i++ {
			entryStart := offset
			var blobSHA1 [20]byte
			copy(blobSHA1[:], data[entryStart+40:entryStart+60])

			flags := binary.BigEndian.Uint16(data[entryStart+60 : entryStart+62])
			headerLen := 62
			if (flags & 0x4000) != 0 {
				headerLen = 64
			}
			if entryStart+headerLen > len(data) {
				break
			}

			offset += headerLen
			stripLen, nextOffset, err := readGitVarint(data, offset)
			if err != nil {
				return nil, fmt.Errorf("corrupt index v4 varint: %w", err)
			}
			offset = nextOffset

			nameEnd := bytes.IndexByte(data[offset:], 0)
			if nameEnd == -1 {
				return nil, fmt.Errorf("corrupt index v4 entry: unterminated path suffix")
			}
			suffix := string(data[offset : offset+nameEnd])
			offset += nameEnd + 1

			prefix := ""
			if stripLen <= len(prevPath) {
				prefix = prevPath[:len(prevPath)-stripLen]
			}
			path := prefix + suffix
			cleanPath := filepath.Clean(filepath.ToSlash(path))
			result.Entries[cleanPath] = GitIndexEntry{
				Path:     cleanPath,
				BlobSHA1: blobSHA1,
			}
			prevPath = path
			parsedCount++
		}
		if parsedCount != entries {
			return nil, fmt.Errorf("corrupt git index %s: expected %d entries, parsed %d", indexPath, entries, parsedCount)
		}
		return result, nil
	}

	return nil, fmt.Errorf("unsupported git index version %d", version)
}

// ReadGitIndexTrackedFiles reads .git/index directly to find all tracked files.
func ReadGitIndexTrackedFiles(storeDir string) (map[string]bool, error) {
	data, err := ReadGitIndex(storeDir)
	if err != nil {
		return nil, err
	}
	tracked := make(map[string]bool, len(data.Entries))
	for k := range data.Entries {
		tracked[k] = true
	}
	return tracked, nil
}

// GitBlobSHA1 computes the SHA1 hash of a git blob object for content.
func GitBlobSHA1(content []byte) [20]byte {
	h := sha1.New()
	fmt.Fprintf(h, "blob %d\x00", len(content))
	h.Write(content)
	var out [20]byte
	copy(out[:], h.Sum(nil))
	return out
}

// VerifyFileMatchesIndex checks that the file on disk matches its index blob SHA1.
func VerifyFileMatchesIndex(storeDir, filePath string, index *GitIndexData) error {
	rel, err := filepath.Rel(storeDir, filePath)
	if err != nil {
		rel = filePath
	}
	cleanRel := filepath.Clean(filepath.ToSlash(rel))
	entry, ok := index.Entries[cleanRel]
	if !ok {
		return fmt.Errorf("%s is untracked in git", rel)
	}
	data, err := os.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("unable to read %s: %w", rel, err)
	}
	actual := GitBlobSHA1(data)
	if actual != entry.BlobSHA1 {
		return fmt.Errorf("%s has uncommitted modifications (working copy blob differs from git index)", rel)
	}
	return nil
}

// VerifyFileMatchesHEADTree checks that filePath matches the blob committed in HEAD commit tree.
func VerifyFileMatchesHEADTree(storeDir, filePath string, headBlobs map[string]string) error {
	rel, err := filepath.Rel(storeDir, filePath)
	if err != nil {
		rel = filePath
	}
	cleanRel := filepath.Clean(filepath.ToSlash(rel))
	expectedSHA, ok := headBlobs[cleanRel]
	if !ok {
		return fmt.Errorf("%s is not committed in HEAD tree", rel)
	}
	data, err := os.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("unable to read %s: %w", rel, err)
	}
	actual := GitBlobSHA1(data)
	actualSHA := hex.EncodeToString(actual[:])
	if actualSHA != expectedSHA {
		return fmt.Errorf("%s has uncommitted modifications (working copy blob differs from HEAD tree)", rel)
	}
	return nil
}

// IsValidAsName checks if a seat name matches [A-Za-z0-9_-]+
func IsValidAsName(name string) bool {
	if len(name) == 0 {
		return false
	}
	for _, r := range name {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-') {
			return false
		}
	}
	return true
}
