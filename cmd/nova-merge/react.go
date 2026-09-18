package main

// react is this lane's subscriber (docs/SPEC-JOBS.md, "Events, not ticks"). It holds no
// timer: it blocks on the pub/sub channels until a message lands or its --deadline is
// reached, and it acts once per message. A pr-checks-done success enqueues the PR unless
// a skip set member or a live hold key stops it; a dev-moved asks for a rebase unit for
// every PR the move made DIRTY; a card-done does nothing, because the recorder and the
// harvester read the stream directly.
//
// The lane is optional. With --lane the reactor reads the lane's repository and base so
// the dev-moved arm can list the DIRTY PRs; without it only the enqueue arm runs.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/ci"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

func cmdReact(args []string, stdout, stderr io.Writer, deps Deps) int {
	f := newFlags("react")
	addr := f.fs.String("redis", "", "")
	lane := f.fs.String("lane", "", "")
	deadline := f.fs.Int("deadline", 60, "")
	timeout := f.fs.Int("timeout", 120, "")
	once := f.fs.Bool("once", false, "")
	if !f.parse(args, stderr) {
		return 2
	}
	f.require("redis", *addr, "the address of the pub/sub instance this reactor subscribes to")
	if *deadline < 1 {
		f.problem(fmt.Sprintf("--deadline is a whole number of seconds the reactor waits before returning, got %d; a loop with no deadline is a process nobody can tell from a stuck one", *deadline))
	}
	if *timeout < 1 {
		f.problem(fmt.Sprintf("--timeout wants a whole number of seconds, got %d", *timeout))
	}
	if !f.done(stderr) {
		return 2
	}

	rdb := deps.Dial(*addr)
	defer rdb.Close()

	var forge ci.Forge
	if *lane != "" {
		st, code := openLane("react", *lane, stderr)
		if st == nil {
			return code
		}
		forge = deps.Forge(st.Repo, st.Base, time.Duration(*timeout)*time.Second)
	}

	r := ci.NewReactor(rdb, forge, nil, stdout)
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(*deadline)*time.Second)
	defer cancel()

	if *once {
		if err := r.RunOnce(ctx); err != nil && !isDeadline(err) {
			fmt.Fprintf(stderr, "REACT FAIL: %s\n", oneline.Err(err))
			return 1
		}
		fmt.Fprintf(stdout, "REACT OK once=true\n")
		return 0
	}
	if err := r.Run(ctx); err != nil && !isDeadline(err) {
		fmt.Fprintf(stderr, "REACT FAIL: %s\n", oneline.Err(err))
		return 1
	}
	fmt.Fprintf(stdout, "REACT OK once=false deadline=%ds\n", *deadline)
	return 0
}

// isDeadline is the reactor's quiet deadline: a watch that never changed returns once at
// its bound, and returning at the bound is the design and not a failure.
func isDeadline(err error) bool {
	return errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled)
}
