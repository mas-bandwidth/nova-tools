package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/hook"
	"github.com/mas-bandwidth/nova-tools/pkg/atomicfile"
	"github.com/mas-bandwidth/nova-tools/pkg/bus"
	"github.com/mas-bandwidth/nova-tools/pkg/codexdesktop"
	"github.com/mas-bandwidth/nova-tools/pkg/tool"
	"github.com/redis/go-redis/v9"
)

const maxCodexEvent = 64 << 10

type codexRoute struct {
	socket, thread, directory string
	since                     time.Time

	pending           bool
	pendingID         string
	pendingTurn       string
	pendingAnsweredAt string
	pendingProof      string
	pendingAnswersLen int64
	pendingOwedLen    int64
	pendingGroupLast  string
	foldedCount       int
	foldedSenders     []string
	foldedChallenge   *bus.Message
}

func (r *codexRoute) fold(m bus.Message) {
	r.foldedCount++
	from := m.From
	if from == "" {
		from = "unknown"
	}
	if !slices.Contains(r.foldedSenders, from) {
		r.foldedSenders = append(r.foldedSenders, from)
	}
	if strings.HasPrefix(m.Subject, "HOOK CHALLENGE nonce=") {
		cp := m
		r.foldedChallenge = &cp
	}
}

func (r *codexRoute) summary() string {
	return fmt.Sprintf("%d more messages from %s; read the bus", r.foldedCount, strings.Join(r.foldedSenders, ", "))
}

func (r *codexRoute) startTurn(s *session, id, turn string) {
	if r == nil {
		return
	}
	r.pending = true
	r.pendingID = id
	r.pendingTurn = turn
	if s == nil || s.hooks.C == nil {
		return
	}
	ctx, cancel := call()
	defer cancel()
	r.pendingAnsweredAt, _ = s.hooks.C.HGet(ctx, hook.RecordKey(s.as), "answered_at").Result()
	r.pendingProof, _ = s.hooks.C.Get(ctx, hook.ProofKey(s.as, hook.Bus)).Result()
	r.pendingAnswersLen, _ = s.hooks.C.HLen(ctx, bus.AnswersOf(s.as)).Result()
	r.pendingOwedLen, _ = s.hooks.C.HLen(ctx, bus.OwedOf(s.as)).Result()
	if s.bus != nil {
		r.pendingGroupLast, _, _ = s.bus.Group(ctx, bus.StreamOf(s.as), s.as)
	}
}

func (w world) codexTurnBoundary(s *session, r *codexRoute) bool {
	if r == nil || !r.pending || s == nil || s.hooks.C == nil {
		return false
	}
	if r.pendingID != "" {
		if answered, err := codexAnswered(s, r.pendingID); err == nil && answered {
			return true
		}
	}
	ctx, cancel := call()
	defer cancel()
	if answeredAt, err := s.hooks.C.HGet(ctx, hook.RecordKey(s.as), "answered_at").Result(); err == nil && answeredAt != "" && answeredAt != r.pendingAnsweredAt {
		return true
	}
	if proof, err := s.hooks.C.Get(ctx, hook.ProofKey(s.as, hook.Bus)).Result(); err == nil && proof != "" && proof != r.pendingProof {
		return true
	}
	if n, err := s.hooks.C.HLen(ctx, bus.AnswersOf(s.as)).Result(); err == nil && n > r.pendingAnswersLen {
		return true
	}
	if n, err := s.hooks.C.HLen(ctx, bus.OwedOf(s.as)).Result(); err == nil && n < r.pendingOwedLen {
		return true
	}
	if s.bus != nil {
		if last, _, err := s.bus.Group(ctx, bus.StreamOf(s.as), s.as); err == nil && last != r.pendingGroupLast {
			return true
		}
	}
	return false
}

