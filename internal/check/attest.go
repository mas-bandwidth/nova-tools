// Package check implements the four record-layer checks behind nova-check.
// Each function returns (result, failures, error): failures mean the check
// ran and said NO (exit 1 at the CLI); a non-nil error means the check could
// not run at all (exit 2). See SPEC.md for what each check asserts and,
// as importantly, what it deliberately does not.
package check

import (
	"bufio"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Failure is one named NO from a check: the subject that failed and why.
type Failure struct {
	Subject string
	Reason  string
}

// Attestation is the successful result of Attest: what a full boot read.
type Attestation struct {
	Files  int
	Bytes  int64
	SHA256 string
}

// Attest verifies that every file listed in the manifest exists under home,
// is a regular file, and is non-empty, and computes the boot attestation:
// file count, total bytes, and a SHA-256 binding paths, order, and contents.
// Any failure leaves the attestation zero-valued; a partial self must not
// produce a pasteable line.
func Attest(home, manifest string) (Attestation, []Failure, error) {
	info, err := os.Stat(home)
	if err != nil {
		return Attestation{}, nil, fmt.Errorf("home: %w", err)
	}
	if !info.IsDir() {
		return Attestation{}, nil, fmt.Errorf("home %q is not a directory", home)
	}
	// Resolve home once so the per-entry containment check compares
	// symlink-free paths on both sides (macOS /var is itself a symlink).
	resolvedHome, err := filepath.EvalSymlinks(home)
	if err != nil {
		return Attestation{}, nil, fmt.Errorf("home: %w", err)
	}
	entries, err := parseManifest(manifest)
	if err != nil {
		return Attestation{}, nil, err
	}
	if len(entries) == 0 {
		return Attestation{}, []Failure{{
			Subject: manifest,
			Reason:  "manifest lists no files; attesting to nothing is not attestation",
		}}, nil
	}

	var (
		att      Attestation
		failures []Failure
		seen     = make(map[string]bool)
		h        = sha256.New()
		lenBuf   [binary.MaxVarintLen64]byte
	)
	fail := func(entry, reason string) {
		failures = append(failures, Failure{entry, reason})
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry, "/") || filepath.IsAbs(entry) {
			fail(entry, "absolute path; manifest entries must be relative to --home")
			continue
		}
		// Entries must already be canonical: near-duplicate spellings of
		// one path ("a.md" vs "./a.md") would dedupe apart and double-count.
		if entry != filepath.ToSlash(filepath.Clean(entry)) || strings.HasPrefix(entry, "./") {
			fail(entry, `not canonical; write the path clean (no "./", "//", ".." segments, or trailing "/")`)
			continue
		}
		if seen[entry] {
			fail(entry, "duplicate manifest entry")
			continue
		}
		seen[entry] = true
		clean := filepath.FromSlash(entry)
		if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
			fail(entry, "escapes --home")
			continue
		}
		full := filepath.Join(home, clean)
		fi, err := os.Lstat(full)
		if err != nil {
			fail(entry, "does not exist")
			continue
		}
		if !fi.Mode().IsRegular() {
			fail(entry, fmt.Sprintf("not a regular file (%s)", fi.Mode().Type()))
			continue
		}
		// The escape check above is lexical only; a symlinked directory
		// inside home could still point anywhere. Resolve and require the
		// real path to sit under the real home, and to contain no symlinked
		// components at all — symlinks are never followed, even one that
		// resolves inside --home.
		resolvedFull, err := filepath.EvalSymlinks(full)
		if err != nil {
			fail(entry, fmt.Sprintf("cannot resolve: %v", err))
			continue
		}
		if resolvedFull != resolvedHome &&
			!strings.HasPrefix(resolvedFull, resolvedHome+string(filepath.Separator)) {
			fail(entry, "escapes --home via a symlinked path component; symlinks are never followed")
			continue
		}
		if resolvedFull != filepath.Join(resolvedHome, clean) {
			fail(entry, "symlinked path component; symlinks are never followed, even one that resolves inside --home")
			continue
		}
		size := fi.Size()
		if size == 0 {
			fail(entry, "empty (0 bytes); a truncated self must not attest")
			continue
		}
		f, err := os.Open(full)
		if err != nil {
			fail(entry, fmt.Sprintf("unreadable: %v", err))
			continue
		}
		// The hash binds path, order, and contents with length-prefixed
		// framing (injective even when contents hold NUL): see SPEC.md.
		n := binary.PutUvarint(lenBuf[:], uint64(len(entry)))
		h.Write(lenBuf[:n])
		h.Write([]byte(entry))
		n = binary.PutUvarint(lenBuf[:], uint64(size))
		h.Write(lenBuf[:n])
		copied, err := io.Copy(h, f)
		f.Close()
		if err != nil {
			fail(entry, fmt.Sprintf("unreadable: %v", err))
			continue
		}
		if copied != size {
			fail(entry, fmt.Sprintf("size changed while reading: expected %d bytes, read %d", size, copied))
			continue
		}
		att.Files++
		att.Bytes += copied
	}
	if len(failures) > 0 {
		return Attestation{}, failures, nil
	}
	att.SHA256 = hex.EncodeToString(h.Sum(nil))
	return att, nil, nil
}

// parseManifest reads one relative path per line; blank lines and lines
// starting with # are ignored. Order is preserved: it is the boot order.
func parseManifest(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("manifest: %w", err)
	}
	defer f.Close()

	var entries []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		entries = append(entries, line)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("manifest: %w", err)
	}
	return entries, nil
}
