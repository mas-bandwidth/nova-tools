package verbs

import (
	"context"
	"fmt"

	"github.com/mas-bandwidth/nova-tools/internal/sprint/sprintfn"
)

// StepRefused writes "the machine's step was refused" for a verb whose step
// the store refused with a bug code (1.3.5, "A bug": "A verb that receives
// such a refusal prints it as an error (exit 3) and writes the same judgment,
// so the coordinator sees it whoever ran the verb"; the grammar decisions,
// 31). It is one step of notes only, with no read: the judgment is opened on
// the verb, the place a rule key has in the tick's (2.2), with the code as its
// cause, and its text names the verb, the op and part, the code, the bound and
// the store's words. A refusal the verb made itself (Local), or a race, is not
// a bug, and writes nothing.
func StepRefused(ctx context.Context, e *Env, rf *Refused) (Result, error) {
	if rf == nil || rf.Local || rf.Refusal == nil || IsRace(rf.Code()) {
		return Result{Verb: rf.verbName()}, nil
	}
	text := fmt.Sprintf("the machine's step was refused: verb %s, code %s", rf.Verb, rf.Code())
	if b := rf.Refusal.Detail.Budget; b != "" {
		text += ", bound " + b
	}
	if rf.Op != "" {
		text += ", op " + rf.Op
		if rf.Part > 0 {
			text += fmt.Sprintf(" part %d", rf.Part)
		}
	}
	if m := rf.Refusal.Message; m != "" {
		text += "; " + m
	}
	note := sprintfn.NoteReq{Op: sprintfn.JOpOpen, Type: typeStepRefused, Cause: rf.Code(), Subjects: []string{rf.Verb}, Text: text}
	return e.Do(ctx, Planned{Verb: rf.Verb, Plan: func(*sprintfn.ReadReply) (Part, error) {
		return Part{Req: &sprintfn.Request{Body: sprintfn.Body{Notes: []sprintfn.NoteReq{note}}}}, nil
	}})
}

// verbName is the refused verb's name, "" for no refusal.
func (r *Refused) verbName() string {
	if r == nil {
		return ""
	}
	return r.Verb
}
