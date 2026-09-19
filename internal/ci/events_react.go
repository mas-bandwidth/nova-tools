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
// idle reactor costs no cycles and wakes exactly when a message lands.
//
// EDGE 17 AND EDGE 18, 2026-09-18. The reactor used to own two things it had no business
// owning: a DOOR OF ITS OWN -- a nil Enqueue installed `SAdd merge:queue`, a redis set
// NOTHING IN THE TREE EVER READ, so `REACT enqueue pr=N` at exit 0 enqueued into a hole --
// and a SECOND HOLD AND A SECOND SKIP SET, `enqueue:hold` and `enqueue:skip` in redis,
// which wore the same words as the lane's `<lane>/hold` and `queue.json`'s `skipped` and
// obeyed neither: a lane held and a PR skipped, and react still printed `REACT enqueue`.
//
// Both are gone. The door is now the caller's -- nova-merge react hands one that writes
// the lane's queue.json, the same file `nova-merge queue` writes, `nova-merge queue status`
// prints and `nova-merge run` walks -- and the hold and the skip set are one Gate, which
// nova-merge react implements over the lane's own two files. ONE QUEUE, ONE HOLD, ONE SKIP
// SET -- and, one level up, ONE ENTRANCE TO THE FORGE'S merge queue, which is
// internal/merge.Enqueuer and `nova-merge land` (#1347) and is not this file.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// ErrBadPayload is what a message this reactor could not read is wrapped in.
//
// EDGE 20: one malformed payload killed the whole reactor -- `REACT FAIL`, exit 1 --
// although an UNKNOWN CHANNEL is deliberately ignored, so a subscriber that survives a
// channel it has never heard of died on one bad byte from a channel it knows. A message
// that is not the JSON its channel promises is one message dropped, said out loud and
// counted on the closing line; everything else -- a bus that cannot be read, a forge that
// will not answer, a queue that cannot be written -- is still a failure, because those
// are the reactor's own edges rather than one publisher's typo.
var ErrBadPayload = errors.New("the payload is not the JSON this channel carries")

// Gate is THE ONE HOLD AND THE ONE SKIP SET the reactor obeys. nova-merge react
// implements it over the lane's `<lane>/hold` and `<lane>/queue.json`, which is exactly
// what `nova-merge queue hold` writes and `nova-merge queue skip` writes, so the two
// verbs and the reactor can never disagree about whether a pull request may be enqueued.
//
// A nil Gate is an OPEN gate and is only ever nil in a test that is about something else.
type Gate interface {
	// Skipped reports whether this pull request is one the queue must leave alone --
	// skipped or parked.
	Skipped(pr int) (bool, error)
	// Held reports whether a hold stands over the whole queue, and its reason.
	Held() (held bool, reason string, err error)
}

// Reactor is the stateful subscriber. sent remembers the rebase-wanted it already
// published per head, so a republished dev-moved does not cut the same rebase twice.
type Reactor struct {
	RDB   *redis.Client
	Forge Forge
	// Gate is the hold and the skip set; see Gate.
	Gate Gate
	// Enqueue is the ONE DOOR. It is the caller's, because the caller is the one that
	// knows where the queue is: nova-merge react writes the lane's queue.json.
	Enqueue func(ctx context.Context, pr int, head string) error
	Log     io.Writer

	// Dropped counts the messages this reactor could not read and carried on past.
	Dropped int

	sent map[int]string
}

