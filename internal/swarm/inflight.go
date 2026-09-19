package swarm

import (
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// THE PER-ROUTE IN-FLIGHT CAP (nova-tools#917).
//
// Measured 2026-09-17 03:30-03:55Z: ~100 Muse cards on
// `opencode/muse-spark-1.3-contributor-free` across five machines. hulk and vision produced
// ZERO results in thirteen minutes at load 0.5-2.0 -- the harnesses idle, waiting on the
// model -- with their logs frozen mid-tool-call at an output age of 783 s. A fresh
// known-answer card on hulk hung for its whole 150 s deadline while deepseek-flash on the
// same bench in the same second answered in 11 s. Space, which started earlier with fewer in
// flight, finished 10 of 17.
//
// SO THE TIER DOES NOT REFUSE; IT QUEUES. Above roughly 30-40 concurrent requests on one key
// the tail latency goes to infinity, and every card launched past that point burns its whole
// deadline for nothing -- and is paid for. A launcher with no cap converts a free tier's
// queue into spend.
//
// The cap is per ROUTE, and a route is the triple that shares a queue: the provider, the
// model, and the KEY. Two models on one key share that key's limit, and the same model on
// two keys does not -- so neither the model alone nor the key alone is the unit.
//
// WHAT THIS IS NOT. It is not a rate limit and it is not a retry policy: it is the count of
// requests this launcher has in flight at once, which is the quantity the measurement above
// is about. And it is not a deadline -- a card held here has not started, is not being paid
// for, and its deadline has not begun.
type inflight struct {
	mu     sync.Mutex
	wake   *sync.Cond
	cap    int            // 0 means no cap: every acquire succeeds at once
	held   map[string]int // route key -> how many are in flight now
	peak   map[string]int // route key -> the most ever in flight at once
	waited map[string]int // route key -> how many acquires had to wait
	closed bool
}

// newInflight is a cap of n per route. n <= 0 is no cap at all, which is what every caller
// that has not asked for one gets: today's behaviour, byte for byte.
func newInflight(n int) *inflight {
	f := &inflight{cap: n, held: map[string]int{}, peak: map[string]int{}, waited: map[string]int{}}
	f.wake = sync.NewCond(&f.mu)
	return f
}

// RouteKey is the provider, model and key a card's requests share a queue with. The key is
// named by its PROFILE and never by its value: this string is printed on a STATUS line, and
// a secret that can reach a log is a secret that will.
func RouteKey(model, authProfile string) string {
	model = strings.TrimSpace(model)
	if model == "" {
		model = "-"
	}
	if authProfile = strings.TrimSpace(authProfile); authProfile == "" {
		authProfile = "-"
	}
	return model + "@" + authProfile
}

// acquire takes one of the route's slots, waiting until there is one. It returns false when
// the batch gave up waiting -- the deadline passed and `close` was called -- and then the
// caller must not launch.
//
// THE WAIT IS NOT A TIMEOUT. A card held here is holding nothing else: no process, no slot
// lease, no spend. It waits as long as the batch does, and the batch's own deadline is what
// ends it, through `close`.
func (f *inflight) acquire(route string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.cap <= 0 {
		f.held[route]++
		f.note(route)
		return true
	}
	waited := false
	for f.held[route] >= f.cap && !f.closed {
		waited = true
		f.wake.Wait()
	}
	if f.closed {
		return false
	}
	if waited {
		f.waited[route]++
	}
	f.held[route]++
	f.note(route)
	return true
}

// note records the high-water mark, under the lock the caller already holds.
func (f *inflight) note(route string) {
	if f.held[route] > f.peak[route] {
		f.peak[route] = f.held[route]
	}
}

// release gives one slot back. It is called EXACTLY ONCE per successful acquire, when the
// card's process has ended however it ended -- finished, killed for idleness, killed at the
// deadline. A release that does not happen is a route that never launches again.
func (f *inflight) release(route string) {
	f.mu.Lock()
	if f.held[route] > 0 {
		f.held[route]--
	}
	f.wake.Broadcast()
	f.mu.Unlock()
}

// close wakes every waiter and refuses every acquire from now on. The batch calls it when
// its deadline has passed: a card still held at that point is never going to run, and a
// launcher goroutine blocked on a condition variable is a batch that does not return.
func (f *inflight) close() {
	f.mu.Lock()
	f.closed = true
	f.wake.Broadcast()
	f.mu.Unlock()
}

// statusLine is what the batch prints about the cap, once, in route order:
//
//	BATCH ROUTE <route> cap=<n> peak=<n> held-back=<n>
//
// `peak` is the most this launcher ever had in flight on that route and `held-back` is how
// many launches had to wait for a slot. Together they say whether the cap bound anything:
// a route whose peak is below the cap and whose held-back is zero was never the constraint,
// and a reader chasing a slow batch can stop looking here.
func (f *inflight) statusLines() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.cap <= 0 {
		return nil
	}
	routes := make([]string, 0, len(f.peak))
	for r := range f.peak {
		routes = append(routes, r)
	}
	sort.Strings(routes)
	out := make([]string, 0, len(routes))
	for _, r := range routes {
		// THE ROUTE IS ESCAPED WHERE IT IS PRINTED, never where it is keyed: the profile half
		// is a path a person typed on `--auth` and may carry a space, a tab or a newline, and
		// this line is read by splitting on spaces. A raw route turns one line into two.
		out = append(out, fmt.Sprintf("BATCH ROUTE %s cap=%d peak=%d held-back=%d", oneline.Field(r), f.cap, f.peak[r], f.waited[r]))
	}
	return out
}
