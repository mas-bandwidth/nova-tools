package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"time"

	"github.com/mas-bandwidth/nova-tools/hook"
	"github.com/mas-bandwidth/nova-tools/pkg/atomicfile"
	"github.com/mas-bandwidth/nova-tools/pkg/bus"
	"github.com/mas-bandwidth/nova-tools/pkg/codexdesktop"
	"github.com/mas-bandwidth/nova-tools/pkg/tool"
	"github.com/redis/go-redis/v9"
)

const maxCodexEvent = 64 << 10

type codexRoute struct{ socket, thread, directory string }

// codexConfig refuses partial routes before any hook record or lease is written.
func (w world) codexConfig(c *tool.Call) (*codexRoute, error) {
	r := &codexRoute{c.Str("codex-socket"), c.Str("codex-thread"), c.Str("codex-events-dir")}
	if r.socket == "" && r.thread == "" && r.directory == "" {
		return nil, nil
	}
	if r.socket == "" || r.thread == "" || r.directory == "" {
		return nil, errors.New("codex delivery wants --codex-socket, --codex-thread and --codex-events-dir together")
	}
	if c.Bool("once") {
		return nil, errors.New("codex delivery wants a running hook client; omit --once")
	}
	if w.codexLock == nil || w.codexValidate == nil || w.codexSave == nil || w.codexDeliver == nil {
		return nil, errors.New("codex delivery wants a configured local native receiver")
	}
	if c.DryRun() {
		if _, err := codexdesktop.SavedPath(r.directory, "route"); err != nil {
			return nil, err
		}
		return r, nil
	}
	if err := w.codexValidate(r.socket, r.thread, r.directory); err != nil {
		return nil, err
	}
	return r, nil
}

// savedCodexEvent is a bounded external-data envelope, not liveness state.
// Bootstrap has no bus message; subsequent server challenges retain their bus ID.
type savedCodexEvent struct {
	LocalReceiver struct {
		Identity    string `json:"identity"`
		Redis       string `json:"redis"`
		Proof       string `json:"proof"`
		Acknowledge string `json:"acknowledge"`
	} `json:"local_receiver"`
	SavedAt            time.Time     `json:"saved_at"`
	HookEpoch          int64         `json:"hook_epoch"`
	HookRole           hook.Role     `json:"hook_role"`
	RecoveryCoverage   string        `json:"recovery_coverage,omitempty"`
	Recovery           []bus.Message `json:"recovery_messages,omitempty"`
	PossibleDuplicates bool          `json:"possible_duplicates,omitempty"`
	EventID            string        `json:"event_id"`
	BootstrapNonce     string        `json:"bootstrap_nonce,omitempty"`
	Message            *bus.Message  `json:"external_message,omitempty"`
}

func (w world) codexPush(c *tool.Call, s *session, r *codexRoute, role hook.Role, epoch int64, id, nonce string, m *bus.Message) *tool.Out {
	var reconcileErr error
	if m != nil {
		answered, err := codexAnswered(s, id)
		if err != nil {
			reconcileErr = fmt.Errorf("read model answer: %w", err)
		}
		if answered {
			line(c, tool.Done().As("PUSH").Fact("push", "codex").Fact("id", id).Fact("acknowledgment", "model-answer").Note("existing bus --re answer reconciles this event; no native mutation attempted"))
			return nil
		}
	}
	if reconcileErr != nil {
		return codexDeliveryFailure(s, role, epoch, id, reconcileErr)
	}
	e := savedCodexEvent{EventID: id, BootstrapNonce: nonce, Message: m, HookEpoch: epoch, HookRole: role}
	return w.codexSend(c, s, r, role, epoch, e)
}

