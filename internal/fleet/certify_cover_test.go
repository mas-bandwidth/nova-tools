package fleet

// The unit tests for the two certificate-currency helpers that decide whether a machine's
// certificate is stale: Stale, the boolean pulse.Fill asks before a card reaches a machine,
// and staleClasses, the list `certify --if-stale` prints. Both are pure functions over a
// certificate slice and a fixed clock, so no test here starts a program, opens a socket or
// reads the host's time.
//
// A certificate is current only when Certified accepts it and it has not aged out; there is
// no error path to refuse, so the "refusal" a case covers is the negative answer: a missing,
// failed, superseded or aged row. Stale says so, and staleClasses names every class that is
// not current, in class order.

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// certAt is one certificate row for the tests below: machine "hulk", class "go-test", build
// "v1" and standard "h", written at the given offset before now.
func certAt(now time.Time, class, build, hash, verdict string, age time.Duration) Certificate {
	return Certificate{
		Machine: "hulk", Class: class, Build: build, Hash: hash,
		Verdict: verdict, Evidence: "go version go1.26.5", At: now.Add(-age),
	}
}

// TestCertifyCoverStale pins the one place "current" is decided for the trigger: most-recent
// data is current; a row that is missing, failed, superseded, from another machine or aged
// out is stale; and the exact max-age boundary is still current, because the comparison is
// `<=`.
func TestCertifyCoverStale(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)
	age := 24 * time.Hour

	for _, tc := range []struct {
		name  string
		certs []Certificate
		want  bool
	}{
		{
			name:  "an OK row inside the age is current",
			certs: []Certificate{certAt(now, "go-test", "v1", "h", VerdictOK, time.Hour)},
		},
		{
			name:  "a WARN row is a pass and is current",
			certs: []Certificate{certAt(now, "go-test", "v1", "h", VerdictWarn, time.Hour)},
		},
		{
			name:  "a row exactly at max age is still current",
			certs: []Certificate{certAt(now, "go-test", "v1", "h", VerdictOK, age)},
		},
		{
			name:  "an aged row is stale",
			certs: []Certificate{certAt(now, "go-test", "v1", "h", VerdictOK, age+time.Second)},
			want:  true,
		},
		{
			name:  "a class nobody ran is stale",
			certs: []Certificate{certAt(now, "sbcl", "v1", "h", VerdictOK, time.Hour)},
			want:  true,
		},
		{
			name:  "a FAIL row is stale",
			certs: []Certificate{certAt(now, "go-test", "v1", "h", VerdictFail, time.Hour)},
			want:  true,
		},
		{
			name:  "a row under the superseded build is stale",
			certs: []Certificate{certAt(now, "go-test", "v2", "h", VerdictOK, time.Hour)},
			want:  true,
		},
		{
			name:  "a row under the superseded standard is stale",
			certs: []Certificate{certAt(now, "go-test", "v1", "g", VerdictOK, time.Hour)},
			want:  true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := Stale(tc.certs, "hulk", "go-test", "v1", "h", now, age)
			assert.Equal(t, tc.want, got, "Stale = %v, want %v", got, tc.want)
		})
	}
}

// TestCertifyCoverStaleClasses pins staleClasses: it names every class of the machine that is
// not current, in the order the workloads are given, and nothing when every class is current.
func TestCertifyCoverStaleClasses(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)
	age := 24 * time.Hour
	m := Machine{Name: "hulk", Roles: []string{"bench", "runner"}}

	for _, tc := range []struct {
		name  string
		certs []Certificate
		loads []Workload
		want  []string
	}{
		{
			name: "the current class is left out and the stale classes stay in class order",
			certs: []Certificate{
				certAt(now, "go-test", "v1", "h", VerdictOK, time.Hour),
				certAt(now, "sbcl", "v1", "h", VerdictFail, time.Hour),
			},
			loads: []Workload{{Class: "go-test"}, {Class: "sbcl"}, {Class: "c-build"}},
			want:  []string{"sbcl", "c-build"},
		},
		{
			name:  "every class current names nothing",
			certs: []Certificate{certAt(now, "go-test", "v1", "h", VerdictOK, time.Hour)},
			loads: []Workload{{Class: "go-test"}},
			want:  nil,
		},
		{
			name:  "a machine with no workloads names nothing",
			certs: nil,
			loads: nil,
			want:  nil,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := staleClasses(tc.certs, m, tc.loads, "v1", "h", now, age)
			assert.Equal(t, tc.want, got, "staleClasses = %v, want %v", got, tc.want)
		})
	}
}
