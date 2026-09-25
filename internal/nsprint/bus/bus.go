// Package bus is the bus in Redis (nova-tools #3865, rowan-new
// specs/bus-redis.md): the people doing the work talk to Rowan, and to each
// other, over Redis streams instead of markdown notes committed to a git repo.
//
// Keys, and nothing else (the friend seat's ACL grant is ~bus:*):
//
//	bus:<to>        STREAM, one entry per message: from, to, kind, subject,
//	                body, ref, re, at (ms, Redis TIME). Consumer group <to>
//	                on a person's inbox; on bus:all one group per reader.
//	bus:sent:<from> STREAM, the sender's outbox: the same fields plus xid,
//	                the inbox entry's id.
//
// A post is one FCALL (ns_bus_post, internal/nsprint/fn/lua/bus.lua): both
// XADDs in one step, the outbox copy naming the id the inbox XADD returned.
// A read is one pipeline (the group creates, this reader's pending entries,
// then new ones) and one XACK pipeline after the entries are printed, so a
// reader that dies between delivery and ack gets the same entries again on
// its next read. No files, no git, no TTL, no KEYS or SCAN.
//
// Scores never travel on the bus: a read is recorded by `nova-sprint read
// post`, a task closed by `task done`; a note may say done and carry the ref.
package bus

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// MaxBody is the largest body the bus carries, in bytes. A longer one belongs
// in a task record or a PR, and the message carries its ref.
const MaxBody = 16 * 1024

// All is the broadcast inbox: every person reads bus:all under a group of
// their own name, so one post reaches each of them once.
const All = "all"

// PostFunction is the nova_sprint library function a post calls.
const PostFunction = "ns_bus_post"

// SeatRules is the whole ACL a seat needs for every bus verb, and all it
// grants: the one key pattern ~bus:*, the stream commands the verbs send, and
// the commands ns_bus_post runs under its caller (XGROUP CREATE, TIME, XADD).
// The fleet store's ACL (rowan-tools, the redis play) grants ~bus:* to the
// friend seat; internal/ci's TestBusFriendSeatReachesOnlyBus runs every verb
// under exactly these rules and is refused a key outside bus:*.
const SeatRules = "resetkeys resetchannels -@all +ping +client|setname +client|id ~bus:* " +
	"+xadd +xreadgroup +xack +xgroup|create +xgroup|setid +xpending +xlen +xinfo|groups +xrange +time +fcall|" + PostFunction

// People are the names a message is addressed to and sent from. A name
// outside this list is refused, never a new inbox: a misspelt --to would
// otherwise be a mailbox nobody reads.
var People = []string{"rowan", "emma", "stella", "johnny", "glenn"}

// Kinds are the message kinds the spec names.
var Kinds = []string{"note", "request", "question", "answer", "done", "ack"}

// scoreMark is the score line's shape; a body or subject holding it is refused.
const scoreMark = "SCORE who="

// InboxKey is the stream a message to `to` is added to.
func InboxKey(to string) string { return "bus:" + to }

// SentKey is the sender's outbox.
func SentKey(from string) string { return "bus:sent:" + from }

// Message is what a sender writes.
type Message struct {
	From, To, Kind, Subject, Body, Ref, Re string
}

// Entry is one delivered message.
type Entry struct {
	ID     string // the stream entry id
	Stream string // bus:<me> or bus:all
	Message
	At int64 // ms, Redis TIME at the post
}

// Refusal is a request the bus will not carry, with the remedy named.
type Refusal struct {
	Why, Remedy string
}

func (r *Refusal) Error() string { return r.Why + "; " + r.Remedy }

func isPerson(name string) bool {
	for _, p := range People {
		if p == name {
			return true
		}
	}
	return false
}

func isKind(k string) bool {
	for _, x := range Kinds {
		if x == k {
			return true
		}
	}
	return false
}

// CheckReader refuses a reader name that is not a person.
func CheckReader(me string) error {
	if !isPerson(me) {
		return &Refusal{Why: fmt.Sprintf("--as %s is not a person on the bus", oneline.Field(me)),
			Remedy: "use one of " + strings.Join(People, ", ")}
	}
	return nil
}

