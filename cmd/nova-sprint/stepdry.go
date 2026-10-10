package main

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
	"github.com/mas-bandwidth/nova-tools/pkg/oneline"
)

// stepDryRun names the verbs whose one write is the store step runStep runs, so
// --dry-run is that step planned on one read of the sprint and nothing written:
// the same request, the same plan function the real run commits (store.Step.Plan),
// and none of the step's commit, its packets or its after-hooks (tool ledger X12,
// docs/STANDARD.md section 2: a verb that writes takes --dry-run that writes
// nothing). A verb is listed only when nothing before or after its runStep call
// writes; a verb with a --dry-run of its own (hold, relink, the selector path of
// brief, recut, rework, return, release, rank and drop) is not.
var stepDryRun = map[string]bool{
	"accept": true, "ack": true, "ask": true, "ci": true, "friend give": true, "funded": true,
	"move": true, "priority": true, "redo": true, "resolve": true, "resume": true, "sentinel set": true,
	"set": true, "twin": true, "card base": true, "stop-return": true,
}

// stepDryWords is the --dry-run flag's description on those verbs.
const stepDryWords = "plan the step on one read of the sprint and print what it would change (WOULD lines) and refuse, and write nothing"

// planDry is a listed verb's --dry-run: the step's own plan on one read of the
// tables it loads, printed as one DRY-RUN line with a WOULD line per change, a
// NOTE line per thing the step would say and a REFUSED line per refusal; exit 1
// when the step would refuse, as the step's own would. Nothing is written.
func (a *app) planDry(verb string, c common, st *store.Store, step store.Step, stdout, stderr io.Writer) int {
	if step.Plan == nil {
		return refuse(stderr, verb, "this step has no plan to show; nothing was changed; run it without --dry-run")
	}
	snap, err := st.Load(context.Background(), step.Load, step.Extras)
	if err != nil {
		return a.readFailed(verb, err, stderr)
	}
	p := step.Plan(snap)
	var lines []string
	for _, u := range p.Units {
		if u.Moved != "" {
			lines = append(lines, "WOULD "+u.Moved)
		}
	}
	for _, w := range p.Props {
		lines = append(lines, fmt.Sprintf("WOULD SET %s %s=%s", w.Table, w.Name, w.Value))
	}
	for _, s := range p.Said {
		lines = append(lines, "NOTE "+s)
	}
	for _, r := range p.Refused {
		lines = append(lines, "REFUSED "+r.Key+": "+r.Why)
	}
	for i := range lines {
		lines[i] = oneline.Escape(lines[i])
	}
	code, status := 0, "ok"
	if len(p.Refused) > 0 {
		code, status = 1, "refused"
	}
	head := fmt.Sprintf("%s %s DRY-RUN changes=%d props=%d refused=%d; nothing was written", token(verb), strings.ToUpper(status), len(p.Units), len(p.Props), len(p.Refused))
	sayOK(stdout, c.json, verb, strings.Join(append([]string{head}, lines...), "\n"),
		map[string]any{"status": status, "exit": code, "dry_run": true, "changes": len(p.Units), "props": len(p.Props), "lines": lines, "refused": p.Refused})
	return code
}
