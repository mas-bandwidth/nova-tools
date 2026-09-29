//go:build functional

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestArchiveFunctionalWorkflow(t *testing.T) {
	t.Parallel()
	hermetic(t)
	checkout, _ := busDir(t)

	// Note 1: Older note (2026-09-05)
	p1 := "from-bo/2026-09-05T1000Z-old1-555555555555.md"
	writeFile(t, checkout, p1,
		"From: Bo\nTo: Ada\nDate: Sat Sep  5 10:00:00 UTC 2026\nId: bo-555555555555\nSubject: Old Note 1\n\nOld body 1.\n")
	appendFile(t, checkout, "from-bo/INDEX",
		"bo-555555555555\t"+p1+"\t2026-09-05T10:00:00Z\tAda\t-\n")

	// Note 2: Older note (2026-09-06)
	p2 := "from-ada/2026-09-06T1200Z-old2-222222222222.md"
	writeFile(t, checkout, p2,
		"From: Ada\nTo: Bo\nDate: Sun Sep  6 12:00:00 UTC 2026\nId: ada-222222222222\nSubject: Old Note 2\n\nOld body 2.\n")
	appendFile(t, checkout, "from-ada/INDEX",
		"ada-222222222222\t"+p2+"\t2026-09-06T12:00:00Z\tBo\t-\n")

	// Note 3: Newer note (2026-09-10)
	p3 := "from-bo/2026-09-10T1400Z-new-333333333333.md"
	writeFile(t, checkout, p3,
		"From: Bo\nTo: Ada\nDate: Thu Sep 10 14:00:00 UTC 2026\nId: bo-333333333333\nSubject: New Note 3\n\nNew body.\n")
	appendFile(t, checkout, "from-bo/INDEX",
		"bo-333333333333\t"+p3+"\t2026-09-10T14:00:00Z\tAda\t-\n")

	commitAs(t, checkout, "Bo", "three notes")
	gitIn(t, checkout, "push", "-q", "origin", "HEAD:refs/heads/main")

	// 1. Dry run
	rDry := invoke(t, "", "archive", "--bus", checkout,
		"--before", "2026-09-08T00:00:00Z", "--dry-run")
	rDry.mustCode(t, 0)
	rDry.mustContain(t, "stdout", "ARCHIVE OK archived=4 kept=1 target=archive (dry run)")

	// Verify nothing was written during dry run
	if _, err := os.Stat(filepath.Join(checkout, "archive")); !os.IsNotExist(err) {
		t.Fatalf("dry run created archive directory")
	}

	// 2. Real archive run with git push
	rReal := invoke(t, "", "archive", "--bus", checkout,
		"--before", "2026-09-08T00:00:00Z",
		"--remote", "origin", "--branch", "main", "--attempts", "3")
	rReal.mustCode(t, 0)
	rReal.mustContain(t, "stdout", "ARCHIVE OK archived=4 kept=1 target=archive commit=")

	// 3. Verify files on disk: old files moved, new file remains
	if _, err := os.Stat(filepath.Join(checkout, p1)); !os.IsNotExist(err) {
		t.Errorf("expected %s removed from active lane", p1)
	}
	if _, err := os.Stat(filepath.Join(checkout, p2)); !os.IsNotExist(err) {
		t.Errorf("expected %s removed from active lane", p2)
	}
	if _, err := os.Stat(filepath.Join(checkout, p3)); err != nil {
		t.Errorf("expected %s kept in active lane", p3)
	}

	// 4. Verify archived files in archive/
	if _, err := os.Stat(filepath.Join(checkout, "archive", p1)); err != nil {
		t.Errorf("expected %s in archive", p1)
	}
	if _, err := os.Stat(filepath.Join(checkout, "archive", p2)); err != nil {
		t.Errorf("expected %s in archive", p2)
	}

	// 5. Verify archive/INDEX
	rawArchIndex, err := os.ReadFile(filepath.Join(checkout, "archive", "INDEX"))
	if err != nil {
		t.Fatalf("reading archive/INDEX: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(rawArchIndex)), "\n")
	if len(lines) != 4 {
		t.Errorf("archive/INDEX has %d lines, want 4", len(lines))
	}

	// 6. Verify check --full passes cleanly
	rCheck := invoke(t, "", "check", "--bus", checkout, "--full")
	rCheck.mustCode(t, 0)
	rCheck.mustContain(t, "stdout", "BUS OK")
}

func TestArchiveFunctionalTarball(t *testing.T) {
	t.Parallel()
	hermetic(t)
	checkout, _ := busDir(t)

	p1 := "from-bo/2026-09-05T1000Z-old1-555555555555.md"
	writeFile(t, checkout, p1,
		"From: Bo\nTo: Ada\nDate: Sat Sep  5 10:00:00 UTC 2026\nId: bo-555555555555\nSubject: Old Note 1\n\nOld body 1.\n")
	appendFile(t, checkout, "from-bo/INDEX",
		"bo-555555555555\t"+p1+"\t2026-09-05T10:00:00Z\tAda\t-\n")

	p2 := "from-bo/2026-09-10T1400Z-new-222222222222.md"
	writeFile(t, checkout, p2,
		"From: Bo\nTo: Ada\nDate: Thu Sep 10 14:00:00 UTC 2026\nId: bo-222222222222\nSubject: New Note 2\n\nNew body.\n")
	appendFile(t, checkout, "from-bo/INDEX",
		"bo-222222222222\t"+p2+"\t2026-09-10T14:00:00Z\tAda\t-\n")

	commitAs(t, checkout, "Bo", "two notes")
	gitIn(t, checkout, "push", "-q", "origin", "HEAD:refs/heads/main")

	tarPath := filepath.Join(checkout, "archive-20260908.tar.gz")
	r := invoke(t, "", "archive", "--bus", checkout,
		"--before", "2026-09-08T00:00:00Z",
		"--out", tarPath,
		"--remote", "origin", "--branch", "main", "--attempts", "3")
	r.mustCode(t, 0)
	r.mustContain(t, "stdout", "ARCHIVE OK archived=3 kept=1 target=archive-20260908.tar.gz commit=")

	if _, err := os.Stat(filepath.Join(checkout, p1)); !os.IsNotExist(err) {
		t.Errorf("expected %s removed from active lane", p1)
	}
	if _, err := os.Stat(tarPath); err != nil {
		t.Errorf("expected tarball %s to exist", tarPath)
	}

	// Verify check passes
	rCheck := invoke(t, "", "check", "--bus", checkout, "--full")
	rCheck.mustCode(t, 0)
	rCheck.mustContain(t, "stdout", "BUS OK")
}
