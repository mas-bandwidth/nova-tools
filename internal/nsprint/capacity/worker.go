package capacity

import (
	"context"
	"fmt"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
)

// worker pause|resume|show (nova-tools #4308): one verb over the paused
// flag of a worker's desired hash, friend and bench alike. A worker is what
// the code calls a consumer, friend:<f> or bench:<b>; the table's header
// already says worker. Pausing used to be capacity bench <b> 0 for a bench
// and capacity friend --paused 1 for a friend. The deal pass
// (internal/nsprint/taskcard pass.go) and the card moves' TM.room read the
// flag, so a paused worker is dealt nothing until it is resumed and keeps
// working what it already holds; the table prints paused in its status.

// Function names registered by internal/nsprint/fn/lua/capacity.lua for the
// worker verb.
const (
	// FunctionWorkerPause sets or clears paused on <kind>:<name>:desired.
	FunctionWorkerPause = "ns_worker_pause"
	// FunctionWorkerShow reads the desired record of every worker (read-only).
	FunctionWorkerShow = "ns_worker_show"
)

// Worker is one worker's desired record as worker show prints it: every
// field is the hash's text, "" when the hash lacks it.
type Worker struct {
	Kind, Name            string
	Slots                 string
	Paused                bool
	Tiers, Kinds, Machine string
}

// ID is <kind>:<name>.
func (w Worker) ID() string { return w.Kind + ":" + w.Name }

// Line is the worker show line: WORKER <id> slots=<n|-> paused=<0|1>
// tiers=<list|-> kinds=<list|-> machine=<m|->.
func (w Worker) Line() string {
	dash := func(s string) string {
		if s == "" {
			return "-"
		}
		return s
	}
	paused := "0"
	if w.Paused {
		paused = "1"
	}
	return fmt.Sprintf("WORKER %s slots=%s paused=%s tiers=%s kinds=%s machine=%s",
		w.ID(), dash(w.Slots), paused, dash(w.Tiers), dash(w.Kinds), dash(w.Machine))
}

// UnknownWorker is the refusal for a name neither registry holds and no
// desired hash names.
type UnknownWorker struct{ ID string }

func (e *UnknownWorker) Error() string {
	return "UNKNOWN " + e.ID + " is not a registered friend or bench; run nova-sprint capacity friend|bench"
}

// PauseWorker sets (paused true) or clears the paused flag of kind:name in
// one FCALL and returns the receipt word, PAUSED or RESUMED, and whether
// the flag changed (a flag already at the value writes nothing).
func PauseWorker(ctx context.Context, st *store.Store, kind, name string, paused bool, actor, idem string) (string, bool, error) {
	if st == nil {
		return "", false, fmt.Errorf("worker pause: nil store")
	}
	flag := "0"
	if paused {
		flag = "1"
	}
	reply, err := st.Client().FCall(ctx, FunctionWorkerPause, nil, kind, name, flag, actor, idem).Result()
	if err != nil {
		return "", false, fmt.Errorf("worker pause %s:%s: %w", kind, name, err)
	}
	values, err := replyValues(reply)
	if err != nil {
		return "", false, fmt.Errorf("worker pause %s:%s: %w", kind, name, err)
	}
	if len(values) < 2 {
		return "", false, fmt.Errorf("worker pause %s:%s: unexpected reply %v", kind, name, reply)
	}
	switch word := fmt.Sprint(values[0]); word {
	case "PAUSED", "RESUMED":
		return word, len(values) > 2 && fmt.Sprint(values[2]) == "1", nil
	case "UNKNOWN":
		return "", false, &UnknownWorker{ID: fmt.Sprint(values[1])}
	default:
		return "", false, fmt.Errorf("worker pause %s:%s: %s %s", kind, name, word, values[1])
	}
}

// ShowWorkers reads every worker's desired record (kind and name both "")
// or the one named, in one read-only FCALL; the rows are sorted by id.
func ShowWorkers(ctx context.Context, st *store.Store, kind, name string) ([]Worker, error) {
	if st == nil {
		return nil, fmt.Errorf("worker show: nil store")
	}
	reply, err := st.Client().FCallRo(ctx, FunctionWorkerShow, nil, kind, name).Result()
	if err != nil {
		return nil, fmt.Errorf("worker show: %w", err)
	}
	values, err := replyValues(reply)
	if err != nil {
		return nil, fmt.Errorf("worker show: %w", err)
	}
	if len(values) < 2 {
		return nil, fmt.Errorf("worker show: unexpected reply %v", reply)
	}
	switch word := fmt.Sprint(values[0]); word {
	case "WORKERS":
	case "UNKNOWN":
		return nil, &UnknownWorker{ID: fmt.Sprint(values[1])}
	default:
		return nil, fmt.Errorf("worker show: %s %s", word, values[1])
	}
	rows := values[2:]
	if len(rows)%6 != 0 {
		return nil, fmt.Errorf("worker show: unexpected reply %v", reply)
	}
	out := make([]Worker, 0, len(rows)/6)
	for i := 0; i+5 < len(rows); i += 6 {
		id := fmt.Sprint(rows[i])
		w := Worker{Slots: fmt.Sprint(rows[i+1]), Paused: fmt.Sprint(rows[i+2]) == "1",
			Tiers: fmt.Sprint(rows[i+3]), Kinds: fmt.Sprint(rows[i+4]), Machine: fmt.Sprint(rows[i+5])}
		for _, k := range []string{KindFriend, KindBench} {
			if len(id) > len(k)+1 && id[:len(k)+1] == k+":" {
				w.Kind, w.Name = k, id[len(k)+1:]
			}
		}
		if w.Kind == "" {
			return nil, fmt.Errorf("worker show: unexpected worker %q", id)
		}
		out = append(out, w)
	}
	return out, nil
}
