package ci

// events_producer.go is the producer half of "Events, not ticks": nova-work events. It
// does two things and neither is a model call.
//
//  1. It reads the cards:done stream with the events consumer group and republishes each
//     entry as card-done. A restart resumes at the group's cursor, so a card finished
//     while the bridge was down is not lost and one already published is not replayed to
//     a fresh consumer.
//  2. It polls gh on --gh-poll while webhooks are not wired here. The poll is the fallback
//     heartbeat the principle allows: it publishes pr-checks-done only when a suite's
//     conclusion changed, and dev-moved only when the base head moved. A quiet poll says
//     nothing, which is the whole point.
//
// The first poll is a change from nothing, so it publishes the terminal conclusions and
// the base head it finds. A restart therefore re-announces the world once; the reactor's
// skip set and the queue set make a repeated event a no-op, which is cheaper than a lost
// one.

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// Producer reads the stream and polls the forge. Its maps are the change detector and
// live only in this process: the durable cursor is the consumer group, not a file.
type Producer struct {
	RDB      *redis.Client
	Forge    Forge
	Consumer string
	Log      io.Writer
	Clock    func() time.Time

	conclusions map[int]string
	base        string
	baseSeen    bool
	groupReady  bool
}

// NewProducer returns a producer with the given identity. A nil log discards the lines.
func NewProducer(rdb *redis.Client, forge Forge, consumer string, log io.Writer) *Producer {
	if consumer == "" {
		consumer = "nova-work-events"
	}
	return &Producer{RDB: rdb, Forge: forge, Consumer: consumer, Log: log,
		conclusions: map[int]string{}}
}

// PublishCardsDone reads the unclaimed entries of cards:done once and republishes each as
// card-done, acking it after the publish. It returns how many it published.
func (p *Producer) PublishCardsDone(ctx context.Context) (int, error) {
	if err := p.ensureGroup(ctx); err != nil {
		return 0, err
	}
	streams, err := p.RDB.XReadGroup(ctx, &redis.XReadGroupArgs{
		Group:    GroupEvents,
		Consumer: p.Consumer,
		Streams:  []string{StreamCardsDone, ">"},
		Count:    100,
		// -1 omits BLOCK: the read is one non-blocking pass and the caller decides
		// the cadence. The zero value would block forever, which is the tick this
		// card exists to delete.
		Block: -1,
	}).Result()
	if err == redis.Nil {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("read %s as group %s: %w", StreamCardsDone, GroupEvents, err)
	}
	published := 0
	for _, stream := range streams {
		for _, msg := range stream.Messages {
			card := valueString(msg.Values["card"])
			label := valueString(msg.Values["label"])
			if card == "" {
				card = msg.ID
			}
			if err := publish(ctx, p.RDB, ChannelCardDone, CardDone{Card: card, Label: label}); err != nil {
				return published, fmt.Errorf("publish %s: %w", ChannelCardDone, err)
			}
			if err := p.RDB.XAck(ctx, StreamCardsDone, GroupEvents, msg.ID).Err(); err != nil {
				return published, fmt.Errorf("ack %s: %w", msg.ID, err)
			}
			published++
		}
	}
	return published, nil
}

// ensureGroup creates the consumer group at the stream's end, once, tolerating the
// BUSYGROUP a second process or a restart would raise.
func (p *Producer) ensureGroup(ctx context.Context) error {
	if p.groupReady {
		return nil
	}
	if err := p.RDB.XGroupCreateMkStream(ctx, StreamCardsDone, GroupEvents, "$").Err(); err != nil {
		if !isBusyGroup(err) {
			return fmt.Errorf("create group %s on %s: %w", GroupEvents, StreamCardsDone, err)
		}
	}
	p.groupReady = true
	return nil
}

// isBusyGroup is the one error XGROUP CREATE raises when the group already exists.
func isBusyGroup(err error) bool {
	return err != nil && strings.Contains(err.Error(), "BUSYGROUP")
}

// PollOnce reads the forge once and publishes what changed. It returns how many messages
// it published, so --once can say so and a test can assert zero for a quiet poll.
func (p *Producer) PollOnce(ctx context.Context) (int, error) {
	if p.Forge == nil {
		return 0, nil
	}
	snap, err := p.Forge.Snapshot()
	if err != nil {
		return 0, fmt.Errorf("poll the forge: %w", err)
	}
	published := 0
	for _, pr := range snap.PRs {
		if pr.Conclusion != ConclusionSuccess && pr.Conclusion != ConclusionFailure {
			continue
		}
		if old, ok := p.conclusions[pr.Number]; ok && old == pr.Conclusion {
			continue
		}
		p.conclusions[pr.Number] = pr.Conclusion
		if err := publish(ctx, p.RDB, ChannelPRChecksDone, PRChecksDone{
			Number: pr.Number, Head: pr.Head, Conclusion: pr.Conclusion,
		}); err != nil {
			return published, fmt.Errorf("publish %s: %w", ChannelPRChecksDone, err)
		}
		published++
	}
	if snap.Base != "" && (!p.baseSeen || snap.Base != p.base) {
		p.base, p.baseSeen = snap.Base, true
		if err := publish(ctx, p.RDB, ChannelDevMoved, DevMoved{SHA: snap.Base}); err != nil {
			return published, fmt.Errorf("publish %s: %w", ChannelDevMoved, err)
		}
		published++
	}
	return published, nil
}

// Run is the loop: read the stream every second and poll the forge every ghPoll, until
// the context's deadline. ghPoll is the fallback heartbeat and is never zero.
func (p *Producer) Run(ctx context.Context, ghPoll time.Duration) error {
	if ghPoll <= 0 {
		ghPoll = time.Minute
	}
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	lastPoll := time.Time{}
	for {
		if _, err := p.PublishCardsDone(ctx); err != nil {
			return err
		}
		if p.Clock == nil {
			p.Clock = time.Now
		}
		if now := p.Clock(); lastPoll.IsZero() || now.Sub(lastPoll) >= ghPoll {
			lastPoll = now
			if _, err := p.PollOnce(ctx); err != nil {
				return err
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-tick.C:
		}
	}
}

// valueString reads one stream value, which go-redis hands back as interface{}.
func valueString(v interface{}) string {
	switch x := v.(type) {
	case string:
		return x
	case []byte:
		return string(x)
	case nil:
		return ""
	default:
		return fmt.Sprint(x)
	}
}
