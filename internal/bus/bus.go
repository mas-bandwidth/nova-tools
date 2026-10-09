// Package bus is the message bus between AIs over Redis streams
// (docs/SPEC-BUS.md; the delivery machine is tla/Bus2.tla). A message is one
// stream entry, written to every recipient's stream and to the log in one
// transaction; a recipient reads its stream through a consumer group, so a
// message is pending from the moment it is delivered until it is acked, and a
// reader that crashes before acking is handed it again. The logic lives here,
// apart from the transport: Bus holds the rules over a Store, the few Redis
// commands the bus uses (redis.go is the Redis one, fake.go the one tests run
// on), so every rule is tested with no socket.
package bus

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"
)

// The keys (SPEC-BUS.md, the data): one stream per recipient, and one log.
const (
	LogKey = "bus2:log"
	Prefix = "bus2:to:"
)

// StreamOf is the recipient's stream.
func StreamOf(name string) string { return Prefix + name }

// Limits (SPEC-BUS.md, the data).
const (
	MaxBody = 1 << 20 // bytes of a body
	MaxName = 64      // bytes of a name
)

// ClaimAfter is how long a delivered message stays with its reader before
// recv hands it to another. It is longer than the longest delivery a reader
// makes (nova-friend's ten minute turn and the kill that ends it), so a live
// reader mid-turn is never handed its message a second time; a dead one's
// is claimed after this (SPEC-BUS.md, the semantics; tla/Bus2.tla
// HeldStaysHeld).
const ClaimAfter = 15 * time.Minute

// Consumer is the one consumer name of every reader: with ClaimAfter, who
// holds an entry is told by its idle time, never by a name. It keeps the
// bus2 spelling with the keys: a consumer name lives in the live store's
// pending lists, and renaming it there is a migration (SPEC-BUS.md, the data).
const Consumer = "nova-bus2"

// unknown is the refusal of a name the roster does not hold, with how to add one.
func unknown(n string) string {
	return fmt.Sprintf("%s is no known name; the names are nova-config's friend and machine rows (nova-bus names lists them); add one with nova-config friend add %s --slots 1 --tiers flash --as <you>, then nova-config apply", n, n)
}

// nameRe is a name: lowercase letters, digits and hyphens.
var nameRe = regexp.MustCompile(`^[a-z0-9-]+$`)

// CheckName says why s is no name, "" when it is one.
func CheckName(s string) string {
	switch {
	case s == "":
		return "a name is empty; it wants lowercase letters, digits and hyphens, at most 64 bytes"
	case len(s) > MaxName:
		return fmt.Sprintf("the name %q is %d bytes, at most %d", s, len(s), MaxName)
	case !nameRe.MatchString(s):
		return fmt.Sprintf("the name %q is not lowercase letters, digits and hyphens", s)
	}
	return ""
}

// Message is one message of the bus: its fields on every stream it is on.
type Message struct {
	ID      string
	From    string
	To      []string
	CC      []string
	Subject string
	Re      string
	Kind    string // one of Kinds; "" is status
	At      time.Time
	Body    string
	// Token is the caller's word for this one logical send, the same on
	// every retry of it (token.go); it is the sender's, never on the entry.
	// Empty is a send with none: every call a new message.
	Token string
}

// The kinds of a message (SPEC-BUS.md, the kind of a message): the bus's own
// vocabulary, which a reader filters on; the bus gives none of them a meaning.
const (
	KindReport  = "report"
	KindAck     = "ack"
	KindStatus  = "status"
	KindRequest = "request"
	KindBlocker = "blocker"
)

// Kinds is every kind, in the order the help lists them.
var Kinds = []string{KindReport, KindAck, KindStatus, KindRequest, KindBlocker}

// CheckKinds says why ks are no kinds, "" when each is one.
func CheckKinds(ks ...string) string {
	for _, k := range ks {
		if !slices.Contains(Kinds, k) {
			return fmt.Sprintf("the kind %q is not one of %s", k, strings.Join(Kinds, ", "))
		}
	}
	return ""
}

// KindName is the message's kind, status when it has none (a message sent
// before kinds).
func (m Message) KindName() string {
	if m.Kind == "" {
		return KindStatus
	}
	return m.Kind
}

