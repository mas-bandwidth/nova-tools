package swarm

import (
	"os"
	"strings"
	"testing"
)

// TestHostnameIsReadOnlyAtTheTwoKnownJobLeaseSites pins the enumeration this card was built
// on. `git grep -n "Hostname\|hostname" -- internal/swarm cmd/nova-swarm` at this card's
// measured head finds exactly four lines: two in `internal/swarm/lease.go` and their two
// direct callers in `internal/swarm/lease_test.go`. The two production sites are
//
//	lease.go:246 (inside startJobLeaseTicking) and lease.go:384 (inside (JobLease).reclaimable)
//
// and BOTH feed only a `JobLease.Host` field, written to the PER-JOB heartbeat lease file for
// same-host-vs-not staleness reasoning. That mechanism is wholly different from the bench-wide
// `TakeSlotLeases` capacity store in `internal/swarm/slots.go`, which never imports or reads
// `JobLease`/`.Host`, and never reads the hostname. Neither hostname read here can ever feed a
// slots owner.
//
// This test makes the "exactly two, both in lease.go" shape permanent: if a future patch adds
// an `os.Hostname()` read anywhere else in this package -- and, in particular, near the slots
// owner path -- it goes red and a person looks at why.
func TestHostnameIsReadOnlyAtTheTwoKnownJobLeaseSites(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	var hits []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		raw, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		for i, line := range strings.Split(string(raw), "\n") {
			if strings.Contains(line, "os.Hostname(") {
				hits = append(hits, name)
				t.Logf("os.Hostname( at %s:%d: %s", name, i+1, strings.TrimSpace(line))
			}
		}
	}
	if len(hits) != 2 {
		t.Fatalf("os.Hostname( is read at exactly the two known JobLease sites; found %d call(s) in %v", len(hits), hits)
	}
	for _, name := range hits {
		if name != "lease.go" {
			t.Errorf("every os.Hostname( read in internal/swarm is in lease.go (feeding only JobLease.Host); found one in %s", name)
		}
	}
}
