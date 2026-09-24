package consume

// report.go is the report-to-read rule of `nova-sprint route` (#2756
// section 11 row 5; nova-tools #3036): a card that ends DONE and commits
// nothing (no pushed sha, no branch, no PR) gets exactly one report read task
// for a non-author friend at its results ref, and never a harvest or review
// task. It reads s:<S>:log in its own consumer group, so it and ok-to-friend
// each see every ended event and split them by NoCommit; the write is one
// Redis Function call (ns_report_read in internal/nsprint/fn/lua/report.lua)
// that also records the event under its idempotency key and XACKs it.

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/task"
	"github.com/redis/go-redis/v9"
)

// GroupReport is the report-to-read consumer group on s:<S>:log and the name
// of its proc line.
const GroupReport = "report-to-read"

// FunctionReportRead is registered by internal/nsprint/fn/lua/report.lua.
const FunctionReportRead = "ns_report_read"

// ReportTitleSuffix is the DONE-WHEN every report read carries.
const ReportTitleSuffix = "DONE-WHEN: typed DISPOSITION on the report with score"

// ReportReadID is the fixed identity of a card attempt's report read,
// report-<label>-a<attempt>. It names the attempt, never the reader, so a
// second end event for the same attempt finds the one task.
func ReportReadID(label, attempt string) string {
	return "report-" + label + "-a" + attempt
}

// NoCommit reports whether a card committed nothing: no pushed sha, no
// branch and no PR. Such a card ending DONE skips harvest (spec 3.2) and is
// the report-to-read rule's; any other DONE card is ok-to-friend's.
func NoCommit(card map[string]string) bool {
	pr := card["pr"]
	return card["pushed_sha"] == "" && card["branch"] == "" && (pr == "" || pr == "0")
}

// Report is the report-to-read rule for one sprint.
type Report struct {
	Store    *store.Store
	Sprint   string
	Consumer string // this process instance's consumer name
	Actor    string
	Count    int64         // events per read; 0 means 100
	Block    time.Duration // block of the first new-event read; 0 means 1 s
}

func (r *Report) loop() (groupLoop, error) {
	if r == nil || r.Store == nil || r.Sprint == "" || r.Consumer == "" {
		return groupLoop{}, fmt.Errorf("report-to-read: store, sprint and consumer are required")
	}
	return groupLoop{store: r.Store, sprint: r.Sprint, group: GroupReport, consumer: r.Consumer,
		count: r.Count, block: r.Block}, nil
}

// Start creates the consumer group if the sprint has none yet and claims
// every entry still pending under a previous instance (spec 5.4).
func (r *Report) Start(ctx context.Context) error {
	g, err := r.loop()
	if err != nil {
		return err
	}
	return g.start(ctx)
}

// Pass handles this instance's pending entries, then drains new events, and
// writes proc:report-to-read.
func (r *Report) Pass(ctx context.Context) (int, error) {
	g, err := r.loop()
	if err != nil {
		return 0, err
	}
	return g.passWithProc(ctx, r.handleBatch)
}

// handleBatch acks every event that is not a card `ended` receipt in one
// XACK, reads the ended cards in one pipeline, takes the reader census once
// if any card needs a reader, and handles each ended event with one
// function call.
func (r *Report) handleBatch(ctx context.Context, msgs []redis.XMessage) (int, error) {
	client := r.Store.Client()
	logKey := "s:" + r.Sprint + ":log"
	var ignore []string
	var events []*okEvent
	for _, m := range msgs {
		kind, _ := m.Values["kind"].(string)
		to, _ := m.Values["to"].(string)
		label, _ := m.Values["id"].(string)
		attempt, _ := m.Values["attempt"].(string)
		if kind != "card" || to != "ended" || label == "" {
			ignore = append(ignore, m.ID)
			continue
		}
		events = append(events, &okEvent{msg: m, to: to, label: label, attempt: attempt})
	}
	if len(ignore) > 0 {
		if err := client.XAck(ctx, logKey, GroupReport, ignore...).Err(); err != nil {
			return 0, fmt.Errorf("report-to-read: ack: %w", err)
		}
	}
	if len(events) == 0 {
		return 0, nil
	}
	pipe := client.Pipeline()
	cmds := make([]*redis.MapStringStringCmd, len(events))
	for i, e := range events {
		cmds[i] = pipe.HGetAll(ctx, "s:"+r.Sprint+":card:"+e.label)
	}
	if _, err := pipe.Exec(ctx); err != nil {
		return 0, fmt.Errorf("report-to-read: read cards: %w", err)
	}
	var census *readerCensus
	handled := 0
	var deferred error
	for i, e := range events {
		e.card = cmds[i].Val()
		err := r.onEnded(ctx, e, &census)
		if errors.Is(err, ErrNoReaders) {
			deferred = err
			continue
		}
		if err != nil {
			return handled, err
		}
		handled++
	}
	return handled, deferred
}

func (r *Report) skip(ctx context.Context, e *okEvent, reason string) error {
	return r.Store.Client().FCall(ctx, FunctionOkFriendSkip, nil,
		r.Sprint, GroupReport, e.msg.ID, reason, "").Err()
}

// onEnded: a card that ended DONE with no commit gets its one report read;
// every other ended event is acked with no transition (a committed card is
// ok-to-friend's, a card not DONE is the 3.3 classification's).
func (r *Report) onEnded(ctx context.Context, e *okEvent, census **readerCensus) error {
	c := e.card
	switch {
	case len(c) == 0:
		return r.skip(ctx, e, "no-card")
	case c["attempt"] != e.attempt:
		return r.skip(ctx, e, "stale-attempt")
	case c["outcome"] != "DONE":
		return r.skip(ctx, e, "not-done")
	case isCICard(e.label, c):
		return r.skip(ctx, e, "ci-card")
	case !NoCommit(c):
		return r.skip(ctx, e, "committed")
	}
	if *census == nil {
		got, err := (&OkFriend{Store: r.Store, Sprint: r.Sprint}).census(ctx)
		if err != nil {
			return fmt.Errorf("report-to-read: %w", err)
		}
		*census = got
	}
	readers := (*census).pick(1, c["author"])
	if len(readers) == 0 {
		return ErrNoReaders
	}
	friend := readers[0]
	ref := c["results"]
	if ref == "" {
		ref = c["identity"]
	}
	priority, _ := strconv.Atoi(c["priority"])
	req := task.PushRequest{
		Sprint: r.Sprint, ID: ReportReadID(e.label, e.attempt), Kind: task.KindRead,
		Title:   fmt.Sprintf("read report %s attempt %s (no commit) at %s | %s", e.label, e.attempt, ref, ReportTitleSuffix),
		Effects: task.EffectsNone, Repo: c["repo"], Ref: ref, To: friend, Front: true, Priority: priority,
	}
	reply, err := r.Store.Client().FCall(ctx, FunctionReportRead, nil,
		r.Sprint, GroupReport, e.msg.ID, e.label, e.attempt,
		req.ID, friend, req.Title, req.Repo, req.Ref, strconv.Itoa(priority), task.PayloadSHA(req), r.Actor).Result()
	if err != nil {
		return fmt.Errorf("report-to-read: %s attempt %s: %w", e.label, e.attempt, err)
	}
	if values, ok := reply.([]any); ok && len(values) > 0 && values[0] == "RETRY" {
		return ErrNoReaders
	}
	(*census).load[friend]++
	return nil
}