// FilterKinds is the entries whose message is one of kinds; with no kinds, all of them.
func FilterKinds(es []Entry, kinds []string) []Entry {
	if len(kinds) == 0 {
		return es
	}
	var out []Entry
	for _, e := range es {
		if slices.Contains(kinds, e.Message().KindName()) {
			out = append(out, e)
		}
	}
	return out
}

// Fields is the message as the stream entry holds it.
func (m Message) Fields() map[string]string {
	return map[string]string{
		"id": m.ID, "from": m.From, "to": strings.Join(m.To, ","), "cc": strings.Join(m.CC, ","),
		"subject": m.Subject, "re": m.Re, "kind": m.Kind, "at": m.At.UTC().Format(time.RFC3339), "body": m.Body,
	}
}

// Parse is the message an entry's fields hold. A field that is not there is
// empty (a kind that is not there is read as status by KindName); an `at` that is no instant is the zero time, never a refusal, so a
// log with one odd entry still reads.
func Parse(fields map[string]string) Message {
	at, _ := time.Parse(time.RFC3339, fields["at"]) // ignored: a bad stamp reads as the zero time, said above
	return Message{
		Kind: fields["kind"], ID: fields["id"], From: fields["from"], To: list(fields["to"]), CC: list(fields["cc"]),
		Subject: fields["subject"], Re: fields["re"], At: at, Body: fields["body"],
	}
}

// list splits a comma list, dropping empty words.
func list(s string) []string {
	var out []string
	for _, w := range strings.Split(s, ",") {
		if w = strings.TrimSpace(w); w != "" {
			out = append(out, w)
		}
	}
	return out
}

// Entry is one stream entry as read: the stream, the entry's id in it
// (<ms>-<seq>, the server's) and its fields.
type Entry struct {
	Stream string
	Entry  string
	Fields map[string]string
	// Stage is the message's receipt state as recv found it, before its
	// delivered stamp ("" none; stages.go): acted on a message the claim hands
	// in again after a turn acted on it. Only recv sets it.
	Stage string
}

// Message is the entry's message.
func (e Entry) Message() Message { return Parse(e.Fields) }

// Store is the few Redis commands the bus uses, each one round trip. The
// bus never deletes: no command here removes an entry, a group or a key
// (a token's record expires: AddOnce).
type Store interface {
	// Roster is the known names (nova-config's friend and machine rows, the
	// sets `friends` and `machines`) and the server's time (TIME), in one trip.
	Roster(ctx context.Context) (names []string, now time.Time, err error)
	// Members is the friends and the machines apart (the sets `friends` and
	// `machines`) and the server's time (TIME), in one trip: Roster split, so
	// a send knows which recipients are friends, owed a receipt.
	Members(ctx context.Context) (friends, machines []string, now time.Time, err error)
	// AddAll appends one entry with fields to every stream, and makes every
	// mark (HSET, or HDEL when it clears), in one MULTI/EXEC: the entry and its
	// marks are on all of them or on none.
	AddAll(ctx context.Context, streams []string, fields map[string]string, marks ...Mark) error
	// AddOnce is AddAll under a send's token, in one atomic step (a script):
	// when key holds a record it writes nothing and answers that record and
	// found; else it sets key to record, expiring after keep, and appends
	// the entry and makes the marks as AddAll does. The record's key is the
	// one key the bus writes that the store removes, by its expiry.
	AddOnce(ctx context.Context, key, record string, keep time.Duration, streams []string, fields map[string]string, marks ...Mark) (prior string, found bool, err error)
	// Sent is the record at key (GET), and whether there is one.
	Sent(ctx context.Context, key string) (record string, found bool, err error)
	// Unmark clears fields of the hash at key (HDEL) and says how many were there.
	Unmark(ctx context.Context, key string, fields ...string) (int64, error)
	// Marks is the whole hash at each key, in one trip (a pipeline of HGETALL);
	// a key that is not there is an empty map.
	Marks(ctx context.Context, keys ...string) ([]map[string]string, error)
	// EnsureGroup makes the group on the stream from its start, making the
	// stream when it is not there (XGROUP CREATE ... 0 MKSTREAM); a group
	// already there is fine.
	EnsureGroup(ctx context.Context, stream, group string) error
	// Claim hands consumer up to count entries pending for the group that have
	// been idle (delivered and not acked) for at least minIdle (XAUTOCLAIM
	// <minIdle> 0-0): what a reader that died, or stalled, was holding.
	Claim(ctx context.Context, stream, group, consumer string, minIdle time.Duration, count int) ([]Entry, error)
	// Read hands consumer up to count entries the group has never delivered
	// (XREADGROUP ... >), waiting up to block for one when block is above
	// zero, else answering at once.
	Read(ctx context.Context, stream, group, consumer string, block time.Duration, count int) ([]Entry, error)
	// Release makes the entries pending for the group claimable at once
	// (XCLAIM ... IDLE <ClaimAfter> JUSTID): what a reader that skipped them
	// hands back, in one trip.
	Release(ctx context.Context, stream, group string, entries ...string) error
	// Ack acks entries for the group (XACK) and says how many were pending.
	Ack(ctx context.Context, stream, group string, entries ...string) (int64, error)
	// Pending is the entry ids pending for the group, up to count (XPENDING).
	Pending(ctx context.Context, stream, group string, count int) ([]string, error)
	// Group is the group's last delivered entry id, and whether the group is
	// there at all (XINFO GROUPS).
	Group(ctx context.Context, stream, group string) (lastDelivered string, exists bool, err error)
	// Range is the entries of the stream from from to to, up to count
	// (XRANGE; "-", "+" and the exclusive "(<id>" as Redis reads them).
	Range(ctx context.Context, stream, from, to string, count int) ([]Entry, error)
	// Get is the named entries of the stream, in one trip (a pipeline of XRANGE
	// id id); an id that is not there is left out.
	Get(ctx context.Context, stream string, entries []string) ([]Entry, error)
	// Forward moves each id's receipt on the hash at key to state at the
	// store's time (TIME), by the receipt rule (forwardLua): only forward, and only
	// delivered starts one; it answers each id's state before, "" for none, in
	// one atomic step (a script). It is the one writer of a receipt.
	Forward(ctx context.Context, key, state string, ids ...string) ([]string, error)
}

