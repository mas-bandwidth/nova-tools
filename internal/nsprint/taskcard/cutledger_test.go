package taskcard_test

import (
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/taskcard"
)

// TestCutLedgerKeyAndParse: the key is the file's sha256; the hash reads
// back as repo and row -> issue; a field that is neither is named.
func TestCutLedgerKeyAndParse(t *testing.T) {
	t.Parallel()
	// sha256("") is a fixed vector.
	if k := taskcard.CutLedgerKey(nil); k != "cut:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855" {
		t.Fatalf("key %q", k)
	}
	if taskcard.CutLedgerKey([]byte("a")) == taskcard.CutLedgerKey([]byte("b")) {
		t.Fatal("two files share a ledger")
	}
	l, err := taskcard.ParseCutLedger("cut:x", map[string]string{"repo": "o/r", "1": "5000", "3": "5001"})
	if err != nil || l.Repo != "o/r" || len(l.Issues) != 2 || l.Issues[1] != 5000 || l.Issues[3] != 5001 {
		t.Fatalf("ledger %+v err %v", l, err)
	}
	if l, err := taskcard.ParseCutLedger("cut:x", map[string]string{}); err != nil || len(l.Issues) != 0 {
		t.Fatalf("empty ledger %+v err %v", l, err)
	}
	if _, err := taskcard.ParseCutLedger("cut:x", map[string]string{"repo": "o/r", "row:2": "7"}); err == nil || !strings.Contains(err.Error(), `field "row:2"`) {
		t.Fatalf("bad field: %v", err)
	}
	if _, err := taskcard.ParseCutLedger("cut:x", map[string]string{"2": "7"}); err == nil || !strings.Contains(err.Error(), "no repo") {
		t.Fatalf("no repo: %v", err)
	}
}
