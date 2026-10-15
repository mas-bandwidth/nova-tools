package bus

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
)

// An ask (SPEC-BUS.md, asks) is a message of kind ask: it wants an answer by
// a deadline the store stamps from its own clock (Message.Due, the send's at
// plus the sender's wait). Never wait for ever: an ask without a deadline is
// refused. It is answered only by a message from one of its recipients whose
// re names it, at the store's time; past its deadline unanswered it times
// out, once, and the bus delivers the sender one message of kind timeout
// naming it (re), which is what wakes the sender's session. Every time here is
// the store's (TIME), never a client's.
//
// The records are hashes beside the streams, written in the send's own
// transaction (marks), never deleted:
//
//	bus2:ask:<id>    from, to, subject, at, due (Unix seconds); timedout and
//	                 timeout (the timeout's at and message id) once it timed out
//	bus2:asks:<from> field the ask's id, value its due: the sender's asks
//	bus2:answers:<name> field a message id the name replied to (re), value
//	                 its first reply's at: written by every reply, ask or not,
//	                 as a receipt is, and read only for an ask's recipients
//	bus2:asktimeout:<id> the once-key of the timeout (AddOnce): one delivery
const (
	AskPrefix        = "bus2:ask:"
	AsksPrefix       = "bus2:asks:"
	AskTimeoutPrefix = "bus2:asktimeout:"
	AnswersPrefix    = "bus2:answers:"
)

// AskOf is the ask's record; AsksOf the sender's asks.
func AskOf(id string) string    { return AskPrefix + id }
func AsksOf(from string) string { return AsksPrefix + from }

// AnswersOf is the name's replies, by the id each names.
func AnswersOf(name string) string { return AnswersPrefix + name }

// TimeoutFrom is the from of a timeout: the bus's own name, never the
// sender's, since a wait skips the messages from the one waiting.
const TimeoutFrom = "nova-bus"

// TimeoutKeep is how long the once-key of a timeout lives; past it the ask's
// own timedout field keeps the expiry from delivering again.
const TimeoutKeep = 30 * 24 * time.Hour

// The states of an ask.
const (
	AskOpen     = "open"
	AskAnswered = "answered"
	AskTimedOut = "timedout"
)

// checkAsk is what is wrong with m's kind and wait.
func checkAsk(m Message) []string {
	switch {
	case m.Kind == KindTimeout:
		return []string{"the kind timeout is the bus's own, sent when an ask goes unanswered; a sender cannot send one"}
	case m.Kind == KindAsk && m.Wait <= 0:
		return []string{"an ask wants a deadline above zero (--deadline <duration>): never wait for ever"}
	case m.Kind != KindAsk && m.Wait != 0:
		return []string{"a deadline is for an ask: --deadline wants --kind ask"}
	}
	return nil
}

func unix(t time.Time) string { return strconv.FormatInt(t.Unix(), 10) }

func parseUnix(v string) time.Time {
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil || v == "" {
		return time.Time{} // no stamp, or one that is no number: no time, never a refusal
	}
	return time.Unix(n, 0).UTC()
}

// askMarks is the ask's records, written in its send's transaction.
func askMarks(m Message) []Mark {
	if m.Kind != KindAsk {
		return nil
	}
	k := AskOf(m.ID)
	return []Mark{
		{Key: k, Field: "from", Value: m.From},
		{Key: k, Field: "to", Value: strings.Join(slices.Compact(slices.Sorted(slices.Values(slices.Concat(m.To, m.CC)))), ",")},
		{Key: k, Field: "subject", Value: m.Subject},
		{Key: k, Field: "at", Value: unix(m.At)},
		{Key: k, Field: "due", Value: unix(m.Due)},
		{Key: AsksOf(m.From), Field: m.ID, Value: unix(m.Due)},
	}
}