// Waiter is the two reads a wait makes over a Store that also holds them: the
// Redis store does; a Store without them cannot wait (SPEC-BUS.md, the verbs:
// wait).
type Waiter interface {
	// Tail is the stream's last entry id and whether the stream is there at
	// all (XINFO STREAM's last-generated-id, "0-0" for an empty one): the
	// cursor a wait arms at when the caller gives none (SPEC-BUS.md, the
	// verbs: wait).
	Tail(ctx context.Context, stream string) (last string, exists bool, err error)
	// BlockRead hands up to count entries of the stream lying past the id
	// after (XREAD), waiting up to block for one when block is above zero (0
	// is for ever), else answering at once. It never touches the consumer
	// group, so what it hands out is still a later recv's to deliver and ack
	// (SPEC-BUS.md, the verbs: wait).
	BlockRead(ctx context.Context, stream, after string, block time.Duration, count int) ([]Entry, error)
}

// Waiter is the Store's wait reads, or the refusal of a Store that has none.
func (b *Bus) Waiter() (Waiter, error) {
	w, ok := b.Store.(Waiter)
	if !ok {
		return nil, errors.New("this store cannot wait")
	}
	return w, nil
}

// Bus is the rules over a Store.
type Bus struct {
	Store Store
	// Rand fills a ULID's random half; crypto/rand when nil.
	Rand func([]byte) (int, error)
	// TokenLife is how long a retry under a send's token answers the
	// original message (DefaultTokenLife when zero); TokenCleanup is when the
	// store drops the token's record (DefaultTokenCleanup when zero, never
	// before the life ends). token.go.
	TokenLife, TokenCleanup time.Duration
	// OnStampError hears a delivered receipt recv stamps that the store did
	// not write; the recv goes on, and the message stays in Overdue until a
	// later stamp lands. Nil drops it, the overdue alarm standing for it
	// (stages.go).
	OnStampError func(err error)
}

// Refusal is a reason a verb could not run as asked: the input, not the store.
type Refusal struct{ Problems []string }

func (r *Refusal) Error() string { return strings.Join(r.Problems, "; ") }