func (w world) codexSend(c *tool.Call, s *session, r *codexRoute, role hook.Role, epoch int64, e savedCodexEvent) *tool.Out {
	id, m := e.EventID, e.Message
	ctx, clockCancel := call()
	savedAt, clockErr := s.hooks.C.Time(ctx).Result()
	clockCancel()
	if clockErr != nil {
		return codexDeliveryFailure(s, role, epoch, id, clockErr)
	}
	e.SavedAt = savedAt

	e.LocalReceiver.Identity = s.as
	e.LocalReceiver.Redis = s.addr
	e.LocalReceiver.Proof = "For a current hook challenge only, run nova-hook prove --as <identity> --redis <redis> --nonce <nonce>. Only the model may prove."
	e.LocalReceiver.Acknowledge = "For an ordinary bus message, send a model answer with nova-bus send --as <identity> --redis <redis> --to <sender> --re <message ID> --subject ACK --body <answer>. A native accepted turn is not this answer. Read recovery_messages in order; they may duplicate earlier delivery. If recovery_coverage is nonempty, notify the coordinator once with its oldest retained entry ID."
	var data []byte
	var err error
	if m != nil && codexMessageBytes(*m) > maxCodexEvent {
		err = fmt.Errorf("external event wants at most %d bytes", maxCodexEvent)
	} else {
		data, err = json.Marshal(e)
	}
	if err == nil && len(data) > maxCodexEvent {
		err = fmt.Errorf("external event wants at most %d bytes", maxCodexEvent)
	}
	if err == nil {
		err = w.codexSave(r.directory, id, data)
	}
	turn := ""
	if err == nil {
		ctx, cancel := context.WithCancel(context.Background())
		turn, err = w.codexDeliver(ctx, r.socket, r.thread, id, r.directory)
		cancel()
	}
	if errors.Is(err, codexdesktop.ErrOutcomeUnknown) && m != nil {
		answered, answerErr := codexAnswered(s, id)
		if answered {
			line(c, tool.Done().As("PUSH").Fact("push", "codex").Fact("id", id).Fact("native", "unknown").Fact("acknowledgment", "model-answer").Note("native outcome remains unknown; existing bus --re answer reconciles the event without retry"))
			return nil
		}
		if answerErr != nil {
			err = errors.Join(err, fmt.Errorf("model answer reconciliation failed: %w", answerErr))
		}
	}
	if err == nil {
		line(c, tool.Done().As("PUSH").Fact("push", "codex").Fact("id", id).Fact("native", "accepted").Fact("turn", turn).Note("native acceptance only; awaiting model proof or bus answer; recovery_messages may repeat prior delivery"))
		return nil
	}
	return codexDeliveryFailure(s, role, epoch, id, err)
}

func codexDeliveryFailure(s *session, role hook.Role, epoch int64, id string, err error) *tool.Out {
	// Remove route proof, not identity or epoch: an unknown native outcome says
	// nothing about whether the model is present. Stop renewal and never retry.
	ctx, cancel := call()
	dropErr := s.hooks.Unprove(ctx, s.as, role, hook.Bus, epoch)
	cancel()
	outcome := "failed"
	if errors.Is(err, codexdesktop.ErrOutcomeUnknown) {
		outcome = "unknown"
	}
	o := tool.Fail("Codex local delivery "+outcome+": "+err.Error()).Fact("id", id).Fact("native", outcome)
	o.Remedy = "inspect the saved event and model bus answer before restarting the native route; never blindly retry an accepted or unknown delivery"
	if dropErr != nil {
		o.Note("the route proof could not be revoked: " + dropErr.Error() + "; this client stops renewing its lease")
	}
	return o
}

func validateCodexRoute(socket, thread, directory string) error {
	if err := codexdesktop.ValidateRoute(socket, thread, directory); err != nil {
		return err
	}
	if err := privateCodexDirectory(directory); err != nil {
		return err
	}
	return codexdesktop.Probe(context.Background(), socket, thread)
}

func privateCodexDirectory(directory string) error {
	if err := codexdesktop.ValidateDirectory(directory); err != nil {
		return err
	}
	if err := codexdesktop.ValidatePrivateDirectory(directory); err != nil {
		return err
	}
	real, err := filepath.EvalSymlinks(directory)
	if err != nil || real != directory {
		return errors.New("event directory wants a canonical path without symlink ancestors")
	}
	return nil
}

func saveCodexEvent(directory, id string, data []byte) error {
	if len(data) > maxCodexEvent {
		return errors.New("saved event exceeds 64 KiB")
	}
	if err := privateCodexDirectory(directory); err != nil {
		return err
	}
	path, err := codexdesktop.SavedPath(directory, id)
	if err != nil {
		return err
	}
	// Refuse replacement, including a saved accepted/unknown event after restart.
	return atomicfile.Write(path, data, 0600, atomicfile.NoReplace())
}

// codexAnswered reads one existing bus-owned answer field. It never scans the
// history hash, writes a receipt, or infers model proof from native acceptance.
func codexAnswered(s *session, id string) (bool, error) {
	ctx, cancel := call()
	defer cancel()
	value, err := s.hooks.C.HGet(ctx, bus.AnswersOf(s.as), id).Result()
	if errors.Is(err, redis.Nil) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	stamp, err := strconv.ParseInt(value, 10, 64)
	if err != nil || stamp <= 0 {
		return false, errors.New("bus answer wants a valid store timestamp")
	}
	return true, nil
}

// Reject oversized external fields before encoding can allocate another copy.
// The final encoded envelope is checked independently for JSON escaping overhead.
func codexMessageBytes(m bus.Message) int {
	total := len(m.ID) + len(m.From) + len(m.Subject) + len(m.Re) + len(m.Kind) + len(m.Body) + len(m.Token) + len(m.Once)
	for _, name := range m.To {
		total += len(name)
		if total > maxCodexEvent {
			return total
		}
	}
	for _, name := range m.CC {
		total += len(name)
		if total > maxCodexEvent {
			return total
		}
	}
	for _, mark := range m.Marks {
		total += len(mark.Key) + len(mark.Field) + len(mark.Value)
		if total > maxCodexEvent {
			return total
		}
	}
	return total
}