// Check is every refusal a post can meet, before anything is written.
func Check(m Message) error {
	if err := CheckReader(m.From); err != nil {
		return err
	}
	if m.To != All && !isPerson(m.To) {
		return &Refusal{Why: fmt.Sprintf("--to %s is not an inbox", oneline.Field(m.To)),
			Remedy: "use one of " + strings.Join(People, ", ") + " or all"}
	}
	if !isKind(m.Kind) {
		return &Refusal{Why: fmt.Sprintf("--kind %s is not a bus kind", oneline.Field(m.Kind)),
			Remedy: "use one of " + strings.Join(Kinds, ", ")}
	}
	if strings.TrimSpace(m.Subject) == "" {
		return &Refusal{Why: "the subject is empty", Remedy: "say what the message is in --subject"}
	}
	if len(m.Body) > MaxBody {
		return &Refusal{Why: fmt.Sprintf("the body is %d bytes, over %d", len(m.Body), MaxBody),
			Remedy: "put it in a task record or a PR and send the ref with --ref"}
	}
	if strings.Contains(m.Body, scoreMark) || strings.Contains(m.Subject, scoreMark) {
		return &Refusal{Why: "a score line (" + scoreMark + ") never travels on the bus",
			Remedy: "record the read with nova-sprint read post, then post a note with --ref"}
	}
	if strings.ContainsAny(m.Ref, " \t\r\n") || strings.ContainsAny(m.Re, " \t\r\n") {
		return &Refusal{Why: "--ref and --re are single tokens", Remedy: "pass a task id, repo#n or an entry id"}
	}
	return nil
}

// Post checks the message and adds it to the inbox and the sender's outbox in
// one FCALL. It returns the inbox entry id.
func Post(ctx context.Context, c redis.Cmdable, m Message) (string, error) {
	if err := Check(m); err != nil {
		return "", err
	}
	id, err := c.FCall(ctx, PostFunction, nil, m.From, m.To, m.Kind, m.Subject, m.Body, m.Ref, m.Re).Text()
	if err != nil {
		if strings.Contains(err.Error(), "Function not found") {
			return "", fmt.Errorf("%s is not loaded: run nova-sprint fn load (%w)", PostFunction, err)
		}
		return "", err
	}
	return id, nil
}

// streams are a reader's two inboxes, in the order every read names them.
func streams(me string) []string { return []string{InboxKey(me), InboxKey(All)} }

func ignoreBusy(err error) error {
	if err != nil && !strings.HasPrefix(err.Error(), "BUSYGROUP") {
		return err
	}
	return nil
}

// Fetch delivers up to n entries to me, oldest first: first the entries
// delivered to me before and never acked (a read that died before its ack),
// then new ones. On first use it creates me's groups at $ (from the start
// with from0, which also rewinds an existing group). Nothing is acked here;
// an entry delivered and not returned (over n) stays pending and comes back
// on the next Fetch.
func Fetch(ctx context.Context, c redis.Cmdable, me string, n int, from0 bool) ([]Entry, error) {
	if err := CheckReader(me); err != nil {
		return nil, err
	}
	if n <= 0 {
		n = 20
	}
	start := "$"
	if from0 {
		start = "0"
	}
	ss := streams(me)
	pipe := c.Pipeline()
	var creates []*redis.StatusCmd
	for _, s := range ss {
		creates = append(creates, pipe.XGroupCreateMkStream(ctx, s, me, start))
		if from0 {
			creates = append(creates, pipe.XGroupSetID(ctx, s, me, "0"))
		}
	}
	old := pipe.XReadGroup(ctx, &redis.XReadGroupArgs{Group: me, Consumer: me, Streams: append(append([]string{}, ss...), "0", "0"), Count: int64(n), Block: -1})
	fresh := pipe.XReadGroup(ctx, &redis.XReadGroupArgs{Group: me, Consumer: me, Streams: append(append([]string{}, ss...), ">", ">"), Count: int64(n), Block: -1})
	_, _ = pipe.Exec(ctx)
	for _, cmd := range creates {
		if err := ignoreBusy(cmd.Err()); err != nil {
			return nil, err
		}
	}
	var out []Entry
	seen := map[string]bool{}
	for _, cmd := range []*redis.XStreamSliceCmd{old, fresh} {
		res, err := cmd.Result()
		if err != nil && !errors.Is(err, redis.Nil) {
			return nil, err
		}
		for _, st := range res {
			for _, m := range st.Messages {
				k := st.Stream + " " + m.ID
				if seen[k] || m.Values == nil {
					continue
				}
				seen[k] = true
				out = append(out, entryOf(st.Stream, m))
			}
		}
	}
	SortEntries(out)
	if len(out) > n {
		out = out[:n]
	}
	return out, nil
}