// NewReactor returns a reactor over one bus, one forge, one gate and one door.
//
// THE DOOR IS NOT A MERGE QUEUE, AND THERE IS EXACTLY ONE OF EACH. What react enqueues is
// the session's list of pull requests whose checks came back green, read by whoever builds
// the next batch -- and that list is the LANE'S `queue.json`, the file `nova-merge queue`
// writes, `nova-merge queue status` prints and `nova-merge run` walks. It used to be the
// redis set `merge:queue`, which nothing in this tree ever read (edge 17): react reported
// `REACT enqueue pr=N` at exit 0 into a hole, and the batch builder it was supposedly for
// had no way to see it. Admission to the FORGE's merge queue is a different door
// altogether -- internal/merge.Enqueuer.Enqueue and its one caller `nova-merge land`, which
// takes a batch and nothing else (Glenn, 2026-09-18) -- and this reactor never reaches it.
// A caller that injected an Enqueue reaching a forge directly would be a second entrance,
// and internal/ci's class rule refuses the spellings that used to build one.
//
// A nil enqueue is therefore NOT a default door any more: it is a reactor with nowhere to
// put a pull request, and it says so on the first green one rather than reporting an
// enqueue about a set nobody reads.
func NewReactor(rdb *redis.Client, forge Forge, gate Gate, enqueue func(context.Context, int, string) error, log io.Writer) *Reactor {
	r := &Reactor{RDB: rdb, Forge: forge, Gate: gate, Enqueue: enqueue, Log: log, sent: map[int]string{}}
	if r.Enqueue == nil {
		r.Enqueue = func(context.Context, int, string) error {
			return errors.New("this reactor was built with no queue to enqueue into; nova-merge react --lane <dir> is the door")
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
		// republishing card-done must not make the reactor do anything at all.
		return nil
	case ChannelPRChecksDone:
		return r.onChecksDone(ctx, payload)
	case ChannelDevMoved:
		return r.onDevMoved(ctx, payload)
	}
	return nil
}

// onChecksDone enqueues a green PR unless the queue's skip set or the lane's hold stops it.
func (r *Reactor) onChecksDone(ctx context.Context, payload string) error {
	var e PRChecksDone
	if err := json.Unmarshal([]byte(payload), &e); err != nil {
		return fmt.Errorf("%s: %w: %s", ChannelPRChecksDone, ErrBadPayload, err)
	}
	if !strings.EqualFold(e.Conclusion, ConclusionSuccess) {
		return nil
	}
	if r.Gate != nil {
		skip, err := r.Gate.Skipped(e.Number)
		if err != nil {
			return fmt.Errorf("read the queue's skip set: %w", err)
		}
		if skip {
			r.line("REACT skip pr=%d head=%s reason=queue-skip\n", e.Number, oneline.Field(e.Head))
			return nil
		}
		held, reason, err := r.Gate.Held()
		if err != nil {
			return fmt.Errorf("read the lane's hold: %w", err)
		}
		if held {
			r.line("REACT hold pr=%d head=%s reason=%s\n", e.Number, oneline.Field(e.Head), oneline.Field(reason))
			return nil
		}
		if prGate, ok := r.Gate.(interface {
			PRHeld(int, string) (bool, string, error)
		}); ok {
			held, reason, err := prGate.PRHeld(e.Number, e.Head)
			if err != nil {
				return fmt.Errorf("read the pr's hold: %w", err)
			}
			if held {
				r.line("REACT hold pr=%d head=%s reason=%s\n", e.Number, oneline.Field(e.Head), oneline.Field(reason))
				return nil
			}
		}
	}
	if err := r.Enqueue(ctx, e.Number, e.Head); err != nil {
		return fmt.Errorf("enqueue %d: %w", e.Number, err)
	}
	r.line("REACT enqueue pr=%d head=%s\n", e.Number, oneline.Field(e.Head))
	return nil
}

// onDevMoved asks for a rebase unit for every PR the move made DIRTY, once per head. A
// clean PR is left alone: the base moved and it still applies.
func (r *Reactor) onDevMoved(ctx context.Context, payload string) error {
	var e DevMoved
	if err := json.Unmarshal([]byte(payload), &e); err != nil {
		return fmt.Errorf("%s: %w: %s", ChannelDevMoved, ErrBadPayload, err)
	}
	if r.Forge == nil {
		return errors.New("this reactor has no forge, so a base move cannot be turned into a rebase list; nova-merge react --lane <dir>")
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
		r.line("REACT rebase-wanted pr=%d head=%s base=%s\n", pr.Number, oneline.Field(pr.Head), oneline.Field(e.SHA))
	}
	return nil
}

// act handles one message and absorbs the one class of failure that is a publisher's and
// not this reactor's: a payload that is not the JSON its channel carries (edge 20).
func (r *Reactor) act(ctx context.Context, channel, payload string) error {
	err := r.Handle(ctx, channel, payload)
	if err == nil || !errors.Is(err, ErrBadPayload) {
		return err
	}
	r.Dropped++
	r.line("REACT DROP channel=%s reason=%s\n", oneline.Field(channel),
		oneline.Field(oneline.Cap(err.Error(), oneline.TailBytes)))
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
			if err := r.act(ctx, msg.Channel, msg.Payload); err != nil {
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
	return r.act(ctx, msg.Channel, msg.Payload)
}

// line writes one action line. A nil log discards it.
func (r *Reactor) line(format string, args ...interface{}) {
	if r.Log == nil {
		return
	}
	fmt.Fprintf(r.Log, format, args...)
}
