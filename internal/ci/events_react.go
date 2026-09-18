package ci

// events_react.go is the reactor half of "Events, not ticks": nova-merge react. It
// subscribes to the three channels and acts on each:
//
//   pr-checks-done success, not skipped and not held -> enqueue the PR, once
//   dev-moved                                        -> rebase-wanted per newly DIRTY PR
//   card-done                                        -> nothing (the recorder and the
//                                                       harvester read the stream directly)
//
// The reactor holds no timer. It blocks on the subscription until its --deadline, so an
// idle reactor costs no cycles and wakes exactly when a message lands. The skip set is
// durable; the hold key expires on its own.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/redis/go-redis/v9"

	novalog "github.com/mas-bandwidth/nova-tools/internal/log"
)

// Reactor is the stateful subscriber. sent remembers the rebase-wanted it already
// published per head, so a republished dev-moved does not cut the same rebase twice.
//
// Events is the structured sink of SPEC-LOGS.md Part 2 (internal/log). It is the REACTOR's
// half of the stream: one line per message it reacted to, whose KIND IS THE CHANNEL NAME
// the message arrived on, so the vocabulary a LogQL query selects on is the vocabulary the
// bus carries, and whose message says what the reactor did about it -- enqueue, skip, hold,
// rebase-wanted, or nothing at all. A nil Events writes nothing, so every caller and every
// test that predates this keeps its exact output.
type Reactor struct {
	RDB     *redis.Client
	Forge   Forge
	Enqueue func(ctx context.Context, pr int, head string) error
	Log     io.Writer
	Events  *novalog.Emitter

	sent map[int]string
}

// NewReactor returns a reactor. A nil Enqueue lands PRs in the merge:queue set, which is
// the exactly-once shape an approval needs.
//
// THAT SET IS NOT A MERGE QUEUE. It is this session's own list of pull requests whose checks
// came back green, read by whoever builds the next batch; nothing in it reaches a forge.
// Admission to the forge's merge queue is internal/merge.Enqueuer.Enqueue and its one
// caller, `nova-merge land`, which takes a batch and nothing else (Glenn, 2026-09-18). A
// caller that injected an Enqueue reaching a forge directly would be a second entrance, and
// internal/ci's class rule refuses the spellings that used to build one.
func NewReactor(rdb *redis.Client, forge Forge, enqueue func(context.Context, int, string) error, log io.Writer) *Reactor {
	r := &Reactor{RDB: rdb, Forge: forge, Enqueue: enqueue, Log: log, sent: map[int]string{}}
	if r.Enqueue == nil {
		r.Enqueue = func(ctx context.Context, pr int, _ string) error {
			return r.RDB.SAdd(ctx, QueueMerge, strconv.Itoa(pr)).Err()
		}
	}
	return r
}

// Handle acts on one message. The channel names the action; an unknown channel is
// ignored, because a subscriber that refused a message it does not know would die on the
// first channel another card adds.
func (r *Reactor) Handle(ctx context.Context, channel, payload string) error {
	switch channel {
	case ChannelCardDone:
		// The recorder and the harvester consume the stream directly, as the card says;
		// republishing card-done must not make the reactor do anything at all. It is
		// still emitted: "the reactor saw this and did nothing on purpose" is an answer,
		// and a channel with no line at all reads as a reactor that was not listening.
		r.reacted(channel, 0, "action=none reason=the recorder reads the stream directly")
		return nil
	case ChannelPRChecksDone:
		return r.onChecksDone(ctx, payload)
	case ChannelDevMoved:
		return r.onDevMoved(ctx, payload)
	}
	return nil
}