// Ack marks the entries read, one pipeline.
func Ack(ctx context.Context, c redis.Cmdable, me string, es []Entry) error {
	if len(es) == 0 {
		return nil
	}
	by := map[string][]string{}
	for _, e := range es {
		by[e.Stream] = append(by[e.Stream], e.ID)
	}
	pipe := c.Pipeline()
	for _, s := range streams(me) {
		if ids := by[s]; len(ids) > 0 {
			pipe.XAck(ctx, s, me, ids...)
		}
	}
	_, err := pipe.Exec(ctx)
	return err
}

// Tail delivers entries to emit as they arrive until ctx ends: first what is
// pending or new (Fetch), then one blocking XREADGROUP per wait of `block`.
// Each batch is acked after emit returns.
func Tail(ctx context.Context, c redis.Cmdable, me string, block time.Duration, emit func(Entry)) error {
	for {
		es, err := Fetch(ctx, c, me, 100, false)
		if err != nil {
			return ctxOr(ctx, err)
		}
		for _, e := range es {
			emit(e)
		}
		if err := Ack(ctx, c, me, es); err != nil {
			return ctxOr(ctx, err)
		}
		if len(es) < 100 {
			break
		}
	}
	for ctx.Err() == nil {
		res, err := c.XReadGroup(ctx, &redis.XReadGroupArgs{Group: me, Consumer: me,
			Streams: append(streams(me), ">", ">"), Count: 100, Block: block}).Result()
		if errors.Is(err, redis.Nil) {
			continue
		}
		if err != nil {
			return ctxOr(ctx, err)
		}
		var es []Entry
		for _, st := range res {
			for _, m := range st.Messages {
				es = append(es, entryOf(st.Stream, m))
			}
		}
		SortEntries(es)
		for _, e := range es {
			emit(e)
		}
		if err := Ack(ctx, c, me, es); err != nil {
			return ctxOr(ctx, err)
		}
	}
	return nil
}

func ctxOr(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return nil
	}
	return err
}

// PendingEntry is one entry delivered to me and not acked.
type PendingEntry struct {
	ID, Stream string
	Idle       time.Duration
	Deliveries int64
}

// Pending is what was delivered to me and not acked, one pipeline.
func Pending(ctx context.Context, c redis.Cmdable, me string) ([]PendingEntry, error) {
	if err := CheckReader(me); err != nil {
		return nil, err
	}
	pipe := c.Pipeline()
	var cmds []*redis.XPendingExtCmd
	for _, s := range streams(me) {
		cmds = append(cmds, pipe.XPendingExt(ctx, &redis.XPendingExtArgs{Stream: s, Group: me, Start: "-", End: "+", Count: 10000, Idle: -1}))
	}
	_, _ = pipe.Exec(ctx)
	var out []PendingEntry
	for i, cmd := range cmds {
		res, err := cmd.Result()
		if err != nil {
			if isNoGroup(err) {
				continue
			}
			return nil, err
		}
		for _, p := range res {
			out = append(out, PendingEntry{ID: p.ID, Stream: streams(me)[i], Idle: p.Idle, Deliveries: p.RetryCount})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return idLess(out[i].ID, out[j].ID) })
	return out, nil
}

func isNoGroup(err error) bool {
	s := err.Error()
	return strings.HasPrefix(s, "NOGROUP") || strings.Contains(s, "no such key")
}

// Inbox is one inbox's length and, per reading group, its unread count
// (delivered-and-unacked plus not yet delivered).
type Inbox struct {
	Name   string
	Len    int64
	Unread map[string]int64
}

