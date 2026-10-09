package main

import (
	"context"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
)

// pauseAdmission gives an early refusal; landAdmission commits SprintPause.tla
// Admit before external work. A batch admitted already keeps push and report.
func (l *lander) pauseAdmission(ctx context.Context) string {
	l.a.serial.Lock()
	m, _, err := l.st.Machine(ctx)
	l.a.serial.Unlock()
	if err != nil {
		return "landing admission cannot read the machine: " + oneline.Err(err) + "; queued cards stay; run: nova-sprint where"
	}
	if !m.Running() {
		return "the machine is STOPPED: new landings are held; queued cards stay; run: nova-sprint start after settling STOP receipts"
	}
	if m.Paused {
		return "the machine is PAUSED: new landings are held; queued cards stay; run: nova-sprint unpause"
	}
	return ""
}

// landAdmission accepts this exact logical task under the store's pause fence.
// Every preparation uses a fresh operation: a later pass cannot reuse an old
// --op receipt to start a different cut while paused. Run retries its own
// operation without changing the batch arguments (SPEC-SPRINT section 14).
func (l *lander) landAdmission(ctx context.Context, stream string, cards []landCard) string {
	r := store.LandAdmissionReq{Stream: stream, Repo: cards[0].repo, Base: cards[0].base, Check: l.check}
	for _, c := range cards {
		r.Pins = append(r.Pins, store.LandAdmissionPin{ID: c.id, Attempt: c.attempt, Head: c.head, BriefSHA256: store.LandBriefSHA256(c.brief)})
	}
	step := store.LandAdmissionStep(r)
	epoch := l.epoch
	step.Epoch = &epoch
	l.a.serial.Lock()
	res, err := l.st.Run(ctx, step)
	l.a.serial.Unlock()
	if err != nil {
		return "landing admission cannot commit: " + oneline.Err(err) + "; queued cards stay; run land again"
	}
	if len(res.Refused) > 0 {
		var why []string
		for _, f := range res.Refused {
			why = append(why, f.Key+": "+f.Why)
		}
		return strings.Join(why, "; ")
	}
	if res.Op == "" {
		return "landing admission has no durable operation receipt; queued cards stay; run land again"
	}
	return ""
}
