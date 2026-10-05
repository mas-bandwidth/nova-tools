package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// cmdServerSwitch switches the server binary to <binary>, keeping the previous binary
// and rolling back if a land fails within the window (docs/SPEC-SPRINT.md section 14).
func (a *app) cmdServerSwitch(args []string, stdout, stderr io.Writer) int {
	fs, _ := a.verbSetup("server switch")
	rollback := fs.Bool("rollback", false, "roll back to previous binary, or enable automatic rollback on failed land in window")
	windowStr := fs.String("window", "15m", "rollback window duration: if a land fails within this window, roll back")
	target := fs.String("target", "", "target binary to replace (default: this binary or NOVA_SPRINT_SERVER_BIN)")

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

	if *rollback {
		fmt.Fprintf(stdout, "SERVER SWITCH OK target %s switched to %s (previous kept at %s.prev; rollback window %s)\n", targetPath, candidate, targetPath, window)
	} else {
		fmt.Fprintf(stdout, "SERVER SWITCH OK target %s switched to %s\n", targetPath, candidate)
	}
	return 0
}
