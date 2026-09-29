package main

import (
	"strings"
	"testing"
)

func TestArchiveHelp(t *testing.T) {
	t.Parallel()
	r := invoke(t, "", "archive", "--help")
	r.mustCode(t, 0)
	if !strings.Contains(r.stdout, "nova-bus archive") {
		t.Fatalf("help stdout does not contain 'nova-bus archive':\n%s", r.stdout)
	}
	if r.stderr != "" {
		t.Fatalf("help stderr is not empty:\n%s", r.stderr)
	}
}

func TestArchiveRequiresBusAndBefore(t *testing.T) {
	t.Parallel()
	r1 := invoke(t, "", "archive")
	r1.mustCode(t, 2)
	r1.mustContain(t, "stderr", "nova-bus archive: --bus is required; refusing to guess; run: nova-bus help")

	r2 := invoke(t, "", "archive", "--bus", "/nonexistent")
	r2.mustCode(t, 2)
	r2.mustContain(t, "stderr", "nova-bus archive: --before is required; refusing to guess; run: nova-bus help")
}

func TestArchiveRefusesMalformedBefore(t *testing.T) {
	t.Parallel()
	r := invoke(t, "", "archive", "--bus", "/nonexistent", "--before", "invalid-date")
	r.mustCode(t, 2)
	r.mustContain(t, "stderr", `nova-bus archive: --before "invalid-date" is not an RFC 3339 instant; refusing to guess; run: nova-bus help`)
}

func TestArchiveRefusesRemoteWithoutBranch(t *testing.T) {
	t.Parallel()
	r := invoke(t, "", "archive", "--bus", "/nonexistent", "--before", "2026-09-08T00:00:00Z", "--remote", "origin")
	r.mustCode(t, 2)
	r.mustContain(t, "stderr", "nova-bus archive: --remote and --branch must be specified together; run: nova-bus help")
}

func TestArchiveRefusesBadAttempts(t *testing.T) {
	t.Parallel()
	r := invoke(t, "", "archive", "--bus", "/nonexistent", "--before", "2026-09-08T00:00:00Z", "--attempts", "0")
	r.mustCode(t, 2)
	r.mustContain(t, "stderr", "nova-bus archive: --attempts is a number of tries and is at least 1, got 0")
}

func TestArchiveRefusesNonexistentBusDir(t *testing.T) {
	t.Parallel()
	r := invoke(t, "", "archive", "--bus", "/nonexistent-dir-12345", "--before", "2026-09-08T00:00:00Z")
	r.mustCode(t, 2)
	r.mustContain(t, "stderr", "is not a git work tree")
}
