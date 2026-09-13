package secrets

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// RunNames reads <store>/<as>.yaml without decrypting and reports top-level key names.
func RunNames(storeDir, asName string, maxShown int) (okLine string, nameLines []string, moreLine string, err error) {
	if maxShown < 0 {
		return "", nil, "", fmt.Errorf("--max %d is negative; expected non-negative integer", maxShown)
	}
	if storeDir == "" {
		return "", nil, "", fmt.Errorf("missing --store <dir>")
	}
	if asName == "" {
		return "", nil, "", fmt.Errorf("missing --as <name>")
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
		return "", nil, "", fmt.Errorf("seat file %s.yaml is absent in store; available names: %s", asName, strings.Join(names, ", "))
	}

	keys, _, _, err := ParseStoreFileWithoutDecrypting(targetFile)
	if err != nil {
		return "", nil, "", fmt.Errorf("unable to read %s: %w", targetFile, err)
	}

	// Sort keys alphabetically by name
	sort.Slice(keys, func(i, j int) bool {
		return keys[i].Name < keys[j].Name
	})

	totalKeys := len(keys)
	sealedCount := 0
	clearCount := 0
	for _, k := range keys {
		if k.Clear {
			clearCount++
		} else {
			sealedCount++
		}
	}

	shown := totalKeys
	if maxShown > 0 && totalKeys > maxShown {
		shown = maxShown
	}

	for i := 0; i < shown; i++ {
		nameLines = append(nameLines, fmt.Sprintf("SECRETS NAME key=%s clear=%t", keys[i].Name, keys[i].Clear))
	}

	if maxShown > 0 && totalKeys > maxShown {
		moreLine = fmt.Sprintf("SECRETS NAMES MORE kind=key shown=%d total=%d run: nova-secrets names --store %s --as %s --max 0",
			shown, totalKeys, storeDir, asName)
	}

	okLine = fmt.Sprintf("SECRETS NAMES OK as=%s keys=%d shown=%d sealed=%d clear=%d",
		asName, totalKeys, shown, sealedCount, clearCount)

	return okLine, nameLines, moreLine, nil
}