// Send checks the message, stamps it with the store's time and a ULID made
// from that time, and appends it to every recipient's stream and the log in
// one transaction. It answers the message as sent. Every problem of the
// message is named at once in one Refusal: a name that is no name, a
// recipient the roster does not know (with how to add one), an empty body, a
// body over MaxBody, a from that is unknown.
//
// A message to a friend is owed her session's receipt (receipt.go): the
// transaction marks it on bus2:owed:<friend> for each friend it names but the
// sender, and a message from a friend naming another (re) is her receipt of
// that one, cleared in the same transaction.
//
// A message naming another (re) is the sender's act on it: its receipt on
// bus2:receipt:<sender> moves to acted in the same transaction, when the
// sender was delivered it (stages.go).
//
// A message with a Token is sent once under it (token.go): the record of the
// token is written in the same step, and a send that finds it writes nothing
// and answers the message it names, id and at, so a caller whose response was
// lost after the write committed retries with the same token and the same
// arguments and gets the original. The same token with other arguments, or
// past its life, is refused. Without a token a lost response retried is a
// second message.
func (b *Bus) Send(ctx context.Context, m Message) (Message, error) {
	m, now, friends, err := b.check(ctx, m)
	if err != nil {
		return Message{}, err
	}
	if m.ID, err = b.ulid(now); err != nil {
		return Message{}, err
	}
	var streams []string
	for _, n := range slices.Compact(slices.Sorted(slices.Values(slices.Concat(m.To, m.CC)))) {
		streams = append(streams, StreamOf(n))
	}
	streams = append(streams, LogKey)
	if m.Token != "" {
		return b.sendOnce(ctx, m, now, streams, owe(m, friends))
	}
	if err := b.Store.AddAll(ctx, streams, m.Fields(), owe(m, friends)...); err != nil {
		return Message{}, err
	}
	return m, nil
}

// Check is Send that writes nothing (a send's --dry-run): every problem of the message
// named at once, as Send names them, and the message as it would be sent, at the store's
// time with its recipients sorted, and no id: an id is made for a message sent.
func (b *Bus) Check(ctx context.Context, m Message) (Message, error) {
	m, _, _, err := b.check(ctx, m)
	return m, err
}

// check is the message as Send would send it, the store's time it is stamped
// with, and the friends of the roster.
func (b *Bus) check(ctx context.Context, m Message) (Message, time.Time, []string, error) {
	var problems []string
	for _, n := range append(append([]string{m.From}, m.To...), m.CC...) {
		if p := CheckName(n); p != "" {
			problems = append(problems, p)
		}
	}
	if len(m.To) == 0 {
		problems = append(problems, "no recipient: --to wants one or more names, comma-separated")
	}
	switch {
	case strings.TrimSpace(m.Body) == "":
		problems = append(problems, "the body is empty; it wants the message's text")
	case len(m.Body) > MaxBody:
		problems = append(problems, fmt.Sprintf("the body is %d bytes, at most %d", len(m.Body), MaxBody))
	}
	if m.Kind == "" {
		m.Kind = KindStatus
	}
	if p := CheckKinds(m.Kind); p != "" {
		problems = append(problems, p)
	}
	if strings.TrimSpace(m.Subject) == "" {
		problems = append(problems, "the subject is empty; it wants one line saying what the message is")
	}
	if p := CheckToken(m.Token); p != "" {
		problems = append(problems, p)
	}
	if len(problems) > 0 {
		return Message{}, time.Time{}, nil, &Refusal{problems}
	}
	friends, machines, now, err := b.Store.Members(ctx)
	if err != nil {
		return Message{}, time.Time{}, nil, err
	}
	names := slices.Concat(friends, machines)
	for _, n := range append(append([]string{m.From}, m.To...), m.CC...) {
		if !slices.Contains(names, n) {
			problems = append(problems, unknown(n))
		}
	}
	if len(problems) > 0 {
		return Message{}, time.Time{}, nil, &Refusal{slices.Compact(problems)}
	}
	m.At = now.UTC().Truncate(time.Second) // the entry's at is RFC3339, to the second
	m.To, m.CC = slices.Compact(slices.Sorted(slices.Values(m.To))), slices.Compact(slices.Sorted(slices.Values(m.CC)))
	return m, now, friends, nil
}

// Recv is one message for the recipient: the oldest one delivered and not
// acked whose reader has had it longer than ClaimAfter (a reader that died
// or stalled), else the oldest never delivered, waiting up to block for it.
// ok is false when there is none. A name the roster does not hold is
// refused, never given a stream to wait on. The group is made on first use.
// (tla/Bus2.tla: Recv, PendingBeforeNew, HeldStaysHeld)
func (b *Bus) Recv(ctx context.Context, as string, block time.Duration) (e Entry, ok bool, err error) {
	return b.RecvKinds(ctx, as, block, nil)
}

