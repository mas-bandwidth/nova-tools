package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"time"
)

// receiverSeatBeat publishes only a successful coordinator receiver pass
// (SPEC-BUS, Seat bus-push receipt). The source hook calls it after a read and
// delivery succeeds. A configured receiver for another name proves no seat.
func (w world) receiverSeatBeat(ctx context.Context, as string, last time.Time) (time.Time, error) {
	if w.getenv(SprintRedisEnv) == "" && w.getenv("NOVA_SPRINT_SERVER") == "" {
		return last, nil
	}
	now := w.now()
	if !last.IsZero() && now.Sub(last) < time.Minute {
		return last, nil
	}
	ctx, stop := context.WithTimeout(ctx, time.Minute)
	defer stop()
	seatRaw, err := w.seatProofCommand(ctx, "nova-sprint seat --json")
	if err != nil {
		return last, err
	}
	var seat struct {
		Holder string `json:"holder"`
	}
	if err = json.Unmarshal(seatRaw, &seat); err != nil {
		return last, fmt.Errorf("seat response is not JSON")
	}
	if seat.Holder != as {
		return last, nil
	}
	actor := oneline.ShellWord(as)
	raw, err := w.seatProofCommand(ctx, "nova-sprint seat push --actor "+actor+" --observe bus --json")
	if err != nil {
		return last, err
	}
	var observation struct {
		Seat struct {
			Holder     string `json:"holder"`
			Epoch      uint64 `json:"epoch"`
			Generation uint64 `json:"generation"`
		} `json:"seat"`
		Set struct {
			Target  string `json:"target"`
			Session string `json:"session"`
		} `json:"set"`
	}
	if err = json.Unmarshal(raw, &observation); err != nil {
		return last, fmt.Errorf("seat observation is not JSON")
	}
	if observation.Seat.Holder != as || observation.Seat.Generation == 0 {
		return last, fmt.Errorf("seat changed during the receiver pass; no proof renewed")
	}
	command := fmt.Sprintf("nova-sprint seat push --actor %s --beat bus --epoch %d --seat-generation %d --observed-target %s --observed-session %s", actor, observation.Seat.Epoch, observation.Seat.Generation, oneline.ShellWord(observation.Set.Target), oneline.ShellWord(observation.Set.Session))
	if _, err = w.seatProofCommand(ctx, command); err != nil {
		return last, err
	}
	return now, nil
}

func (w world) seatProofCommand(ctx context.Context, command string) ([]byte, error) {
	var out, errs bytes.Buffer
	code, err := w.run(ctx, command, "", &out, &errs)
	if err != nil {
		return nil, fmt.Errorf("seat receiver proof could not run: %w", err)
	}
	if code != 0 {
		return nil, fmt.Errorf("seat receiver proof command exited %d: %s", code, errs.String())
	}
	return out.Bytes(), nil
}
