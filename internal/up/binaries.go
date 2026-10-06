package up

import (
	"fmt"
	"strings"
)

// The binaries step: every program the later steps run is on PATH and
// answers its version. nova-up installs nothing: a program that is not
// there is missing, with the command that installs it, and the run stops
// before any step applies (docs/SPEC-UP.md "Steps", 3).
func init() {
	Register(Step{Name: "binaries", Order: 30, Plan: planBinaries, Apply: func(*Env) error { return nil }})
}

func planBinaries(e *Env) Finding {
	var absent, installs []string
	for _, t := range tools {
		p, err := e.Exec.LookPath(t.name)
		if err == nil {
			_, err = e.Run(Cmd{Name: p, Args: t.version})
		}
		if err != nil {
			absent = append(absent, t.name)
			installs = append(installs, t.install(e.GOOS))
		}
	}
	if len(absent) > 0 {
		return Finding{Missing, fmt.Sprintf("%s not on PATH or not answering its version; install: %s",
			strings.Join(absent, ","), strings.Join(installs, " && "))}
	}
	return Finding{OK, fmt.Sprintf("%d on PATH, each answering its version", len(tools))}
}