// codexConfig refuses partial routes before any hook record or lease is written.
func (w world) codexConfig(c *tool.Call) (*codexRoute, error) {
	r := &codexRoute{socket: c.Str("codex-socket"), thread: c.Str("codex-thread"), directory: c.Str("codex-events-dir")}
	rawSince := c.Str("codex-since")
	if rawSince != "" {
		var err error
		r.since, err = time.Parse(time.RFC3339, rawSince)
		if err != nil || rawSince != r.since.UTC().Format(time.RFC3339) {
			return nil, errors.New("codex cutover wants --codex-since as a fixed UTC timestamp, for example 2026-10-14T20:00:00Z; retain it in launch configuration across restarts")
		}
	}
	if r.socket == "" && r.thread == "" && r.directory == "" && rawSince == "" {
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
	RecoverySince      string        `json:"recovery_since,omitempty"`
	RecoveryCoverage   string        `json:"recovery_coverage,omitempty"`
	Recovery           []bus.Message `json:"recovery_messages,omitempty"`
	PossibleDuplicates bool          `json:"possible_duplicates,omitempty"`
	EventID            string        `json:"event_id"`
	BootstrapNonce     string        `json:"bootstrap_nonce,omitempty"`
	Summary            string        `json:"summary,omitempty"`
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
	if m != nil && codexMessageBytes(*m) > maxCodexEvent {
		return codexDeliveryFailure(s, role, epoch, id, fmt.Errorf("external event wants at most %d bytes", maxCodexEvent))
	}
	if r != nil && r.pending && r.foldedCount == 0 && w.codexTurnBoundary(s, r) {
		r.pending = false
		r.pendingID = ""
		r.pendingTurn = ""
	}
	if r != nil && r.pending {
		if m != nil {
			r.fold(*m)
		}
		line(c, tool.Done().As("PUSH").Fact("push", "codex").Fact("id", id).Fact("native", "coalesced").Fact("pending", r.foldedCount).Fact("summary", tool.Text(r.summary())).Note("folded into pending notice; delivered at next turn boundary"))
		return nil
	}
	e := savedCodexEvent{EventID: id, BootstrapNonce: nonce, Message: m, HookEpoch: epoch, HookRole: role}
	out := w.codexSend(c, s, r, role, epoch, e)
	if out == nil && r != nil {
		r.startTurn(s, id, r.pendingTurn)
	}
	return out
}

func (w world) codexStep(c *tool.Call, s *session, r *codexRoute, role hook.Role, epoch int64) *tool.Out {
	if r == nil || !r.pending || !w.codexTurnBoundary(s, r) {
		return nil
	}
	r.pending = false
	r.pendingID = ""
	r.pendingTurn = ""
	if r.foldedCount == 0 {
		return nil
	}
	summary := r.summary()
	id := "summary-" + w.nonce()
	from := "bus"
	if len(r.foldedSenders) > 0 {
		from = strings.Join(r.foldedSenders, ", ")
	}
	e := savedCodexEvent{
		EventID:   id,
		HookEpoch: epoch,
		HookRole:  role,
		Summary:   summary,
		Message:   &bus.Message{ID: id, From: from, Subject: summary, Body: summary},
	}
	if r.foldedChallenge != nil {
		e.Recovery = []bus.Message{*r.foldedChallenge}
	}
	r.foldedCount = 0
	r.foldedSenders = nil
	r.foldedChallenge = nil
	out := w.codexSend(c, s, r, role, epoch, e)
	if out == nil {
		r.startTurn(s, id, r.pendingTurn)
	}
	return out
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
		if r != nil {
			r.pendingTurn = turn
		}
		o := tool.Done().As("PUSH").Fact("push", "codex").Fact("id", id).Fact("native", "accepted").Fact("turn", turn)
		if e.Summary != "" {
			o.Fact("summary", tool.Text(e.Summary))
		}
		line(c, o.Note("native acceptance only; awaiting model proof or bus answer; recovery_messages may repeat prior delivery"))
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
