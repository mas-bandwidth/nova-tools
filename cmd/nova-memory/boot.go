// boot.go holds the boot verb: its flags, its run and the helpers only it uses.

package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

func cmdBoot(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("boot", flag.ContinueOnError)
	root := fs.String("root", "", "memory root directory (required)")
	pin := fs.String("pin", "", "pin file naming the memories to load (required)")
	asJSON := fs.Bool("json", false, "print the result as one JSON object instead of lines")
	given, pos, ok := parse(fs, args, stderr, "root", "pin")
	if given == nil {
		return 2
	}
	bad := !ok
	if len(pos) > 0 {
		refuse(stderr, " boot", fmt.Sprintf("unexpected argument %q", pos[0]))
		bad = true
	}
	if given["pin"] && strings.TrimSpace(*pin) == "" {
		refuse(stderr, " boot", "--pin names the file listing the memories to load; name it")
		bad = true
	}
	if bad {
		return 2
	}
	n, bytes, ok := loadPin(*root, *pin, stderr)
	if !ok {
		return 2
	}
	if *asJSON {
		return result("boot").Fact("files", n).Fact("bytes", bytes).Render(stdout, true)
	}
	fmt.Fprintf(stdout, "BOOT OK files=%d bytes=%d\n", n, bytes)
	return 0
}

// loadPin reads the pin file and loads exactly the files it names, relative to
// root and never by walking the directory. It returns the count and the byte
// total of the loaded memories, and the total is what the reads returned, not
// what the directory entries promised (docs/SPEC.md, boot: "Boot reads exactly
// those files ... The load is the named files' byte total"; the defect it
// closes is docs/ratings/snapshots/0c5803c2de40/memory-read.md finding 4,
// where an os.Lstat sum counted bytes nothing had read). Every misshapen
// entry is a refusal, because a boot that silently skipped a named memory is
// a self that loaded less than it thinks it did.
func loadPin(root, pin string, stderr io.Writer) (int, int64, bool) {
	return loadPinOpen(root, pin, stderr, func(name string) (io.ReadCloser, error) { return os.Open(name) })
}

// loadPinOpen is loadPin with each pinned file's opener handed in per call:
// production opens with os.Open, and a test that must inject a read failure
// after metadata validation injects its own. The seam is a parameter and
// nothing else — no global to flip, so a run's behavior is fixed by its
// arguments. os.Lstat below is kept, not swapped for os.Stat, so the
// final-component symlink rejection stays exactly what validation has always
// been: entry checking, not a path-security contract.
func loadPinOpen(root, pin string, stderr io.Writer, open func(string) (io.ReadCloser, error)) (int, int64, bool) {
	entries, err := readPin(pin)
	if err != nil {
		refuse(stderr, " boot", oneline.Err(err))
		return 0, 0, false
	}
	if len(entries) == 0 {
		refuse(stderr, " boot", fmt.Sprintf("--pin %s names no memories; a boot of nothing is not a boot", oneline.Escape(pin)))
		return 0, 0, false
	}
	var total int64
	seen := make(map[string]bool, len(entries))
	for _, e := range entries {
		if strings.HasPrefix(e, "/") || filepath.IsAbs(e) {
			refuse(stderr, " boot", fmt.Sprintf("pin entry %q is absolute; every entry is relative to --root", oneline.Escape(e)))
			return 0, 0, false
		}
		if e != path.Clean(e) {
			refuse(stderr, " boot", fmt.Sprintf("pin entry %q is not canonical (no \"./\", \"//\", \"..\" or trailing \"/\")", oneline.Escape(e)))
			return 0, 0, false
		}
		if e == ".." || strings.HasPrefix(e, "../") {
			refuse(stderr, " boot", fmt.Sprintf("pin entry %q escapes --root", oneline.Escape(e)))
			return 0, 0, false
		}
		if seen[e] {
			refuse(stderr, " boot", fmt.Sprintf("pin entry %q appears twice; double-counted bytes are a lie", oneline.Escape(e)))
			return 0, 0, false
		}
		seen[e] = true
		full := filepath.Join(root, filepath.FromSlash(e))
		fi, err := os.Lstat(full)
		if err != nil {
			refuse(stderr, " boot", fmt.Sprintf("pin entry %q does not exist under --root", oneline.Escape(e)))
			return 0, 0, false
		}
		if !fi.Mode().IsRegular() {
			refuse(stderr, " boot", fmt.Sprintf("pin entry %q is not a regular file", oneline.Escape(e)))
			return 0, 0, false
		}
		if fi.Size() == 0 {
			refuse(stderr, " boot", fmt.Sprintf("pin entry %q is empty; a memory of zero bytes cannot be loaded", oneline.Escape(e)))
			return 0, 0, false
		}
		// Validation is the entry's claim; the READ is the boot's. Open the
		// file, stream every byte to io.Discard through a constant buffer so
		// the pinned corpus never sits in memory however large it grows, and
		// count what actually came back. A failed read or close is a refusal
		// naming the offending entry, and each file is closed before the next
		// one is opened.
		f, err := open(full)
		if err != nil {
			refuse(stderr, " boot", fmt.Sprintf("pin entry %q could not be opened for reading: %s; check the file is readable and re-run, or correct the pin", oneline.Escape(e), oneline.Err(err)))
			return 0, 0, false
		}
		n, readErr := io.Copy(io.Discard, f)
		closeErr := f.Close()
		if readErr != nil {
			refuse(stderr, " boot", fmt.Sprintf("pin entry %q read %d of %d bytes before failing: %s; check the file is readable and re-run, or correct the pin", oneline.Escape(e), n, fi.Size(), oneline.Err(readErr)))
			return 0, 0, false
		}
		if closeErr != nil {
			refuse(stderr, " boot", fmt.Sprintf("pin entry %q read %d bytes but could not be closed: %s; check the file's storage and re-run, or correct the pin", oneline.Escape(e), n, oneline.Err(closeErr)))
			return 0, 0, false
		}
		total += n
	}
	return len(entries), total, true
}

// readPin reads one memory path per line; blank lines and lines starting with
// # are ignored. Order is preserved — it is the boot order.
func readPin(name string) ([]string, error) {
	f, err := os.Open(name)
	if err != nil {
		return nil, err
	}
	// ignored: a file opened only for reading
	defer func() { _ = f.Close() }()
	var out []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		out = append(out, line)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return out, nil
}