// RecvKinds is Recv for the messages whose kind is one of kinds (none: any).
// The message it hands out is stamped delivered on the recipient's receipts,
// one trip more, and carries the receipt it found (Entry.Stage): acted on one
// the claim hands in again after a turn acted on it (stages.go).
// A message the filter skips is handed back to the group at once (Store.Release),
// neither acked nor held, so a reader that asks for all gets it next; the skip
// costs one round trip per message skipped, and one more to release a run of
// them. (tla/Bus2.tla: Recv; a skipped message is back as a lost one)
func (b *Bus) RecvKinds(ctx context.Context, as string, block time.Duration, kinds []string) (e Entry, ok bool, err error) {
	if p := CheckName(as); p != "" {
		return Entry{}, false, &Refusal{[]string{p}}
	}
	if p := CheckKinds(kinds...); p != "" {
		return Entry{}, false, &Refusal{[]string{p}}
	}
	names, _, err := b.Store.Roster(ctx)
	if err != nil {
		return Entry{}, false, err
	}
	if !slices.Contains(names, as) {
		return Entry{}, false, &Refusal{[]string{unknown(as)}}
	}
	stream := StreamOf(as)
	if err := b.Store.EnsureGroup(ctx, stream, as); err != nil {
		return Entry{}, false, err
	}
	var skipped []string
	release := func() error {
		if len(skipped) == 0 {
			return nil
		}
		err := b.Store.Release(ctx, stream, as, skipped...)
		skipped = nil
		return err
	}
	// the claimed ones first, as without a filter; a skipped one is fresh
	// until released, so the claim runs out
	for {
		got, err := b.Store.Claim(ctx, stream, as, Consumer, ClaimAfter, 1)
		if err != nil {
			return Entry{}, false, err
		}
		if len(got) == 0 {
			break
		}
		if len(FilterKinds(got, kinds)) > 0 {
			return b.delivered(ctx, as, got[0]), true, release()
		}
		skipped = append(skipped, got[0].Entry)
	}
	if err := release(); err != nil {
		return Entry{}, false, err
	}
	for {
		got, err := b.Store.Read(ctx, stream, as, Consumer, block, 1)
		if err != nil || len(got) == 0 {
			return Entry{}, false, err
		}
		if len(FilterKinds(got, kinds)) > 0 {
			return b.delivered(ctx, as, got[0]), true, nil
		}
		if err := b.Store.Release(ctx, stream, as, got[0].Entry); err != nil {
			return Entry{}, false, err
		}
	}
}

// delivered stamps e delivered for as and answers it with the receipt found.
// (tla/Bus2Receipts.tla: RRecv)
func (b *Bus) delivered(ctx context.Context, as string, e Entry) Entry {
	e.Stage = b.stamp(ctx, as, Delivered, e.Message().ID)
	return e
}

// AckEntry acks one entry the recipient was handed (XACK); acking it again
// is a no-op that says so. (tla/Bus2.tla: Ack, AckIdempotent)
func (b *Bus) AckEntry(ctx context.Context, as, entry string) (acked bool, err error) {
	n, err := b.Store.Ack(ctx, StreamOf(as), as, entry)
	return n > 0, err
}

// Ack acks messages by their ids: each is looked up among the recipient's
// pending entries, so an id that is not pending (acked already, never
// delivered, or not this recipient's) is answered acked=false, never a
// failure: ack is idempotent. Ack by id is the session's verb (nova-bus ack),
// so it is also the session's receipt of every id it names, pending or not
// (Receipt): a daemon that acked the stream first takes nothing from it.
func (b *Bus) Ack(ctx context.Context, as string, ids []string) (map[string]bool, error) {
	acked, entries, err := b.pendingOf(ctx, as, ids)
	if err != nil {
		return nil, err
	}
	if len(entries) > 0 {
		if _, err := b.Store.Ack(ctx, StreamOf(as), as, entries...); err != nil {
			return nil, err
		}
	}
	if _, err := b.Receipt(ctx, as, ids); err != nil {
		return nil, err
	}
	return acked, nil
}

// WouldAck is Ack that writes nothing (an ack's --dry-run): each id true when it is
// pending for the recipient, so Ack would ack it.
func (b *Bus) WouldAck(ctx context.Context, as string, ids []string) (map[string]bool, error) {
	acked, _, err := b.pendingOf(ctx, as, ids)
	return acked, err
}

