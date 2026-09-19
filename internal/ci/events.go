package ci

// events.go is the event vocabulary of docs/SPEC-JOBS.md "Events, not ticks". A finished
// card, a completed check suite and a moved base are EVENTS: nova-work events publishes
// them on Redis pub/sub, and nova-merge react subscribes and acts. Nothing here polls on
// the merge path; the one poll left, --gh-poll, is the fallback heartbeat the principle
// allows until the forge can push a webhook at us.
//
// Every payload is JSON of one struct below. The channels and keys are names a second
// process reads, so they are constants rather than string literals scattered through the
// loops.

import (
	"context"
	"encoding/json"

	"github.com/redis/go-redis/v9"
)

// The pub/sub channels. card-done is a republish of the cards:done stream; the other two
// are the gh fallback's heartbeat and the rebase request the rebase verb consumes.
const (
	ChannelCardDone     = "card-done"
	ChannelPRChecksDone = "pr-checks-done"
	ChannelDevMoved     = "dev-moved"
	ChannelRebaseWanted = "rebase-wanted"
)

// The stream, group and keys the bridge shares with the rest of the family. The stream is
// the record of finished cards; the group is this bridge's cursor, so a restart resumes
// where it stopped instead of replaying the stream.
const (
	StreamCardsDone = "cards:done"
	GroupEvents     = "events"
)

// THREE NAMES USED TO LIVE HERE AND THEY ARE GONE (edges 17 and 18, 2026-09-18).
//
//	merge:queue    the set react enqueued into. NOTHING IN THE TREE EVER READ IT: the
//	               merge layer's queue is <lane>/queue.json, which `nova-merge queue`
//	               writes and `nova-merge run` walks, so `REACT enqueue pr=N` at exit 0
//	               enqueued into a hole. The door is the caller's now, and nova-merge
//	               react hands the reactor one that writes that file.
//	enqueue:skip   a SECOND skip set, beside queue.json's `skipped`.
//	enqueue:hold   a SECOND hold, beside <lane>/hold.
//
// Two hold mechanisms and two skip sets wearing the same words are one mechanism nobody
// can reason about: with `<lane>/hold` standing and the pull request in the lane's skip
// list, react still printed `REACT enqueue`, and only the redis key stopped it. ONE
// QUEUE, ONE HOLD, ONE SKIP SET: the reactor reads the lane's, through Gate.

// The check-suite conclusions this vocabulary distinguishes. PENDING and every
// not-yet-complete state are not events and are never published.
const (
	ConclusionSuccess = "SUCCESS"
	ConclusionFailure = "FAILURE"
)

// CardDone is one finished card, read off the cards:done stream.
type CardDone struct {
	Card  string `json:"card"`
	Label string `json:"label,omitempty"`
}

// PRChecksDone is one completed check suite on an open rowan/* pull request.
type PRChecksDone struct {
	Number     int    `json:"number"`
	Head       string `json:"head"`
	Conclusion string `json:"conclusion"`
}

// DevMoved is the base branch's head changing to a new sha.
type DevMoved struct {
	SHA string `json:"sha"`
}

// RebaseWanted asks the rebase verb to cut a unit for a PR a merge made DIRTY.
type RebaseWanted struct {
	Number int    `json:"number"`
	Head   string `json:"head"`
}

// PRState is one open pull request as the forge sees it, folded to the three facts the
// bridge needs: where its head is, what its checks concluded, and whether a move of the
// base left it DIRTY. It is deliberately smaller than merge.PR: this is the event edge,
// not the merge condition.
type PRState struct {
	Number     int
	Branch     string
	Head       string
	Conclusion string // SUCCESS, FAILURE, PENDING or ""
	Mergeable  string // CLEAN, DIRTY, UNKNOWN or ""
}

// Snapshot is the forge's answer to one poll: the open rowan/* pull requests and the
// base branch's current head.
type Snapshot struct {
	PRs  []PRState
	Base string
}

// Forge is the edge between the bridge and the host. It is an interface for the reason
// merge.Host is: the tests need an edge that cannot reach the network, and the one
// implementation that shells to gh is then a thing a reader can check line by line.
type Forge interface {
	Snapshot() (Snapshot, error)
}

// publish marshals one payload and puts it on a channel. A marshal of these structs
// cannot fail, but the publish can, and its error is returned rather than dropped: a
// producer that cannot say an event happened must say so.
func publish(ctx context.Context, rdb *redis.Client, channel string, payload interface{}) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return rdb.Publish(ctx, channel, string(body)).Err()
}
