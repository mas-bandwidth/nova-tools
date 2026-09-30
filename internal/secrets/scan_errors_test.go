package secrets

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInvariantScansDoNotCallAMissingRootClean(t *testing.T) {
	t.Parallel()
	root := filepath.Join(t.TempDir(), "missing")
	for _, tc := range []struct {
		name string
		got  []CheckFailure
		kind string
	}{
		{"private key", CheckInvariant5(root, filepath.Join(t.TempDir(), "key")), "store-private-key"},
		{"untracked plaintext", CheckInvariant7(root, nil), "untracked-plaintext"},
	} {
		if len(tc.got) != 1 || tc.got[0].Kind != tc.kind ||
			tc.got[0].File != "." || !strings.Contains(tc.got[0].Reason, "cannot inspect store entry") {
			t.Errorf("%s scan of missing root = %+v; want one read failure", tc.name, tc.got)
		}
	}
}

func TestInvariantScansReportUnreadableEntriesAndKeepExclusions(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"broken-link", filepath.Join(".git", "ignored-link")} {
		if err := os.Symlink("missing-target", filepath.Join(root, path)); err != nil {
			t.Skipf("symlink fixture unavailable: %v", err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "seat.key"), []byte("AGE-SECRET-KEY-1fixture\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "clear.yaml"), []byte("TOKEN: plain\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	private := CheckInvariant5(root, filepath.Join(t.TempDir(), "outside.key"))
	plain := CheckInvariant7(root, nil)
	for _, tc := range []struct {
		name string
		got  []CheckFailure
		want map[string]bool
	}{
		{"private key", private, map[string]bool{"broken-link": true, "seat.key": true}},
		{"untracked plaintext", plain, map[string]bool{"broken-link": true, "clear.yaml": true}},
	} {
		if len(tc.got) != len(tc.want) {
			t.Errorf("%s findings = %+v; want files %v", tc.name, tc.got, tc.want)
			continue
		}
		for _, f := range tc.got {
			if !tc.want[f.File] || strings.Contains(f.File, ".git") {
				t.Errorf("%s unexpected finding: %+v", tc.name, f)
			}
		}
	}
	// A tracked file is not invariant 7's subject, even if it cannot be read.
	if got := CheckInvariant7(root, map[string]bool{"broken-link": true, "clear.yaml": true, "seat.key": true}); len(got) != 0 {
		t.Fatalf("tracked files or .git were scanned: %+v", got)
	}
}
