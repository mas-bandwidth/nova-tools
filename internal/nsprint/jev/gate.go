package jev

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// TypeGate is Jev's mechanical pass on a PR head (`nova-sprint jev mech`,
// internal/jev): its gate=ok|fail is a decision, recorded at mech, and the
// head's fate is its outcome, joined at land (ok: the head landed).
const TypeGate = "gate"

// GateSubject is a gate row's subject: the PR and the head's first 12 hex.
func GateSubject(name string, n int, head string) string {
	head = strings.ToLower(strings.TrimSpace(head))
	if len(head) > 12 {
		head = head[:12]
	}
	return name + "#" + strconv.Itoa(n) + "@" + head
}

// RecordGate is jev mech's one call: the gate word as the rules answer on the
// PR head's row, the JEV line as its state. A new mech line at the same head
// replaces the row.
func RecordGate(ctx context.Context, c redis.Cmdable, name string, n int, head, gate, line string) error {
	return Record(ctx, c, Decision{Type: TypeGate, Subject: GateSubject(name, n, head), State: line, Rules: gate,
		Fields: map[string]string{"pr": name + "#" + strconv.Itoa(n), "head": strings.ToLower(head)}})
}

// GateHead is one landed PR head.
type GateHead struct {
	N    int
	Head string
}

// JoinGates is the lander's one call at land: outcome on the gate row of
// every head that has one (one pipeline to read, one MULTI to write); a head
// with no row (mech never ran on it) is counted, not an error. A row already
// holding the same outcome is left as it is.
func JoinGates(ctx context.Context, c redis.Cmdable, name string, heads []GateHead, outcome, by, why string) (joined, missing int, err error) {
	if len(heads) == 0 {
		return 0, 0, nil
	}
	got := make([]*redis.SliceCmd, len(heads))
	if _, err := c.Pipelined(ctx, func(p redis.Pipeliner) error {
		for i, h := range heads {
			got[i] = p.HMGet(ctx, RowKey(TypeGate, GateSubject(name, h.N, h.Head)), "type", "outcome")
		}
		return nil
	}); err != nil {
		return 0, 0, fmt.Errorf("jev join gates: %w", err)
	}
	var outs []Outcome
	for i, h := range heads {
		v := got[i].Val()
		if len(v) < 2 || v[0] == nil {
			missing++
			continue
		}
		if prev, _ := v[1].(string); prev == outcome {
			continue
		}
		outs = append(outs, Outcome{Type: TypeGate, Subject: GateSubject(name, h.N, h.Head), Outcome: outcome, By: by, Why: why})
	}
	if len(outs) == 0 {
		return 0, missing, nil
	}
	at := time.Now().UnixMilli()
	if _, err := c.TxPipelined(ctx, func(p redis.Pipeliner) error {
		for _, o := range outs {
			joinCmds(ctx, p, o, at)
		}
		return nil
	}); err != nil {
		return 0, missing, fmt.Errorf("jev join gates: %w", err)
	}
	return len(outs), missing, nil
}