// answerMark is m's reply to the message its re names, on the sender's
// answers hash, the first reply by the store's time standing (HSETNX), in the
// send's own transaction, so a send stays two trips: whether re is an ask,
// and m's sender one of its recipients, is decided when the ask is read.
func answerMark(m Message) []Mark {
	if m.Re == "" {
		return nil
	}
	return []Mark{{Key: AnswersOf(m.From), Field: m.Re, Value: unix(m.At), Once: true}}
}

// Ask is one ask as its record holds it.
type Ask struct {
	ID, From, Subject string
	To                []string
	At, Due           time.Time
	// AnsweredBy and AnsweredAt are the first recipient's reply by the
	// store's time, in time or late; empty when none replied.
	AnsweredBy string
	AnsweredAt time.Time
	// Answers is every recipient's reply, by name, at the store's time.
	Answers map[string]time.Time
	// TimedOutAt and TimeoutID are the timeout the bus delivered; zero when
	// it has not (yet) been delivered.
	TimedOutAt time.Time
	TimeoutID  string
}

// State is the ask's state at now: answered when a recipient replied by its
// deadline, timedout once now reaches the deadline without one (whether or
// not the timeout is delivered yet), else open.
func (a Ask) State(now time.Time) string {
	switch {
	case a.AnsweredBy != "" && !a.AnsweredAt.After(a.Due):
		return AskAnswered
	case !a.TimedOutAt.IsZero() || !now.Before(a.Due):
		return AskTimedOut
	}
	return AskOpen
}

func parseAsk(id string, h map[string]string) Ask {
	return Ask{ID: id, From: h["from"], Subject: h["subject"], To: list(h["to"]),
		At: parseUnix(h["at"]), Due: parseUnix(h["due"]), TimedOutAt: parseUnix(h["timedout"]), TimeoutID: h["timeout"]}
}

// answer records n's reply at at on a: every one in Answers, the first by
// the store's time (then by name) as AnsweredBy.
func (a *Ask) answer(n string, at time.Time) {
	if a.Answers == nil {
		a.Answers = map[string]time.Time{}
	}
	a.Answers[n] = at
	if a.AnsweredBy == "" || at.Before(a.AnsweredAt) || at.Equal(a.AnsweredAt) && n < a.AnsweredBy {
		a.AnsweredBy, a.AnsweredAt = n, at
	}
}

// Asks is the asks from sends, oldest first, and the store's time: two trips.
func (b *Bus) Asks(ctx context.Context, from string) ([]Ask, time.Time, error) {
	if p := CheckName(from); p != "" {
		return nil, time.Time{}, &Refusal{[]string{p}}
	}
	_, now, err := b.Store.Roster(ctx)
	if err != nil {
		return nil, time.Time{}, err
	}
	asks, err := b.asksOf(ctx, from)
	return asks, now, err
}

