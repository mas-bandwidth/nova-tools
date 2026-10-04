package main

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func init() {
	register(verb{
		name:    "sums",
		summary: "check a directory is exactly the shipped set, then write and verify SHA256SUMS over it",
		help: `usage: go run ./tools/ghrelease sums <stamp> <dist>

Checks that <dist> holds exactly the shipped set, then writes and verifies
SHA256SUMS over the whole of it, on this one machine. Exit 0 written and
verified; 1 refused or a checksum does not verify; 2 usage.

The binaries are built one platform per runner ("ghrelease build") and arrive
here as artifacts. This is the single place that sees every one of them, so it
is where the set is checked and summed: SHA256SUMS lists every artifact of the
release and is computed over the bytes on this disk, never assembled from
per-platform pieces. release.yml runs it in the step that attaches the set to
the release; certification.yml's release-dry-run runs it over the dry-run set.

Exactly the shipped set: every cmd/*/ tool for every platform in
release-targets, and nothing else. A leg that uploaded nothing, a tool one
platform lost, or a stray file from a runner is refused by name here, before a
checksum file can agree with it. SHA256SUMS is computed before it is written,
so it cannot list itself, and it is verified at once: a checksum file nobody has
checked is a file whose first reader is the person it was supposed to reassure.
`,
		do: doSums,
	})
}

func doSums(e env, args []string) int {
	if len(args) != 2 {
		fmt.Fprintf(e.stderr, "usage: %s sums <stamp> <dist>\n", tool)
		return 2
	}
	stamp, dist := args[0], args[1]
	distDir := dist
	if !filepath.IsAbs(distDir) {
		distDir = filepath.Join(e.root(), distDir)
	}

	if fi, err := os.Stat(distDir); err != nil || !fi.IsDir() {
		fmt.Fprintf(e.stderr, "refusing: %s is not a directory\n", dist)
		return 1
	}
	sumsPath := filepath.Join(distDir, "SHA256SUMS")
	if _, err := os.Lstat(sumsPath); err == nil {
		fmt.Fprintf(e.stderr, "refusing: %s already holds a SHA256SUMS\n", dist)
		return 1
	}

	targets, err := e.shipped()
	if err != nil {
		fmt.Fprintf(e.stderr, "%s sums: %v\n", tool, err)
		return 2
	}
	tools, _ := toolNames(e.root(), "cmd")
	var want []string
	for _, t := range targets {
		for _, n := range tools {
			want = append(want, artifactName(n, stamp, t))
		}
	}
	sort.Strings(want)
	if len(want) == 0 {
		fmt.Fprintln(e.stderr, "refusing: the shipped set is empty (cmd/*/ x release-targets)")
		return 1
	}

	entries, err := os.ReadDir(distDir)
	if err != nil {
		fmt.Fprintf(e.stderr, "refusing: %s cannot be read: %v\n", dist, err)
		return 1
	}
	have := make([]string, 0, len(entries))
	for _, ent := range entries {
		have = append(have, ent.Name())
	}
	sort.Strings(have)
	if missing, extra := diffSorted(want, have); len(missing)+len(extra) > 0 {
		fmt.Fprintf(e.stdout, "--- shipped set\n+++ %s\n", dist)
		for _, n := range missing {
			fmt.Fprintf(e.stdout, "-%s\n", n)
		}
		for _, n := range extra {
			fmt.Fprintf(e.stdout, "+%s\n", n)
		}
		fmt.Fprintf(e.stderr, "refusing: %s is not the shipped set (- missing, + not shipped)\n", dist)
		return 1
	}

	var sums bytes.Buffer
	for _, n := range want {
		sum, err := fileSum(filepath.Join(distDir, n))
		if err != nil {
			fmt.Fprintf(e.stderr, "sha256sum: %s: %v\n", n, err)
			return 1
		}
		fmt.Fprintf(&sums, "%s  %s\n", sum, n)
	}
	if err := os.WriteFile(sumsPath, sums.Bytes(), 0o644); err != nil {
		fmt.Fprintf(e.stderr, "%s sums: %v\n", tool, err)
		return 1
	}
	if err := os.Chmod(sumsPath, 0o644); err != nil {
		fmt.Fprintf(e.stderr, "%s sums: %v\n", tool, err)
		return 1
	}
	if !verifySums(e.stdout, e.stderr, distDir, sumsPath) {
		return 1
	}
	fmt.Fprintf(e.stdout, "SHA256SUMS over %d artifacts:\n", len(want))
	fmt.Fprint(e.stdout, sums.String())
	return 0
}

// diffSorted returns the names in want and not in have (missing) and the names
// in have and not in want (extra); both inputs are sorted.
func diffSorted(want, have []string) (missing, extra []string) {
	i, j := 0, 0
	for i < len(want) || j < len(have) {
		switch {
		case j >= len(have) || (i < len(want) && want[i] < have[j]):
			missing = append(missing, want[i])
			i++
		case i >= len(want) || have[j] < want[i]:
			extra = append(extra, have[j])
			j++
		default:
			i++
			j++
		}
	}
	return missing, extra
}

func fileSum(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// verifySums reads the checksum file back from disk and checks every line
// against the file it names, printing "<name>: OK" or "<name>: FAILED" for
// each, as sha256sum -c does.
func verifySums(stdout, stderr io.Writer, dir, sumsPath string) bool {
	f, err := os.Open(sumsPath)
	if err != nil {
		fmt.Fprintf(stderr, "sha256sum: %v\n", err)
		return false
	}
	defer f.Close()
	ok := true
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		sum, name, found := strings.Cut(sc.Text(), "  ")
		if !found {
			fmt.Fprintf(stderr, "sha256sum: SHA256SUMS: improperly formatted line\n")
			return false
		}
		got, err := fileSum(filepath.Join(dir, name))
		if err != nil || got != sum {
			fmt.Fprintf(stdout, "%s: FAILED\n", name)
			ok = false
			continue
		}
		fmt.Fprintf(stdout, "%s: OK\n", name)
	}
	if !ok {
		fmt.Fprintln(stderr, "sha256sum: WARNING: computed checksums did NOT match")
	}
	return ok
}
