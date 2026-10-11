package secrets

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/mas-bandwidth/nova-tools/pkg/oneline"
)

// NameRow is one key name in a seat's file and whether the store keeps it in the clear.
// It holds no value, sealed or clear.
type NameRow struct {
	Name  string
	Clear bool
}

// NamesReport is what names found: the seat, the rows shown (at most --max), and the
// counts over the whole file. Lines renders it as typed lines; the caller renders the
// same value as JSON, so the two cannot drift.
type NamesReport struct {
	As, StoreDir         string
	Rows                 []NameRow
	Total, Sealed, Clear int
}

// Lines is the report as the typed lines names prints: one NAME line per row shown, a
// MORE line when --max cut the list, and the OK line.
func (r NamesReport) Lines() (okLine string, nameLines []string, moreLine string) {
	for _, n := range r.Rows {
		nameLines = append(nameLines, fmt.Sprintf("SECRETS NAME key=%s clear=%t", oneline.Field(n.Name), n.Clear))
	}
	if len(r.Rows) < r.Total {
		moreLine = fmt.Sprintf("SECRETS NAMES MORE kind=key shown=%d total=%d run: nova-secrets names --store %s --as %s --max 0",
			len(r.Rows), r.Total, oneline.Field(r.StoreDir), oneline.Field(r.As))
	}
	okLine = fmt.Sprintf("SECRETS NAMES OK as=%s keys=%d shown=%d sealed=%d clear=%d",
		oneline.Field(r.As), r.Total, len(r.Rows), r.Sealed, r.Clear)
	return okLine, nameLines, moreLine
}

// RunNames reads <store>/<as>.yaml without decrypting and reports top-level key names.
func RunNames(storeDir, asName string, maxShown int) (NamesReport, error) {
	if maxShown < 0 {
		return NamesReport{}, fmt.Errorf("--max %d is negative; expected non-negative integer", maxShown)
	}
	if err := preflight(storeDir, need{storeDir, "--store <dir>", false}, need{asName, "--as <name>", true}); err != nil {
		return NamesReport{}, err
	}

	targetFile := filepath.Join(storeDir, asName+".yaml")
	if _, err := os.Stat(targetFile); err != nil {
		return NamesReport{}, seatAbsent(storeDir, asName)
	}

	keys, _, _, err := ParseStoreFileWithoutDecrypting(targetFile)
	if err != nil {
		return NamesReport{}, fmt.Errorf("unable to read %s: %w", targetFile, err)
	}

	// Sort keys alphabetically by name
	sort.Slice(keys, func(i, j int) bool {
		return keys[i].Name < keys[j].Name
	})

	r := NamesReport{As: asName, StoreDir: storeDir, Total: len(keys)}
	for i, k := range keys {
		if k.Clear {
			r.Clear++
		} else {
			r.Sealed++
		}
		if maxShown == 0 || i < maxShown {
			r.Rows = append(r.Rows, NameRow{Name: k.Name, Clear: k.Clear})
		}
	}
	return r, nil
}
