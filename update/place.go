package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
)

// item is one file to install: its name in the bin directory, the sha256 the release
// gives it, and where its bytes come from.
type item struct {
	name, sum string
	open      func() (io.ReadCloser, error)
}

// place installs items into bin. Every new file is staged and its bytes checked first, so
// nothing is replaced unless the whole set verified; a file already holding the bytes is
// skipped. Each target is then moved aside and the staged file renamed onto it (a running
// binary can be replaced this way on every platform); a failed rename puts back every
// target already replaced.
func place(bin string, items []item, progress io.Writer) (installed, skipped int, err error) {
	if err := os.MkdirAll(bin, 0o755); err != nil {
		return 0, 0, err
	}
	type staged struct{ target, tmp string }
	var todo []staged
	defer func() {
		for _, s := range todo {
			if s.tmp != "" {
				_ = os.Remove(s.tmp) // ignored: cleanup of a temp that was not placed
			}
		}
	}()
	for _, it := range items {
		target := filepath.Join(bin, it.name)
		if sum, err := fileSum(target); err == nil && sum == it.sum {
			skipped++
			continue
		}
		tmp, err := stage(bin, it)
		if err != nil {
			return 0, 0, fmt.Errorf("%s: %w", it.name, err)
		}
		todo = append(todo, staged{target: target, tmp: tmp})
	}
	type moved struct{ target, aside string }
	var done []moved
	undo := func() {
		for i := len(done) - 1; i >= 0; i-- {
			d := done[i]
			if d.aside != "" {
				_ = os.Rename(d.aside, d.target) // ignored: best effort; the error that stopped the install is reported
			} else {
				_ = os.Remove(d.target) // ignored: the target did not exist before
			}
		}
	}
	for i, s := range todo {
		aside := ""
		if _, err := os.Lstat(s.target); err == nil {
			aside = s.tmp + ".old"
			if err := os.Rename(s.target, aside); err != nil {
				undo()
				return 0, 0, fmt.Errorf("cannot move %s aside: %w", s.target, err)
			}
		}
		fmt.Fprintf(progress, "nova-update: installing %s\n", filepath.Base(s.target))
		if err := os.Rename(s.tmp, s.target); err != nil {
			if aside != "" {
				_ = os.Rename(aside, s.target) // ignored: best effort; the rename error is reported
			}
			undo()
			return 0, 0, fmt.Errorf("cannot install %s: %w", s.target, err)
		}
		todo[i].tmp = ""
		done = append(done, moved{target: s.target, aside: aside})
	}
	for _, d := range done {
		if d.aside != "" {
			_ = os.Remove(d.aside) // ignored: on windows a running binary's old file stays until it exits; it is hidden and inert
		}
	}
	return len(done), skipped, nil
}

// stage copies an item into a fresh temp in bin, hashing as it goes, and refuses bytes
// that are not the release's.
func stage(bin string, it item) (string, error) {
	src, err := it.open()
	if err != nil {
		return "", err
	}
	defer func() { _ = src.Close() }() // ignored: read side
	f, err := os.CreateTemp(bin, "."+it.name+".*")
	if err != nil {
		return "", err
	}
	h := sha256.New()
	_, err = io.Copy(io.MultiWriter(f, h), src)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Chmod(f.Name(), 0o755)
	}
	if err == nil && hex.EncodeToString(h.Sum(nil)) != it.sum {
		err = fmt.Errorf("its bytes are not the release's (sha256 %s, %s says %s)", hex.EncodeToString(h.Sum(nil)), sumsFile, it.sum)
	}
	if err != nil {
		_ = os.Remove(f.Name()) // ignored: the staged copy is discarded
		return "", err
	}
	return f.Name(), nil
}

var versionDir = regexp.MustCompile(`^v(\d+)\.(\d+)\.(\d+)$`)

// keepNewest is how many release directories besides the one installed prune keeps, so an
// earlier build can be put back.
const keepNewest = 4

// prune removes old release directories under root: only names shaped v<maj>.<min>.<patch>,
// never the one just installed nor the keepNewest newest. It never fails the install.
func prune(root, current string) (removed, failed int) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return 0, 1
	}
	type ver struct {
		name string
		n    [3]int
	}
	var vs []ver
	for _, e := range entries {
		m := versionDir.FindStringSubmatch(e.Name())
		if !e.IsDir() || m == nil || e.Name() == current {
			continue
		}
		var v ver
		v.name = e.Name()
		for i := range 3 {
			v.n[i], _ = strconv.Atoi(m[i+1]) // ignored: the pattern holds digits only
		}
		vs = append(vs, v)
	}
	sort.Slice(vs, func(i, j int) bool {
		a, b := vs[i].n, vs[j].n
		for k := range 3 {
			if a[k] != b[k] {
				return a[k] > b[k]
			}
		}
		return false
	})
	for i, v := range vs {
		if i < keepNewest {
			continue
		}
		if err := os.RemoveAll(filepath.Join(root, v.name)); err != nil {
			failed++
		} else {
			removed++
		}
	}
	return removed, failed
}
