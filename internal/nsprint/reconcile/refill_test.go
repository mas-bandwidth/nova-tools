package reconcile_test

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/deal"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/reconcile"
)

// fakeDialer is the fixture benches' sshd: every session succeeds and keeps
// the `card launch --stdin` lines it carried (CI-NET: no host in a test).
type fakeDialer struct {
	mu       sync.Mutex
	sessions map[string]int      // bench -> sessions run
	lines    map[string][]string // bench -> launch lines
}

func (d *fakeDialer) Dial(b deal.Bench) deal.Session { return &fakeSession{d: d, bench: b.Name} }

type fakeSession struct {
	d     *fakeDialer
	bench string
}

func (s *fakeSession) Run(_ context.Context, stdin []byte) error {
	s.d.mu.Lock()
	defer s.d.mu.Unlock()
	if s.d.sessions == nil {
		s.d.sessions, s.d.lines = map[string]int{}, map[string][]string{}
	}
	s.d.sessions[s.bench]++
	for _, l := range strings.Split(strings.TrimSpace(string(stdin)), "\n") {
		if l != "" {
			s.d.lines[s.bench] = append(s.d.lines[s.bench], l)
		}
	}
	return nil
}

func (d *fakeDialer) count(bench string) (sessions, lines int) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.sessions[bench], len(d.lines[bench])
}

// TestRefillClassify: which events wake the deal, which the route, and which
// nothing (the reconciler's own receipts and beats).
func TestRefillClassify(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		stream string
		v      map[string]any
		want   string
	}{
		{"cap:log", map[string]any{"kind": "slot-freed", "target": "bench:hulk", "actor": "card"}, reconcile.WakeDeal},
		{"cap:log", map[string]any{"kind": "capacity", "target": "bench:hulk", "actor": "rowan"}, reconcile.WakeDeal},
		{"cap:log", map[string]any{"kind": "slot-freed", "target": "bench:hulk", "actor": "reconciler"}, reconcile.WakeNone},
		{"cap:log", map[string]any{"kind": "slot-freed", "consumer": "friend:stella"}, reconcile.WakeRoute},
		{"s:x:log", map[string]any{"kind": "card push", "actor": "rowan"}, reconcile.WakeDeal},
		{"s:x:log", map[string]any{"kind": "card deal", "actor": "reconciler"}, reconcile.WakeNone},
		{"s:x:log", map[string]any{"kind": "card beat", "actor": "card"}, reconcile.WakeNone},
		{"s:x:log", map[string]any{"kind": "task push", "actor": "rowan"}, reconcile.WakeRoute},
		{"s:x:log", map[string]any{"kind": "sprint resume", "actor": "rowan"}, reconcile.WakeDeal},
	} {
		if got := reconcile.Classify(tc.stream, tc.v, reconcile.DefaultActor); got != tc.want {
			t.Errorf("Classify(%s, %v) = %q, want %q", tc.stream, tc.v, got, tc.want)
		}
	}
}