// pendingOf is each id true when it is among the recipient's pending entries, and those
// entries.
func (b *Bus) pendingOf(ctx context.Context, as string, ids []string) (map[string]bool, []string, error) {
	if p := CheckName(as); p != "" {
		return nil, nil, &Refusal{[]string{p}}
	}
	pending, err := b.pendingEntries(ctx, as)
	if err != nil {
		return nil, nil, err
	}
	acked := map[string]bool{}
	var entries []string
	for _, id := range ids {
		acked[id] = false
		for _, e := range pending {
			if e.Fields["id"] == id {
				entries = append(entries, e.Entry)
				acked[id] = true
			}
		}
	}
	return acked, entries, nil
}

// pendingLimit bounds one look at a recipient's pending entries.
const pendingLimit = 1000

// pendingEntries is the recipient's pending entries with their fields.
func (b *Bus) pendingEntries(ctx context.Context, as string) ([]Entry, error) {
	_, exists, err := b.Store.Group(ctx, StreamOf(as), as)
	if err != nil || !exists {
		return nil, err
	}
	ids, err := b.Store.Pending(ctx, StreamOf(as), as, pendingLimit)
	if err != nil || len(ids) == 0 {
		return nil, err
	}
	return b.Store.Get(ctx, StreamOf(as), ids)
}

// Peek is what waits for the recipient, reading only: the pending entries
// (delivered, not acked) and the new ones (never delivered), oldest first,
// up to pendingLimit of each.
func (b *Bus) Peek(ctx context.Context, as string) (pending, fresh []Entry, err error) {
	if p := CheckName(as); p != "" {
		return nil, nil, &Refusal{[]string{p}}
	}
	last, exists, err := b.Store.Group(ctx, StreamOf(as), as)
	if err != nil {
		return nil, nil, err
	}
	from := "-"
	if exists {
		if pending, err = b.pendingEntries(ctx, as); err != nil {
			return nil, nil, err
		}
		from = "(" + last
	}
	fresh, err = b.Store.Range(ctx, StreamOf(as), from, "+", pendingLimit)
	return pending, fresh, err
}

// logLimit bounds one read of the log; the caller caps what it shows.
const logLimit = 10000

// WaitMax bounds the message lines one wait prints (SPEC-BUS.md, the verbs:
// wait); the entries past them stay for the next run.
const WaitMax = 5

// WaitRead bounds the entries one blocking read of a wait takes: one batch
// holds the skipped and the counted of one burst, and the decision over it
// is one pure function (SPEC-BUS.md, the verbs: wait).
const WaitRead = 100

// WaitPick is the wait's decision over one batch of entries, a pure function
// (SPEC-BUS.md, the verbs: wait): the first WaitMax entries not from me whose
// subject starts with none of skips (matched without case), in order, and the
// cursor past every entry the walk saw -- a skipped entry moves it -- so a
// caller that re-arms with it misses nothing between runs. The walk stops at
// the WaitMax-th entry that counts, and the entries after it stay for the
// next run.
func WaitPick(entries []Entry, me string, skips []string) (kept []Entry, after string) {
	prefixes := make([]string, len(skips))
	for i, s := range skips {
		prefixes[i] = strings.ToLower(s)
	}
	for _, e := range entries {
		m := e.Message()
		if m.From == me || hasPrefix(m.Subject, prefixes) {
			after = e.Entry // a skipped entry moves the cursor and is not printed
			continue
		}
		kept = append(kept, e)
		if len(kept) == WaitMax {
			return kept, e.Entry
		}
		after = e.Entry
	}
	return kept, after
}

