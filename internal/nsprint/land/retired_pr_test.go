package land_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestNoRetiredPRRecordRead is the #3611 DONE-WHEN guard: no non-test Go file
// under cmd/nova-sprint or internal/nsprint/land may read the retired PR
// record s:<S>:pr:<repo>:<n> (the format literal s:%s:pr:). The unit records
// plus the typed lines and ci:<repo>:<head> are the contract; nothing writes
// that PR record.
func TestNoRetiredPRRecordRead(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller(0) failed")
	}
	root := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(thisFile))))
	checked := 0
	for _, dir := range []string{
		filepath.Join(root, "cmd", "nova-sprint"),
		filepath.Join(root, "internal", "nsprint", "land"),
	} {
		err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if info.IsDir() {
				return nil
			}
			if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			checked++
			b, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			if strings.Contains(string(b), "s:%s:pr:") {
				t.Errorf("%s reads the retired PR record (s:%%s:pr:)", path)
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", dir, err)
		}
	}
	if checked == 0 {
		t.Fatal("found no non-test Go files; the check would pass vacuously")
	}
}
