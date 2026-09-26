//go:build functional

package main

// Rowan's failure-guidance audit (2026-09-26): `nova-work ready --graph`
// names a blocked node's resolver as "nova-merge queue" (open need) or
// "nova-sprint card harvest" (merged need) -- internal/jobs/jobs.go:403-409,
// internal/worklang/graph.go:276-280 -- and neither verb exists: nova-merge
// answers `unknown subcommand "queue"`, nova-sprint card `unknown verb
// harvest`. The blocked-dependency guidance is a dead end. This probe builds
// both binaries and runs the resolver it is given with -h.

import (
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestRowanAuditBlockerResolverVerbExists(t *testing.T) {
	t.Parallel()
	seed := writeSeed(t, openNeedSeed)
	_, stdout, _ := invoke("ready", "--graph", seed, "--node", "a")
	m := regexp.MustCompile(`resolver="([^"]+)"`).FindStringSubmatch(stdout)
	if m == nil {
		t.Fatalf("ready row names no resolver: %q", stdout)
	}
	argv := strings.Fields(m[1])
	bin := filepath.Join(t.TempDir(), argv[0])
	if out, err := exec.Command("go", "build", "-o", bin, "../"+argv[0]).CombinedOutput(); err != nil {
		t.Fatalf("build %s: %v %s", argv[0], err, out)
	}
	out, _ := exec.Command(bin, append(argv[1:], "-h")...).CombinedOutput()
	if s := string(out); strings.Contains(s, "unknown subcommand") || strings.Contains(s, "unknown verb") {
		t.Fatalf("ready names resolver %q for a blocked node; it answers %q", m[1], strings.TrimSpace(s))
	}
}
