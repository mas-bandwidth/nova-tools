package cairn

import (
	"os"
	"path/filepath"
	"strings"
)

// Coverage derives the ledger from the store: session records and stored
// entries counted, never remembered, so the number cannot drift from the
// tree it reports on. Only the tests ask for it; no verb does.
func Coverage(store string) Ledger {
	var led Ledger
	names := map[string]bool{}
	for _, dir := range []string{filepath.Join(store, "sessions"), store} {
		if files, err := os.ReadDir(dir); err == nil {
			for _, f := range files {
				if !f.IsDir() && strings.HasSuffix(f.Name(), ".md") {
					id := strings.TrimSuffix(f.Name(), ".md")
					if ValidID(id) {
						names[id] = true
					}
				}
			}
		}
	}
	led.Sessions = len(names)
	if _, _, total, err := Index(store, ""); err == nil {
		led.Entries = total
	}
	return led
}
