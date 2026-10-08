package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

func (a *app) cmdMachinePause(args []string, stdout, stderr io.Writer) int {
	return a.setPaused("pause", true, args, stdout, stderr)
}

func (a *app) cmdMachineUnpause(args []string, stdout, stderr io.Writer) int {
	return a.setPaused("unpause", false, args, stdout, stderr)
}

// setPaused is the admission-only store write in SprintPause.tla and
// SPEC-SPRINT section 14. It cannot start a STOPPED machine.
func (a *app) setPaused(name string, paused bool, args []string, stdout, stderr io.Writer) int {
	fs, c := a.verbSetup(name)
	pos, err := parse(fs, args)
	if err != nil {
		return refuse(stderr, name, err.Error())
	}
	if len(pos) != 0 {
		return refuse(stderr, name, "takes no words; run: nova-sprint "+name)
	}
	st, err := a.store(*c)
	if err != nil {
		return refuse(stderr, name, err.Error())
	}
	ctx := context.Background()
	before, after, res, err := st.SetPaused(ctx, paused)
	out := machineOut{Before: before.StateWord(), After: after.StateWord(), Changed: before.Paused != after.Paused, Notes: res.Notes, Sprint: sprintLine(ctx, st)}
	if err != nil {
		out.Error = err.Error()
		w := stderr
		if c.json {
			w = stdout
		}
		machinePauseRefusal(w, name, c.json, out)
		if !before.Running() && !before.Since.IsZero() {
			return 1
		}
		return 2
	}
	if c.json {
		b, _ := json.Marshal(out)
		fmt.Fprintln(stdout, string(b))
	} else {
		what := "changed"
		if !out.Changed {
			what = "unchanged: the machine is " + after.StateWord() + " already"
		}
		fmt.Fprintf(stdout, "%s OK before=%s after=%s %s\n", token(name), out.Before, out.After, what)
		if out.Sprint != "" {
			fmt.Fprintln(stdout, out.Sprint)
		}
	}
	return 0
}

// machinePauseRefusal renders one refusal with the same remedy in both forms.
func machinePauseRefusal(w io.Writer, name string, jsonOut bool, result machineOut) {
	result.Error = oneline.WithRemedy(result.Error, prog+" "+name+" -h")
	if jsonOut {
		b, _ := json.Marshal(result)
		fmt.Fprintln(w, string(b))
	} else {
		fmt.Fprintf(w, "%s %s: %s\n", prog, name, result.Error)
	}
}
