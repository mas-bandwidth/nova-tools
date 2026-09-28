// Package yield is the one place a process steps behind CI (nova-tools#4293,
// Glenn 2026-09-26 ~12:00 PM ET: "CI over work is a permanent setting. It's
// a GOOD idea. because work creates more CI, so without this, it is
// unstable").
//
// Every copy the wrapper starts (nova-card, card run, card session) and every
// local test run a coordinator's child makes (nova-ci local) calls ToCI on
// itself before it execs anything: setpriority to Nice, which its children
// inherit. A CI leg runs at nice 0 and wins the cores the moment it lands;
// nothing schedules around it. This holds on every bench and every worker
// kind; it is structure, not a setting a play could forget.
package yield

import "fmt"

// Nice is the priority every copy and every local test run steps down to.
// CI legs stay at 0. 15 leaves the scheduler no doubt (three of the four
// steps between 0 and the floor) while keeping the process above the idle
// class that a nice 19 would share with backups and indexers.
const Nice = 15

// ToCI puts this process at Nice. A process already at Nice or below it
// (a child started under `nice -n 19`, say) is left where it is: raising
// priority is what an unprivileged process may not do, and the point is
// already made. Any other failure is an error the caller prints, never
// swallows; on an OS with no setpriority the error says so.
func ToCI() error {
	if err := setNice(Nice); err != nil {
		if n, err2 := currentNice(); err2 == nil && n >= Nice {
			return nil
		}
		return fmt.Errorf("nice %d: %w", Nice, err)
	}
	return nil
}
