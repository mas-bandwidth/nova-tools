package main

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
)

// cmdUnpin removes a stored WHO pin on unstarted cards (docs/SPEC-SPRINT.md,
// WHO preference). The preview only reads; runStep records the actor and reason.
func (a *app) cmdUnpin(args []string, stdout, stderr io.Writer) int {
	fs, c := a.verbSetup("unpin")
	stream := fs.String("stream", "", "unpin unstarted cards in this stream; report each refusal")
	dry := fs.Bool("dry-run", false, "show the planned unpins and refusals without writing")
	reason := fs.String("reason", "", "required reason recorded with the dropped WHO line")
	ids, err := parse(fs, args)
	if err != nil {
		return refuse(stderr, "unpin", err.Error())
	}
	if (len(ids) == 0) == (*stream == "") || strings.TrimSpace(*reason) == "" {
		return refuse(stderr, "unpin", "wants <id>... or --stream <s>, and --reason <text>; run: nova-sprint help unpin")
	}
	st, err := a.store(*c)
	if err != nil {
		return refuse(stderr, "unpin", err.Error())
	}
	req := sprint.UnpinReq{IDs: ids, Stream: *stream, Reason: *reason, Who: c.actor}
	if *dry {
		_, snap, err := st.Check(context.Background(), 1)
		if err != nil {
			return a.readFailed("unpin", err, stderr)
		}
		plan := sprint.Unpin(snap, req)
		lines := append([]string(nil), plan.Said...)
		for _, u := range plan.Units {
			lines = append(lines, u.Moved)
		}
		for _, r := range plan.Refused {
			lines = append(lines, "REFUSED "+r.Key+": "+r.Why)
		}
		sayOK(stdout, c.json, "unpin", fmt.Sprintf("UNPIN DRY-RUN changes=%d refused=%d\n%s", len(plan.Units), len(plan.Refused), strings.Join(lines, "\n")), map[string]any{"dry_run": true, "changes": len(plan.Units), "lines": lines, "refused": plan.Refused})
		if len(plan.Refused) > 0 {
			return 1
		}
		return 0
	}
	return a.runStep("unpin", *c, st, store.UnpinStep(req), stdout, stderr)
}
