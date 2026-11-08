//go:build functional

package boundary

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestNoCompiledBinaries fails on any tracked file that is a compiled binary
// (ELF, Mach-O or PE magic) or larger than 1 MiB outside docs/assets: build
// outputs belong outside the checkout (docs/TESTING.md).
func TestNoCompiledBinaries(t *testing.T) {
	base := root(t)
	out, err := exec.Command("git", "-C", base, "ls-files", "-z").Output()
	if err != nil {
		t.Skipf("git ls-files: %v", err)
	}
	magics := [][]byte{
		[]byte("\x7fELF"), {0xfe, 0xed, 0xfa, 0xce}, {0xfe, 0xed, 0xfa, 0xcf}, {0xce, 0xfa, 0xed, 0xfe},
		{0xcf, 0xfa, 0xed, 0xfe}, {0xca, 0xfe, 0xba, 0xbe}, []byte("MZ"),
	}
	for _, name := range strings.Split(strings.TrimSuffix(string(out), "\x00"), "\x00") {
		p := filepath.Join(base, filepath.FromSlash(name))
		fi, err := os.Lstat(p)
		if err != nil || !fi.Mode().IsRegular() {
			continue // deleted in the working tree, or a symlink (TestNoSymlinks)
		}
		if fi.Size() > 1<<20 && !strings.HasPrefix(name, "docs/assets/") {
			t.Errorf("%s is %d bytes: a tracked file over 1 MiB belongs in docs/assets or outside the repository", name, fi.Size())
		}
		f, err := os.Open(p)
		if err != nil {
			t.Error(err)
			continue
		}
		head := make([]byte, 4)
		n, _ := io.ReadFull(f, head)
		f.Close()
		for _, m := range magics {
			if n >= len(m) && bytes.Equal(head[:len(m)], m) && (len(m) > 2 || strings.HasSuffix(name, ".exe") || strings.HasSuffix(name, ".dll")) {
				t.Errorf("%s is a compiled binary (magic %x): remove it from the repository and build outside the checkout", name, m)
				break
			}
		}
	}
}