// asksOf is the asks of the senders, oldest first, in two trips.
func (b *Bus) asksOf(ctx context.Context, senders ...string) ([]Ask, error) {
	keys := make([]string, len(senders))
	for i, s := range senders {
		keys[i] = AsksOf(s)
	}
	idx, err := b.Store.Marks(ctx, keys...)
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, h := range idx {
		for id := range h {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return nil, nil
	}
	slices.Sort(ids) // a ULID sorts by the store's time
	recs := make([]string, len(ids))
	for i, id := range ids {
		recs[i] = AskOf(id)
	}
	hs, err := b.Store.Marks(ctx, recs...)
	if err != nil {
		return nil, err
	}
	out := make([]Ask, 0, len(ids))
	var asked []string
	for i, id := range ids {
		if a := parseAsk(id, hs[i]); a.From != "" {
			out = append(out, a)
			asked = append(asked, a.To...)
		}
	}
	// the replies: one hash per name asked, read once (a third trip)
	asked = slices.Compact(slices.Sorted(slices.Values(asked)))
	if len(asked) == 0 {
		return out, nil
	}
	keys = make([]string, len(asked))
	for i, n := range asked {
		keys[i] = AnswersOf(n)
	}
	ans, err := b.Store.Marks(ctx, keys...)
	if err != nil {
		return nil, err
	}
	for i := range out {
		for _, n := range out[i].To {
			if v, ok := ans[slices.Index(asked, n)][out[i].ID]; ok {
				out[i].answer(n, parseUnix(v))
			}
		}
	}
	return out, nil
}

// Expired is one timeout an expiry delivered: the ask, and the message.
type Expired struct {
	Ask       Ask
	MessageID string
}

// ExpireAsks is the expiry (SPEC-BUS.md, asks): every ask of every known
// sender past its deadline with no answer by it and no timeout yet gets its
// timeout, one message to its sender, at most once however many expiries run
// (the once-key, AddOnce). It answers the timeouts this call delivered. The
// alarm the coordinator's loop runs (nova-bus overdue) calls it, and any loop
// with a Bus may: it reads the store's time, never a clock of its own.
func (b *Bus) ExpireAsks(ctx context.Context) ([]Expired, error) {
	due, friends, now, err := b.dueAsks(ctx)
	if err != nil {
		return nil, err
	}
	var out []Expired
	for _, a := range due {
		id, delivered, err := b.timeOut(ctx, a, now, friends)
		if err != nil {
			return out, err
		}
		if delivered {
			out = append(out, Expired{Ask: a, MessageID: id})
		}
	}
	return out, nil
}

// DueAsks is the plan of an expiry, read and never written: every ask of
// every known sender ExpireAsks would time out now, by the store's time
// (nova-bus overdue --dry-run). The selection is ExpireAsks' own (dueAsks); an
// ask another expiry times out first is still listed, as it is due.
func (b *Bus) DueAsks(ctx context.Context) ([]Ask, error) {
	due, _, _, err := b.dueAsks(ctx)
	return due, err
}

// dueAsks is every ask of every known sender past its deadline with no answer
// by it and no timeout yet, oldest first, with the friends and the store's now
// the expiry needs.
func (b *Bus) dueAsks(ctx context.Context) (due []Ask, friends []string, now time.Time, err error) {
	friends, machines, now, err := b.Store.Members(ctx)
	if err != nil {
		return nil, nil, time.Time{}, err
	}
	names := slices.Compact(slices.Sorted(slices.Values(slices.Concat(friends, machines))))
	asks, err := b.asksOf(ctx, names...)
	if err != nil {
		return nil, nil, time.Time{}, err
	}
	for _, a := range asks {
		if a.State(now) == AskTimedOut && a.TimedOutAt.IsZero() {
			due = append(due, a)
		}
	}
	return due, friends, now, nil
}

// TimeoutSubject is the subject of a's timeout at now.
func TimeoutSubject(a Ask, now time.Time) string {
	return fmt.Sprintf("ASK TIMEOUT %s %s after %s: %s", a.ID, strings.Join(a.To, ","), now.Sub(a.At).Round(time.Second), a.Subject)
}

// timeOut delivers a's timeout at now under its once-key, and says whether
// this call delivered it: false when another expiry already had.
func (b *Bus) timeOut(ctx context.Context, a Ask, now time.Time, friends []string) (string, bool, error) {
	m := Message{From: TimeoutFrom, To: []string{a.From}, Kind: KindTimeout, Re: a.ID, At: now.UTC().Truncate(time.Second),
		Subject: TimeoutSubject(a, now),
		Body: fmt.Sprintf("no one of %s answered the ask %s (sent %s) with --re %s by its deadline %s, by the store's clock. "+
			"nova-bus asks --as %s says where each ask stands; nova-bus receipts names what was delivered.\n",
			strings.Join(a.To, ","), a.ID, a.At.Format(time.RFC3339), a.ID, a.Due.Format(time.RFC3339), a.From)}
	var err error
	if m.ID, err = b.ulid(now); err != nil {
		return "", false, err
	}
	marks := []Mark{{Key: AskOf(a.ID), Field: "timedout", Value: unix(m.At)}, {Key: AskOf(a.ID), Field: "timeout", Value: m.ID}}
	if slices.Contains(friends, a.From) {
		marks = append(marks, Mark{Key: OwedOf(a.From), Field: m.ID, Value: m.At.Format(time.RFC3339)})
	}
	_, found, err := b.Store.AddOnce(ctx, AskTimeoutPrefix+a.ID, m.ID, TimeoutKeep, []string{StreamOf(a.From), LogKey}, m.Fields(), marks...)
	if err != nil || found {
		return "", false, err
	}
	return m.ID, true, nil
}

// The liveness of a name from the asks to it (SPEC-BUS.md, asks).
const (
	LiveUp        = "up"        // answered its newest decided ask by the deadline
	LiveDeaf      = "deaf"      // took its newest decided ask (delivered) and let it time out
	LiveUnreached = "unreached" // never took its newest decided ask before it timed out
	LiveDown      = "down"      // no ask to it decided in the window: nothing heard
)

// Live is one name's liveness and the ask that decided it ("" for down).
type Live struct {
	Name, State, AskID string
	At                 time.Time // the deciding ask's at
}

// LivenessOf is each name's liveness at now, a pure function: the newest ask
// to it sent within window whose outcome is known by now (answered, or past
// its deadline) decides. answered by the name by the deadline is up; else
// delivered to it (delivered[name][id], its receipt) is deaf, else unreached;
// none is down. An ask still open says nothing yet.
func LivenessOf(asks []Ask, delivered map[string]map[string]bool, names []string, now time.Time, window time.Duration) []Live {
	out := make([]Live, len(names))
	for i, n := range names {
		out[i] = Live{Name: n, State: LiveDown}
		var best *Ask
		for j := range asks {
			a := &asks[j]
			if !slices.Contains(a.To, n) || now.Sub(a.At) > window || a.At.After(now) {
				continue
			}
			answered := a.answeredBy(n)
			if !answered && now.Before(a.Due) {
				continue // still open: no word yet
			}
			if best == nil || a.At.After(best.At) || a.At.Equal(best.At) && a.ID > best.ID {
				best = a
			}
		}
		if best == nil {
			continue
		}
		state := LiveUnreached
		switch {
		case best.answeredBy(n):
			state = LiveUp
		case delivered[n][best.ID]:
			state = LiveDeaf
		}
		out[i] = Live{Name: n, State: state, AskID: best.ID, At: best.At}
	}
	return out
}

// answeredBy is whether n answered a by its deadline.
func (a Ask) answeredBy(n string) bool {
	at, ok := a.Answers[n]
	if !ok && a.AnsweredBy == n {
		at, ok = a.AnsweredAt, true
	}
	return ok && !at.After(a.Due)
}

// Liveness is LivenessOf over the store: every friend, the asks of every
// known sender, and each friend's receipts, at the store's time.
func (b *Bus) Liveness(ctx context.Context, window time.Duration) ([]Live, time.Time, error) {
	friends, machines, now, err := b.Store.Members(ctx)
	if err != nil {
		return nil, time.Time{}, err
	}
	names := slices.Compact(slices.Sorted(slices.Values(slices.Concat(friends, machines))))
	asks, err := b.asksOf(ctx, names...)
	if err != nil {
		return nil, time.Time{}, err
	}
	friends = slices.Compact(slices.Sorted(slices.Values(friends)))
	keys := make([]string, len(friends))
	for i, f := range friends {
		keys[i] = StagesOf(f)
	}
	delivered := map[string]map[string]bool{}
	if len(keys) > 0 {
		hs, err := b.Store.Marks(ctx, keys...)
		if err != nil {
			return nil, time.Time{}, err
		}
		for i, f := range friends {
			delivered[f] = map[string]bool{}
			for id := range hs[i] {
				delivered[f][id] = true
			}
		}
	}
	return LivenessOf(asks, delivered, friends, now, window), now, nil
}