// Ls is every inbox (each person's and all), one pipeline.
func Ls(ctx context.Context, c redis.Cmdable) ([]Inbox, error) {
	names := append(append([]string{}, People...), All)
	pipe := c.Pipeline()
	lens := make([]*redis.IntCmd, len(names))
	groups := make([]*redis.XInfoGroupsCmd, len(names))
	for i, n := range names {
		lens[i] = pipe.XLen(ctx, InboxKey(n))
		groups[i] = pipe.XInfoGroups(ctx, InboxKey(n))
	}
	_, _ = pipe.Exec(ctx)
	out := make([]Inbox, 0, len(names))
	for i, n := range names {
		l, err := lens[i].Result()
		if err != nil {
			return nil, err
		}
		in := Inbox{Name: n, Len: l, Unread: map[string]int64{}}
		gs, err := groups[i].Result()
		if err != nil && !isNoGroup(err) {
			return nil, err
		}
		for _, g := range gs {
			if n != All && g.Name != n {
				continue
			}
			lag := g.Lag
			if lag < 0 {
				lag = l - g.EntriesRead
			}
			in.Unread[g.Name] = lag + g.Pending
		}
		if n != All {
			if _, ok := in.Unread[n]; !ok {
				in.Unread[n] = l
			}
		}
		out = append(out, in)
	}
	return out, nil
}

// Lookup finds entry xid in me's inboxes, one pipeline, for a reply.
func Lookup(ctx context.Context, c redis.Cmdable, me, xid string) (Entry, error) {
	if err := CheckReader(me); err != nil {
		return Entry{}, err
	}
	pipe := c.Pipeline()
	ss := streams(me)
	cmds := make([]*redis.XMessageSliceCmd, len(ss))
	for i, s := range ss {
		cmds[i] = pipe.XRangeN(ctx, s, xid, xid, 1)
	}
	_, _ = pipe.Exec(ctx)
	for i, cmd := range cmds {
		res, err := cmd.Result()
		if err != nil {
			if strings.Contains(err.Error(), "Invalid stream ID") {
				return Entry{}, &Refusal{Why: "--re " + oneline.Field(xid) + " is not an entry id", Remedy: "pass the id bus read printed"}
			}
			return Entry{}, err
		}
		if len(res) == 1 {
			return entryOf(ss[i], res[0]), nil
		}
	}
	return Entry{}, &Refusal{Why: "--re " + oneline.Field(xid) + " is in neither " + ss[0] + " nor " + ss[1], Remedy: "pass the id bus read printed to you"}
}

func entryOf(stream string, m redis.XMessage) Entry {
	s := func(k string) string {
		v, _ := m.Values[k].(string)
		return v
	}
	at, _ := strconv.ParseInt(s("at"), 10, 64)
	return Entry{ID: m.ID, Stream: stream, At: at, Message: Message{
		From: s("from"), To: s("to"), Kind: s("kind"), Subject: s("subject"), Body: s("body"), Ref: s("ref"), Re: s("re"),
	}}
}

// SortEntries orders entries oldest first by id; ids of the two inboxes
// compare by their millisecond part, then their sequence.
func SortEntries(es []Entry) {
	sort.SliceStable(es, func(i, j int) bool { return idLess(es[i].ID, es[j].ID) })
}

func idLess(a, b string) bool {
	am, as := splitID(a)
	bm, bs := splitID(b)
	if am != bm {
		return am < bm
	}
	return as < bs
}

func splitID(id string) (uint64, uint64) {
	ms, seq, _ := strings.Cut(id, "-")
	m, _ := strconv.ParseUint(ms, 10, 64)
	s, _ := strconv.ParseUint(seq, 10, 64)
	return m, s
}

var eastern = func() *time.Location {
	l, err := time.LoadLocation("America/New_York")
	if err != nil {
		return nil
	}
	return l
}()

// AtET is the entry's time in Eastern (Glenn's clock), one token.
func AtET(ms int64) string {
	t := time.UnixMilli(ms)
	if eastern == nil {
		return t.UTC().Format("2006-01-02T15:04:05Z")
	}
	return t.In(eastern).Format("2006-01-02T15:04:05") + "ET"
}

// Line is an entry's header: `<xid> <at ET> <from> <kind> "<subject>"`, then
// ref=, re= and to=all when set.
func (e Entry) Line() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s %s %s %s %s", e.ID, AtET(e.At), oneline.Field(e.From), oneline.Field(e.Kind), oneline.Quote(e.Subject))
	if e.Ref != "" {
		b.WriteString(" ref=" + oneline.Field(e.Ref))
	}
	if e.Re != "" {
		b.WriteString(" re=" + oneline.Field(e.Re))
	}
	if e.To == All {
		b.WriteString(" to=all")
	}
	return b.String()
}
