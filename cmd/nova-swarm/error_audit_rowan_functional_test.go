//go:build functional

package main

// Rowan's failure-guidance audit (2026-09-26): slots list and slots release
// against a store directory that does not exist answer as if it were an
// empty store -- list prints nothing and exits 0, release prints SLOTS
// RELEASED released=0 and exits 0 -- so a mistyped --store reads as "nothing
// held" and "freed". Both should refuse, name the path, and point at
// `nova-swarm slots init --store <dir>`.

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRowanAuditSlotsMissingStoreIsNotEmpty(t *testing.T) {
	t.Parallel()
	missing := filepath.Join(t.TempDir(), "no-such-store")
	for _, argv := range [][]string{
		{"slots", "list", "--store", missing},
		{"slots", "release", "--store", missing, "--owner", "o", "--all"},
	} {
		var out, errOut bytes.Buffer
		code := run(argv, strings.NewReader(""), &out, &errOut, time.Now())
		if code == 0 || !strings.Contains(errOut.String()+out.String(), missing) {
			t.Errorf("%s on a store that does not exist: exit=%d stdout=%q stderr=%q; want a refusal naming %s and slots init",
				strings.Join(argv[:2], " "), code, out.String(), errOut.String(), missing)
		}
	}
}
