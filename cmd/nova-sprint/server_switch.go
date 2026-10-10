package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/pkg/oneline"
)

// cmdServerSwitch switches the server binary to <binary>, keeping the previous binary
// and rolling back if a land fails within the window (docs/SPEC-SPRINT.md section 14).
// Before the swap it runs the candidate's shadow tick against the store, read-only, and
// refuses the swap when the shadow fails (shadow.go; install-canary-shadow-tick-r.w1).
func (a *app) cmdServerSwitch(args []string, stdout, stderr io.Writer) int {
	fs, c := a.verbSetup("server switch")
	rollback := fs.Bool("rollback", false, "roll back to previous binary, or enable automatic rollback on failed land in window")
	windowStr := fs.String("window", "15m", "rollback window duration: if a land fails within this window, roll back")
	target := fs.String("target", "", "target binary to replace (default: this binary or NOVA_SPRINT_SERVER_BIN)")
	dry := fs.Bool("dry-run", false, "run the candidate's shadow tick (read-only) and say what would be switched; switch, roll back and write nothing")
	tickDeadline := fs.Duration("tick-deadline", TickDeadline, "the candidate's shadow tick (<binary> tick --shadow, read-only, against the store --redis names) must end in this long, or the switch is refused")

	pos, err := parse(fs, args)
	if err != nil {
		return refuse(stderr, "server switch", err.Error())
	}

	targetPath := *target
	if targetPath == "" {
		targetPath = a.getenv("NOVA_SPRINT_SERVER_BIN")
		if targetPath == "" {
			exe := a.executable
			if exe == nil {
				exe = os.Executable
			}
			var err error
			targetPath, err = exe()
			if err != nil {
				return refuse(stderr, "server switch", "cannot determine target binary: "+err.Error())
			}
		}
	}

	// Case 1: Manual rollback requested with no binary
	if len(pos) == 0 && *rollback {
		if *dry {
			if _, err := os.Stat(targetPath + ".prev"); err != nil {
				return refuse(stderr, "server switch", "no previous binary to roll back to: "+oneline.Err(err)+"; nothing was changed")
			}
			fmt.Fprintf(stdout, "SERVER SWITCH DRY-RUN rollback target=%s from=%s.prev; nothing was changed\n", targetPath, targetPath)
			return 0
		}
		if err := sprint.ServerRollback(context.Background(), targetPath); err != nil {
			fmt.Fprintf(stderr, "%s server switch: rollback failed: %s\n", prog, oneline.Escape(err.Error()))
			return 1
		}
		fmt.Fprintf(stdout, "SERVER SWITCH ROLLED BACK target %s restored from %s.prev\n", targetPath, targetPath)
		return 0
	}

	if len(pos) == 0 {
		return refuse(stderr, "server switch", "wants <binary> [--rollback] or --rollback alone; run: nova-sprint server switch -h")
	}

	if len(pos) > 1 {
		return refuse(stderr, "server switch", "takes one <binary>, found "+pos[1])
	}

	candidate := pos[0]

	window, err := time.ParseDuration(*windowStr)
	if err != nil || window <= 0 {
		return refuse(stderr, "server switch", "invalid --window duration: "+*windowStr)
	}

	if *tickDeadline <= 0 {
		return refuse(stderr, "server switch", "invalid --tick-deadline: the candidate's shadow tick is given a deadline")
	}
	// the canary: the candidate plans a tick on the store, applying nothing, before
	// anything on disk changes; a refusal leaves the old server as it was
	shadowAt := a.now()
	plan, wall, err := runShadow(context.Background(), candidate, c.redis, *tickDeadline)
	if err != nil {
		fmt.Fprintf(stderr, "%s server switch REFUSED: the shadow tick of %s %s; %s is unchanged and the old server keeps running; remedy: verify the candidate binary with %s tick --shadow before switching; run: nova-sprint server switch -h\n", prog, candidate, oneline.Escape(err.Error()), targetPath, candidate)
		return 1
	}
	fmt.Fprintf(stdout, "SHADOW TICK OK binary=%s epoch=%d state=%s parts=%d size=%d took=%s wall=%s\n", candidate, plan.Epoch, plan.State, len(plan.Parts), plan.Size, plan.Took.Round(time.Millisecond), wall.Round(time.Millisecond))

	if *dry {
		fmt.Fprintf(stdout, "SERVER SWITCH DRY-RUN target=%s binary=%s rollback=%t window=%s; nothing was switched or written\n", targetPath, candidate, *rollback, window)
		return 0
	}

	err = sprint.ServerSwitch(context.Background(), sprint.ServerSwitchOptions{
		Binary:   candidate,
		Target:   targetPath,
		Rollback: *rollback,
		Window:   window,
		Now:      a.now,
		Stdout:   stdout,
		Stderr:   stderr,
	})
	if err != nil {
		fmt.Fprintf(stderr, "%s server switch FAILED: %s\n", prog, oneline.Escape(err.Error()))
		return 1
	}
	if err := writeShadowRecord(targetPath, shadowRecord{Binary: candidate, At: shadowAt, Wall: wall, Plan: plan}); err != nil {
		fmt.Fprintf(stderr, "%s server switch: switched, and the shadow record %s.shadow.json was not written: %s\n", prog, targetPath, oneline.Escape(err.Error()))
	}

	if *rollback {
		fmt.Fprintf(stdout, "SERVER SWITCH OK target %s switched to %s (previous kept at %s.prev; rollback window %s)\n", targetPath, candidate, targetPath, window)
	} else {
		fmt.Fprintf(stdout, "SERVER SWITCH OK target %s switched to %s\n", targetPath, candidate)
	}
	return 0
}
