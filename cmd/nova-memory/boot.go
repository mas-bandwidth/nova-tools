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

	"github.com/mas-bandwidth/nova-tools/pkg/oneline"
)

func cmdBoot(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("boot", flag.ContinueOnError)
	root := fs.String("root", "", "memory root directory (required)")
	pin := fs.String("pin", "", "pin file naming the memories to check (required)")
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
		refuse(stderr, " boot", "--pin names the file listing the memories to check; name it")
		bad = true
	}
	if given["root"] && strings.TrimSpace(*root) == "" {
		refuse(stderr, " boot", "--root names the memory directory and is never guessed from the working directory")
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

// loadPin reads the pin file and checks the pin: every file present and
// readable, and their size, relative to root and never by walking the
// directory. It returns the count and the byte
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
// arguments. Metadata is os.Root.Lstat on the slash path, not os.Stat and not
// os.Lstat of a joined path: the final component is not followed, and an
// intermediate directory symlink that leaves --root is a refusal
// (docs/SPEC.md rule 245; security#76 finding 5, re-file of security#58
// finding 3). os.Lstat on the joined path only checks the final component.
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
	// Open once. Root.Lstat refuses a path that escapes through a symlink;
	// close on every return, including a later refusal.
	rf, err := os.OpenRoot(root)
	if err != nil {
		refuse(stderr, " boot", fmt.Sprintf("--root %s: %s", oneline.Escape(root), oneline.Err(err)))
		return 0, 0, false
	}
	defer rf.Close() // ignored: a read-only root holds nothing to flush
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
		fi, err := rf.Lstat(e)
		if err != nil {
			// errPathEscapes is unexported; its text is the signal that an
			// intermediate symlink left the root. Any other error keeps the
			// missing-entry refusal. Both reasons are format literals so the
			// print audit sees no raw argument.
			if strings.Contains(err.Error(), "path escapes from parent") {
				refuse(stderr, " boot", fmt.Sprintf("pin entry %q escapes --root through a symlink", oneline.Escape(e)))
			} else {
				refuse(stderr, " boot", fmt.Sprintf("pin entry %q does not exist under --root", oneline.Escape(e)))
			}
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
		// one is opened. The opener still receives the joined path: the seam
		// tests name that path, and the Lstat above has already refused an
		// escape.
		full := filepath.Join(root, filepath.FromSlash(e))
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
	defer f.Close()
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