// onChecksDone enqueues a green PR unless a skip or a hold stops it.
func (r *Reactor) onChecksDone(ctx context.Context, payload string) error {
	var e PRChecksDone
	if err := json.Unmarshal([]byte(payload), &e); err != nil {
		return fmt.Errorf("%s payload is not JSON: %w", ChannelPRChecksDone, err)
	}
	if !strings.EqualFold(e.Conclusion, ConclusionSuccess) {
		return nil
	}
	skip, err := r.RDB.SIsMember(ctx, SetEnqueueSkip, strconv.Itoa(e.Number)).Result()
	if err != nil {
		return fmt.Errorf("read %s: %w", SetEnqueueSkip, err)
	}
	if skip {
		r.line("REACT skip pr=%d head=%s reason=skip-set\n", e.Number, e.Head)
		r.reacted(ChannelPRChecksDone, e.Number, "action=skip reason=skip-set head="+e.Head)
		return nil
	}
	held, err := r.RDB.Exists(ctx, KeyEnqueueHold).Result()
	if err != nil {
		return fmt.Errorf("read %s: %w", KeyEnqueueHold, err)
	}
	if held > 0 {
		r.line("REACT hold pr=%d head=%s reason=hold-key\n", e.Number, e.Head)
		r.reacted(ChannelPRChecksDone, e.Number, "action=hold reason=hold-key head="+e.Head)
		return nil
	}
	if err := r.Enqueue(ctx, e.Number, e.Head); err != nil {
		return fmt.Errorf("enqueue %d: %w", e.Number, err)
	}
	r.line("REACT enqueue pr=%d head=%s\n", e.Number, e.Head)
	r.reacted(ChannelPRChecksDone, e.Number, "action=enqueue head="+e.Head)
	return nil
}

// onDevMoved asks for a rebase unit for every PR the move made DIRTY, once per head. A
// clean PR is left alone: the base moved and it still applies.
func (r *Reactor) onDevMoved(ctx context.Context, payload string) error {
	var e DevMoved
	if err := json.Unmarshal([]byte(payload), &e); err != nil {
		return fmt.Errorf("%s payload is not JSON: %w", ChannelDevMoved, err)
	}
	snap, err := r.Forge.Snapshot()
	if err != nil {
		return fmt.Errorf("poll the forge after %s: %w", ChannelDevMoved, err)
	}
	for _, pr := range snap.PRs {
		if !strings.EqualFold(pr.Mergeable, "DIRTY") {
			continue
		}
		if r.sent[pr.Number] == pr.Head {
			continue
		}
		r.sent[pr.Number] = pr.Head
		if err := publish(ctx, r.RDB, ChannelRebaseWanted, RebaseWanted{Number: pr.Number, Head: pr.Head}); err != nil {
			return fmt.Errorf("publish %s: %w", ChannelRebaseWanted, err)
		}
		r.line("REACT rebase-wanted pr=%d head=%s base=%s\n", pr.Number, pr.Head, e.SHA)
		r.reacted(ChannelDevMoved, pr.Number, "action=rebase-wanted head="+pr.Head+" base="+e.SHA)
	}
	return nil
}

// Run subscribes and handles messages until the context's deadline. The subscription is
// confirmed before the loop, so a caller that publishes after Run has started cannot race
// the subscribe.
func (r *Reactor) Run(ctx context.Context) error {
	sub := r.RDB.Subscribe(ctx, ChannelCardDone, ChannelPRChecksDone, ChannelDevMoved)
	defer sub.Close()
	if _, err := sub.Receive(ctx); err != nil {
		return fmt.Errorf("subscribe: %w", err)
	}
	ch := sub.Channel()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case msg, ok := <-ch:
			if !ok {
				return nil
			}
			if err := r.Handle(ctx, msg.Channel, msg.Payload); err != nil {
				return err
			}
		}
	}
}

// RunOnce handles exactly one message and returns. RunOnce returns when the message is
// handled, so a --once invocation is one action and not one tick.
func (r *Reactor) RunOnce(ctx context.Context) error {
	sub := r.RDB.Subscribe(ctx, ChannelCardDone, ChannelPRChecksDone, ChannelDevMoved)
	defer sub.Close()
	if _, err := sub.Receive(ctx); err != nil {
		return fmt.Errorf("subscribe: %w", err)
	}
	msg, err := sub.ReceiveMessage(ctx)
	if err != nil {
		return err
	}
	return r.Handle(ctx, msg.Channel, msg.Payload)
}

// reacted is the structured half of every action above: one line whose kind is the channel
// the message arrived on and whose message says what was done about it. A nil Events
// emitter writes nothing (internal/log), so this costs a nil check on a quiet reactor.
func (r *Reactor) reacted(channel string, pr int, msg string) {
	if r.Events == nil {
		return
	}
	l := r.Events.Line(channel)
	l.PR = pr
	l.Msg = msg
	r.Events.Send(l)
}

// line writes one action line. A nil log discards it.
func (r *Reactor) line(format string, args ...interface{}) {
	if r.Log == nil {
		return
	}
	fmt.Fprintf(r.Log, format, args...)
}