// hasPrefix is whether the subject starts with one of the prefixes, without
// case.
func hasPrefix(subject string, prefixes []string) bool {
	s := strings.ToLower(subject)
	for _, p := range prefixes {
		if p != "" && strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}

// WaitArm is the cursor a wait on as starts from: after when given (the
// stream's tail is not read), else the stream's last entry id read once
// ("0-0" when the stream is not there). A name the roster does not hold is
// refused, never given a stream to wait on (SPEC-BUS.md, the semantics), as
// recv refuses one.
func (b *Bus) WaitArm(ctx context.Context, as, after string) (string, error) {
	if p := CheckName(as); p != "" {
		return "", &Refusal{[]string{p}}
	}
	names, _, err := b.Store.Roster(ctx)
	if err != nil {
		return "", err
	}
	if !slices.Contains(names, as) {
		return "", &Refusal{[]string{unknown(as)}}
	}
	if after != "" {
		return after, nil
	}
	w, err := b.Waiter()
	if err != nil {
		return "", err
	}
	tail, exists, err := w.Tail(ctx, StreamOf(as))
	if err != nil {
		return "", err
	}
	if !exists || tail == "" {
		return "0-0", nil
	}
	return tail, nil
}

// Log is the log's messages from the entry id from ("-" for its start),
// oldest first, up to logLimit of them. A reader that always passes "-"
// sees only the oldest window, so once the log is longer than logLimit a
// fresh entry is past the cap on every read. Arm at LogCursor and read
// with LogForward.
func (b *Bus) Log(ctx context.Context, from string) ([]Entry, error) {
	return b.Store.Range(ctx, LogKey, from, "+", logLimit)
}

// LogCursor is the log's tail, the cursor a reader arms at so LogForward
// returns only entries appended after it. An empty log arms at "0-0", the
// id before any entry. The store's Tail is the same read a wait arms with.
func (b *Bus) LogCursor(ctx context.Context) (string, error) {
	w, err := b.Waiter()
	if err != nil {
		return "", err
	}
	tail, exists, err := w.Tail(ctx, LogKey)
	if err != nil {
		return "", err
	}
	if !exists || tail == "" {
		return "0-0", nil
	}
	return tail, nil
}

// LogForward is one bounded window of the log strictly after cursor, oldest
// first, at most logLimit entries. cursor is an entry id ("0-0" before any).
// next is the last entry read, or cursor when the window is empty, so the
// caller advances as it consumes and a fresh entry is not hidden behind the
// oldest logLimit. more is set when the window is full and a further read
// from next may hold more.
func (b *Bus) LogForward(ctx context.Context, cursor string) (es []Entry, next string, more bool, err error) {
	if cursor == "" {
		cursor = "0-0"
	}
	cursor = strings.TrimPrefix(cursor, "(")
	es, err = b.Log(ctx, "("+cursor)
	if err != nil {
		return nil, cursor, false, err
	}
	if len(es) == 0 {
		return nil, cursor, false, nil
	}
	return es, es[len(es)-1].Entry, len(es) == logLimit, nil
}

// IDAt is the first entry id a stream could hold at t (<ms>-0): the floor of
// a Range from that instant.
func IDAt(t time.Time) string { return fmt.Sprintf("%d-0", t.UnixMilli()) }

// Names is the roster, sorted.
func (b *Bus) Names(ctx context.Context) ([]string, error) {
	names, _, err := b.Store.Roster(ctx)
	if err != nil {
		return nil, err
	}
	sort.Strings(names)
	return slices.Compact(names), nil
}

// ulid is a ULID (ulid/spec: 48 bits of ms since the epoch, 80 random bits,
// Crockford base32, 26 characters) for the instant at, whose first ten random
// bits are the microsecond within the millisecond, so two ids made from the
// store's time sort in the order the store gave it, down to the microsecond
// TIME answers; 70 bits stay random. Written here rather than taken from a
// module: the encoding is twenty lines, and no adopted module of this
// repository makes one.
func (b *Bus) ulid(at time.Time) (string, error) {
	var raw [16]byte
	ms := uint64(at.UnixMilli())
	for i := 5; i >= 0; i-- {
		raw[i] = byte(ms)
		ms >>= 8
	}
	fill := b.Rand
	if fill == nil {
		fill = rand.Read
	}
	if _, err := fill(raw[6:]); err != nil {
		return "", errors.New("no random bytes for a message id: " + err.Error())
	}
	us := uint16(at.Nanosecond()/1000%1000) << 6 // ten bits, at the top of the random half
	raw[6], raw[7] = byte(us>>8), byte(us)|raw[7]&0x3f
	const alphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"
	var out [26]byte
	// 128 bits into 26 five-bit groups, the first group holding the top 3 bits.
	var acc uint64
	bits, j := 0, 25
	for i := 15; i >= 0; i-- {
		acc |= uint64(raw[i]) << bits
		bits += 8
		for bits >= 5 && j >= 0 {
			out[j] = alphabet[acc&31]
			acc >>= 5
			bits -= 5
			j--
		}
	}
	for j >= 0 {
		out[j] = alphabet[acc&31]
		acc >>= 5
		j--
	}
	return string(out[:]), nil
}
