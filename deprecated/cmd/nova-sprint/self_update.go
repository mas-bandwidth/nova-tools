// self update (#4337) is rebuild.sh as a verb: the coordinator's own
// nova-sprint rebuilt at a dev commit with the module's pinned Go and
// installed by rename, never cp over the live binary
// (internal/nsprint/fleetbuild/selfupdate.go). The fleet play does the
// benches; this does the machine it runs on.
//
//	self update [--sha <sha>] [--from <checkout>] [--allow-branch]
//
// With no --from it builds in the release clone fleet release uses, at --sha
// or dev's tip. The loops on this machine keep running the old binary until
// kickstarted; fleet release <sha> does both as its self step.
//
// Exit 0 installed or already current (SELF UPDATE OK <old> -> <new>, or
// SELF UPDATE SKIPPED); 1 a refusal (SELF UPDATE REFUSED: <why> (<remedy>));
// 2 usage.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fleetbuild"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/taskcard"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/verbflag"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

func init() {
	register(Verb{
		Name:    "self",
		Summary: "update [--sha <sha>] [--from <checkout>] [--allow-branch]: rebuild this machine's nova-sprint with the pinned Go and install it by rename, printing old -> new",
		Run:     runSelf,
	})
}

// selfDeps is everything self update reaches outside the process; a test
// hands in fakes per call.
type selfDeps struct {
	Runner fleetbuild.ExecRunner
	Home   func() (string, error)
	PID    int
}

func runSelf(ctx context.Context, args []string, out, errOut io.Writer) int {
	return runSelfWith(ctx, args, out, errOut, selfDeps{Runner: releaseExec{}, Home: fleetBuildHome, PID: os.Getpid()})
}

func runSelfWith(ctx context.Context, args []string, out, errOut io.Writer, deps selfDeps) int {
	if len(args) == 0 || args[0] != "update" {
		return refuse(errOut, "self", "wants the subverb update: nova-sprint self update [--sha <sha>] [--from <checkout>] [--allow-branch]")
	}
	fs := verbflag.New("self update")
	sha := fs.String("sha", "", "the commit to build (default: dev's tip in the release clone, HEAD with --from)")
	from := fs.String("from", "", "a nova-tools checkout built as it stands (default: the release clone)")
	allow := fs.Bool("allow-branch", false, "install a commit off origin/dev or an edited --from tree")
	pos, err := parseInterspersed(fs, args[1:])
	if err != nil {
		return refuse(errOut, "self update", err.Error())
	}
	if len(pos) > 0 {
		return refuse(errOut, "self update", "takes no arguments; the commit is --sha <sha>")
	}
	home, err := deps.Home()
	if err != nil {
		return selfRefused(errOut, fmt.Errorf("no home directory: %v", err))
	}
	var duty *taskcard.Duty
	var st *store.Store
	start := time.Now()
	if redisAddr := redisDefault(); redisAddr != "" {
		if s, err := store.Open(ctx, redisAddr); err == nil {
			st = s
			defer st.Close()
			duty, _ = taskcard.StartDuty(ctx, st.Client(), taskcard.DutyOpts{
				Stream: "ops",
				Op:     "self-update",
				Actor:  "rowan",
				Title:  "self update",
				Now:    start,
			})
		}
	}

	s := &fleetbuild.SelfUpdate{Runner: deps.Runner, Home: home, From: *from, Sha: *sha,
		AllowBranch: *allow, PID: deps.PID, Out: out}
	res, err := s.Run(ctx)
	if err != nil {
		if duty != nil && st != nil {
			_ = taskcard.CancelDuty(ctx, st.Client(), duty.ID, "rowan", "self update refused")
		}
		return selfRefused(errOut, err)
	}
	wall := time.Since(start)
	if res.Skipped {
		fmt.Fprintf(out, "SELF UPDATE SKIPPED %s already answers %s commit=%s\n", res.Bin, res.New, res.Commit[:12])
		if duty != nil && st != nil {
			_ = taskcard.LandDuty(ctx, st.Client(), duty.ID, "rowan", res.Commit, "self update skipped", wall)
		}
		return 0
	}
	fmt.Fprintf(out, "SELF UPDATE OK %s -> %s commit=%s toolchain=%s bin=%s\n", res.Old, res.New, res.Commit[:12], res.Toolchain, res.Bin)
	if duty != nil && st != nil {
		_ = taskcard.LandDuty(ctx, st.Client(), duty.ID, "rowan", res.Commit, "self update ok", wall)
	}
	return 0
}

func selfRefused(errOut io.Writer, err error) int {
	msg := err.Error()
	if errors.Is(err, fleetbuild.ErrRefused) {
		msg = strings.TrimPrefix(msg, fleetbuild.ErrRefused.Error()+": ")
	}
	fmt.Fprintf(errOut, "SELF UPDATE REFUSED: %s\n", oneline.Escape(msg))
	return 1
}
