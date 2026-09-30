// Package verbs is nova-sprint's layer 7 driver: the one way a verb reads,
// plans, builds and sends its step (the upper design, EVENT-DRIVEN-TICK
// version 2.1, section 1.5; item IT18), and the machine's own verbs (init,
// init --coordinator, start, stop, clear, goal).
//
// A verb in one step is Do: one atomic read of what its plan names (1.5.1),
// a pure plan over that read, the step builder's check that the step is one
// step inside the bounds (1.3.6, IT04), and one call of the write path
// (IT12). A race refusal is planned again on a fresh read, at most five times;
// any other refusal is returned at once (1.5.3, 1.3.5). A verb in parts is
// Parts: each part one atomic step with its own op identity <op>/p<k>, its
// continuation in its receipt's caller result, resumed by Layer 1's done
// (1.5.4; L1 5).
//
// Before gate G0 nothing here runs against a store: the tests drive the
// composed twin (sprintfn.Twin over tset.Mem) through the same Client the
// store's binding implements.
package verbs

import (
	"context"
	"crypto/rand"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	mathrand "math/rand/v2"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/sprintfn"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/stepbuild"
	"github.com/mas-bandwidth/nova-tools/internal/tset"
)

// Retries is how many times a step refused on a race is planned again on a
// fresh read (1.5.3: "up to 5 times with jitter").
const Retries = 5

// StepChunk is a part's size in changed members when the verb names none: L1
// 6's admission ceiling (1.0, "The chunk").
const StepChunk = 2000

// DoneWindow is how many part identities one done query asks (1.5.4: "up to
// 2,000 a query"; L1 7).
const DoneWindow = 2000

// retryJitterMS is the most a retry waits, per retry already made, before it
// reads again: a short random wait, so that two verbs that raced do not race
// again in step (1.5.3).
const retryJitterMS = 5

// Env is what every verb runs with: the write path's client, the
// deployment's names, the actor X checks for a coordinator's verb, and the
// epoch the verb plans at (AL2). Do and Parts move Epoch to the active epoch
// when a read or a refusal shows it moved (STALE, EPOCHAHEAD).
type Env struct {
	C      sprintfn.Client
	Names  sprint.Names
	Actor  string
	Epoch  uint64
	mu     sync.Mutex // guards Epoch against two verbs of one Env at once
	noWait bool       // tests: retry without the jitter's wait
}

func (e *Env) epoch() uint64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.Epoch
}

func (e *Env) setEpoch(n uint64) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.Epoch = n
}

// dec is an epoch as the wire writes it.
func dec(n uint64) tset.Decimal { return tset.Decimal(strconv.FormatUint(n, 10)) }

// undec is a wire epoch as a number; false when it is not one.
func undec(d tset.Decimal) (uint64, bool) {
	n, err := strconv.ParseUint(string(d), 10, 64)
	return n, err == nil && tset.ValidDecimal(d)
}

// Planned is a verb of one step (1.5.3): the read its plan names, and the
// plan, a pure function of that read. Op and Args make the step's identity
// when the caller gave --op: a repeat with the same op and arguments returns
// the recorded result, and other arguments are refused (L1 5; 3, "--op").
type Planned struct {
	// Verb is the verb's name, the step's Meta.Verb.
	Verb string
	// Op is the caller's --op, "" for none.
	Op string
	// Args are the verb's semantic arguments, the intent's (L1 5): every flag
	// the caller gave, never an observation.
	Args map[string]any
	// Read is the read of the plan at an epoch; nil reads nothing (a verb of
	// one round trip).
	Read func(epoch tset.Decimal) *sprintfn.ReadRequest
	// Plan is the step from the read (nil when Read is nil). A nil request is
	// a verb with nothing to write; a *Refused error refuses it before any
	// write is sent.
	Plan func(rd *sprintfn.ReadReply) (*sprintfn.Request, error)
}

// Result is what a verb did.
type Result struct {
	Verb string
	// Op is the op identity the verb ran under, "" for none; for a verb in
	// parts, part k's identity is Op + "/p" + k.
	Op string
	// Epoch is the epoch the verb's last step was planned at, and EpochAfter
	// the epoch after it (one more after a clear).
	Epoch, EpochAfter uint64
	// Step is the last step's reply, nil for a verb that wrote nothing.
	Step *sprintfn.StepReply
	// Read is the last read's answer (a verb that only reads returns it).
	Read *sprintfn.ReadReply
	// Replay says the step was a repeat of an applied op: nothing was written
	// and Recorded is the result the receipt kept.
	Replay   bool
	Recorded string
	// Parts is how many parts this run applied, and Resumed how many a
	// resume found applied before it (1.5.4).
	Parts, Resumed int
	// Chunk is the part size the last part ran at (halved after a LIMIT).
	Chunk int
	// Retries is how many times a step was planned again after a race, and
	// Trips the round trips the verb made (1.5.3: two, n + 1 in parts).
	Retries, Trips int
	// Said is the verb's one line.
	Said string
}

// Refused is a verb refused: nothing of its step (or of its part) was
// written. Local says the verb refused itself from its read before sending
// anything.
type Refused struct {
	Verb    string
	Refusal *sprintfn.Refusal
	Retries int
	Op      string
	Part    int // the part refused; 0 for a verb of one step
	Local   bool
	Hint    string // how to go on
}

// Error is the refusal in one line.
func (r *Refused) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s refused", r.Verb)
	if r.Part > 0 {
		fmt.Fprintf(&b, " at part %d of op %s", r.Part, r.Op)
	}
	if r.Refusal != nil {
		fmt.Fprintf(&b, ": %s", r.Refusal.Code)
		if m := r.Refusal.Message; m != "" && m != r.Refusal.Code {
			fmt.Fprintf(&b, " (%s)", m)
		}
	}
	if r.Retries > 0 {
		fmt.Fprintf(&b, " after %d retries", r.Retries)
	}
	if r.Hint != "" {
		b.WriteString("; " + r.Hint)
	}
	return b.String()
}

// Code is the refusal's code, "" when there is none.
func (r *Refused) Code() string {
	if r == nil || r.Refusal == nil {
		return ""
	}
	return r.Refusal.Code
}

// refuseLocal is a refusal the verb makes from its read, before any write.
func refuseLocal(verb, code, format string, args ...any) *Refused {
	msg := fmt.Sprintf(format, args...)
	return &Refused{Verb: verb, Local: true,
		Refusal: &sprintfn.Refusal{Code: code, Message: msg + "; nothing was changed",
			Detail: sprintfn.RefusalDetail{RefusalDetail: tset.RefusalDetail{IDs: []string{}, Cells: []string{}, Rows: []string{}}}}}
}

// Unknown is a step whose reply was lost (L1 8, OUTCOMEUNKNOWN): whether it
// applied is not known. The same command with --op <Op> settles it by done
// (1.5.4); a verb run without an op names what it did.
type Unknown struct {
	Verb string
	Op   string
	Part int
	Err  error
}

// Error says the outcome is unknown and how to settle it.
func (u *Unknown) Error() string {
	if u.Op == "" {
		return fmt.Sprintf("%s: the outcome is unknown (%v): read the sprint before running it again", u.Verb, u.Err)
	}
	if u.Part > 0 {
		return fmt.Sprintf("%s: part %d's outcome is unknown (%v): run the same command with --op %s", u.Verb, u.Part, u.Err, u.Op)
	}
	return fmt.Sprintf("%s: the outcome is unknown (%v): run the same command with --op %s", u.Verb, u.Err, u.Op)
}

// Unwrap is the cause, so errors.Is finds tset.ErrOutcomeUnknown.
func (u *Unknown) Unwrap() error { return u.Err }

// raceCodes are the refusals of a race (1.0, "The sprint's refusal codes"):
// a step planned on a read that moved. EXISTS is the create of a derived id
// (a concurrent rework or ask --another), NOROW a guard over the stream set
// (a concurrent remove). ROWSET is added beside the design's list: AL3's
// guard on the rows as read, which a clear carries (L1 3), refuses a
// concurrent row change with it.
var raceCodes = map[string]bool{
	"PLACE": true, "REVISION": true, "CELLFULL": true, "RANGECOUNT": true,
	sprintfn.CodeStaleGen: true, sprintfn.CodeIngestAt: true, sprintfn.CodeCounter: true,
	sprintfn.CodeDropping: true, sprintfn.CodeStopped: true, sprintfn.CodeXGuard: true,
	sprintfn.CodeStale: true, sprintfn.CodeEpochAhead: true, "EXISTS": true, "NOROW": true,
	"ROWSET": true,
}

// IsRace says a refusal code is a race, which a verb plans again on a fresh
// read (1.5.3); every other code is returned as it is (1.3.5).
func IsRace(code string) bool { return raceCodes[code] }

// epochMoved says a refusal is the epoch's: the caller reloads it (1.0).
func epochMoved(code string) bool {
	return code == sprintfn.CodeStale || code == sprintfn.CodeEpochAhead
}

// Intent is the canonical encoding of a verb's step or part (L1 5, 1.5.4):
// the verb, the deployment, the epoch, the part and the semantic arguments,
// as JSON with its object keys sorted. Arguments are strings, integers,
// booleans or lists of strings; a list of named ids is replaced by IDsDigest
// by the caller, so the intent stays under 64 KiB.
func Intent(names sprint.Names, epoch tset.Decimal, verb string, part int, args map[string]any) (string, error) {
	m := map[string]any{"v": 1, "verb": verb, "prefix": names.Prefix, "epoch": string(epoch), "part": part}
	if len(args) != 0 {
		m["args"] = args
	}
	b, err := json.Marshal(m) // encoding/json sorts map keys
	if err != nil {
		return "", err
	}
	if len(b) > tset.MaxIntentBytes {
		return "", fmt.Errorf("the intent is %d bytes, over %d: digest the id lists (IDsDigest)", len(b), tset.MaxIntentBytes)
	}
	return string(b), nil
}

// IDsDigest is a list of named ids as an intent carries it (1.5.4): the SHA-1
// of the sorted ids, one a line. SHA-1 is the digest Layer 1's receipts use.
func IDsDigest(sorted []string) string {
	sum := sha1.Sum([]byte(strings.Join(sorted, "\n")))
	return "sha1:" + hex.EncodeToString(sum[:])
}

// digest is an intent's digest as done compares it (L1 5: SHA1 of the intent
// bytes, in hex).
func digest(intent string) string {
	sum := sha1.Sum([]byte(intent))
	return hex.EncodeToString(sum[:])
}

// validOp holds a caller's op to what a part identity needs: text of at most
// 200 bytes (so <op>/p<k> stays inside a 256-byte identifier), with no '~'
// (an epoch's own, OpFamily) and no '/' (the part's own).
func validOp(op string) bool {
	return op != "" && len(op) <= 200 && utf8.ValidString(op) && !strings.ContainsAny(op, "~/\x00\r\n\t ")
}

// NewOp is an op the verb makes when the caller gave none, printed so that
// the same command with --op resumes it (1.5.4).
func NewOp() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err) // crypto/rand does not fail on a supported platform
	}
	return "op-" + hex.EncodeToString(b[:])
}

// wait is the jitter before a retry (1.5.3), bounded by retryJitterMS a retry
// made so far; it returns early when the context ends.
func (e *Env) wait(ctx context.Context, retries int) error {
	if e.noWait {
		return ctx.Err()
	}
	d := time.Duration(mathrand.IntN(retryJitterMS*retries+1)) * time.Millisecond
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// Do runs a verb of one step (1.5.3): read, plan, build, send. A race is
// planned again on a fresh read at most Retries times, the epoch reloaded
// first after STALE or EPOCHAHEAD; any other refusal is returned at once, not
// retried (1.3.5). With an op, the first read also asks done for it, so a
// repeat of an applied op returns its recorded result with no step (L1 5), and
// the op's intent is its arguments at the epoch the step is planned at. Two
// round trips, one more for each retry and each reload.
func (e *Env) Do(ctx context.Context, p Planned) (Result, error) {
	res := Result{Verb: p.Verb, Op: p.Op}
	if p.Op != "" && !validOp(p.Op) {
		return res, refuseLocal(p.Verb, sprintfn.CodeRequest, "--op %q is not an op (text of at most 200 bytes, no blank, '~' or '/')", p.Op)
	}
	askDone := p.Op != ""
	for {
		epoch := dec(e.epoch())
		res.Epoch = e.epoch()
		var rd *sprintfn.ReadReply
		var intent string
		if p.Op != "" {
			var err error
			if intent, err = Intent(e.Names, epoch, p.Verb, 0, p.Args); err != nil {
				return res, refuseLocal(p.Verb, sprintfn.CodeRequest, "%v", err)
			}
		}
		rr := &sprintfn.ReadRequest{Epoch: epoch}
		if p.Read != nil {
			if q := p.Read(epoch); q != nil {
				rr = q
				rr.Epoch = epoch
			}
		}
		doneAt := -1
		if askDone {
			rr.Tset = append(append([]tset.ReadQuery(nil), rr.Tset...), tset.ReadQuery{Kind: "done",
				Ops: []tset.DoneIdentity{{Epoch: epoch, Op: p.Op, IntentDigest: digest(intent)}}})
			doneAt = len(rr.Tset) - 1
		}
		if len(rr.Tset)+len(rr.Sprint) != 0 {
			r, err := sprintfn.Read(ctx, e.C, rr)
			res.Trips++
			if err != nil {
				return res, err
			}
			if r.Err != nil {
				return res, r.Err
			}
			if ref := r.Refusal; ref != nil {
				if epochMoved(ref.Code) && res.Retries < Retries && e.reload(ref) {
					res.Retries++
					continue
				}
				return res, &Refused{Verb: p.Verb, Refusal: ref, Retries: res.Retries, Op: p.Op}
			}
			rd = r.Read
			// The op's receipt first: it lives at the epoch the op ran at
			// (L1 5), which a clear it made has moved past.
			if doneAt >= 0 {
				slot := rd.Tset[doneAt]
				rd.Tset = rd.Tset[:doneAt]
				if len(slot.Done) == 1 {
					switch s := slot.Done[0]; s.Status {
					case "match":
						res.Replay, res.Read = true, rd
						if s.Receipt != nil {
							res.Recorded = s.Receipt.Result
							if after, ok := undec(s.Receipt.EpochAfter); ok {
								res.EpochAfter = after
							}
						}
						res.Said = fmt.Sprintf("%s: op %s already applied; nothing was written", p.Verb, p.Op)
						return res, nil
					case "conflict":
						return res, refuseLocal(p.Verb, "OPCONFLICT", "op %s was applied with other arguments", p.Op)
					}
				}
			}
			// AL2: the read names the active epoch; a verb plans at it.
			if active, ok := undec(rd.ActiveEpoch); ok && active != e.epoch() {
				if res.Retries >= Retries {
					return res, &Refused{Verb: p.Verb, Retries: res.Retries, Op: p.Op,
						Refusal: &sprintfn.Refusal{Code: sprintfn.CodeStale, Message: "the epoch kept moving under the verb"}}
				}
				res.Retries++
				e.setEpoch(active)
				askDone = false
				continue
			}
		}
		res.Read = rd
		if p.Plan == nil {
			return res, nil
		}
		req, err := p.Plan(rd)
		if err != nil {
			var rf *Refused
			if errors.As(err, &rf) && rf.Verb == "" {
				rf.Verb = p.Verb
			}
			return res, err
		}
		if req == nil {
			return res, nil
		}
		e.fill(req, p.Verb, epoch)
		if p.Op != "" {
			result := ""
			if req.Body.Op != nil {
				result = req.Body.Op.Result
			}
			req.Body.Op = &sprintfn.Op{ID: p.Op, Intent: intent, Result: result}
		}
		if rf := build(p.Verb, req); rf != nil {
			return res, rf
		}
		out, err := sprintfn.Step(ctx, e.C, req)
		res.Trips++
		if err != nil {
			return res, e.unknown(p.Verb, p.Op, 0, err)
		}
		if out.Err != nil {
			return res, e.unknown(p.Verb, p.Op, 0, out.Err)
		}
		if ref := out.Refusal; ref != nil {
			if IsRace(ref.Code) && res.Retries < Retries {
				res.Retries++
				if epochMoved(ref.Code) {
					e.reload(ref)
				}
				if err := e.wait(ctx, res.Retries); err != nil {
					return res, err
				}
				askDone = false // no receipt is written by a refused step
				continue
			}
			rf := &Refused{Verb: p.Verb, Refusal: ref, Retries: res.Retries, Op: p.Op}
			if IsRace(ref.Code) {
				rf.Hint = fmt.Sprintf("the sprint kept moving under the verb for %d retries; run it again", Retries)
			}
			return res, rf
		}
		res.Step = out.Step
		res.Replay = out.Step.Reply.Replay
		res.Recorded = out.Step.Reply.Result
		if after, ok := undec(out.Step.Reply.EpochAfter); ok {
			res.EpochAfter = after
			if after > e.epoch() {
				e.setEpoch(after)
			}
		}
		return res, nil
	}
}

// fill sets what every request of a verb carries: its epoch, the verb and the
// actor (X's NOTCOORD reads Meta.Actor, 1.5.3).
func (e *Env) fill(req *sprintfn.Request, verb string, epoch tset.Decimal) {
	req.Epoch = epoch
	if req.Meta.Verb == "" {
		req.Meta.Verb = verb
	}
	if req.Meta.Actor == "" {
		req.Meta.Actor = e.Actor
	}
}

// reload moves the epoch to the one a refusal names (STALE, EPOCHAHEAD: the
// caller reloads the epoch, 1.0), and says whether it named one. A refusal
// that names none leaves the epoch, and the next read's active epoch (AL2)
// moves it.
func (e *Env) reload(ref *sprintfn.Refusal) bool {
	if n, ok := undec(ref.Detail.ActiveEpoch); ok {
		e.setEpoch(n)
		return true
	}
	return ref.Code == sprintfn.CodeStale // the next read names the active epoch
}

// unknown is a lost reply (L1 8): the step may have applied.
func (e *Env) unknown(verb, op string, part int, err error) error {
	var ou *sprintfn.OutcomeUnknownError
	if errors.As(err, &ou) || errors.Is(err, tset.ErrOutcomeUnknown) {
		return &Unknown{Verb: verb, Op: op, Part: part, Err: err}
	}
	var ie *sprintfn.ItemError
	if errors.As(err, &ie) {
		return &Refused{Verb: verb, Refusal: ie.Refusal, Op: op, Part: part, Local: true}
	}
	// Any other failure of a dispatched call: nothing says it did not apply.
	return &Unknown{Verb: verb, Op: op, Part: part, Err: err}
}

// build is the step builder's check (1.3.6, IT04): the request's member and
// row entries are cut by stepbuild, and a request the cut makes more than one
// step is refused LIMIT here, before it is sent (V7: every step fits the
// bounds). The step sent is the request's own entries: the builder checks,
// the write path carries. Advance, rowset, count and rcount entries are not
// modelled by the builder (stepbuild's package documentation), and the rows
// of a step that advances are left out too (the builder holds rows to 100, an
// advance allows 1,024, L1 6); Layer 1 bounds them at apply.
func build(verb string, req *sprintfn.Request) *Refused {
	advance := false
	for _, en := range req.Body.Entries {
		if en.Kind == "advance" {
			advance = true
		}
	}
	var in []stepbuild.Entry
	for i, en := range req.Body.Entries {
		sb, ok, err := toBuilder(en)
		if err != nil {
			return refuseLocal(verb, sprintfn.CodeRequest, "entry %d: %v", i, err)
		}
		if !ok || (advance && sb.Kind == stepbuild.KindRows) {
			continue
		}
		in = append(in, sb)
	}
	if len(in) == 0 {
		return nil
	}
	cfg := stepbuild.Config{Epoch: string(req.Epoch)}
	if op := req.Body.Op; op != nil {
		id := stepbuild.Ident{Op: op.ID, Intent: op.Intent, Result: op.Result}
		cfg.Ident = func(int) stepbuild.Ident { return id }
	}
	steps, err := stepbuild.Build(cfg, in)
	switch {
	case errors.Is(err, stepbuild.ErrLimit):
		return refuseLocal(verb, sprintfn.CodeLimit, "%v", err)
	case err != nil:
		return refuseLocal(verb, sprintfn.CodeRequest, "%v", err)
	case len(steps) > 1:
		return refuseLocal(verb, sprintfn.CodeLimit, "the step is past one step's bounds (the builder cuts it in %d); a verb this size goes in parts (1.5.4)", len(steps))
	}
	return nil
}

// toBuilder is one wire entry as the step builder takes it; false for a kind
// the builder does not model.
func toBuilder(en tset.Entry) (stepbuild.Entry, bool, error) {
	var kind stepbuild.Kind
	switch en.Kind {
	case "create":
		kind = stepbuild.KindCreate
	case "move":
		kind = stepbuild.KindMove
	case "remove":
		kind = stepbuild.KindRemove
	case "guard":
		kind = stepbuild.KindGuard
	case "rows":
		kind = stepbuild.KindRows
	default:
		return stepbuild.Entry{}, false, nil
	}
	out := stepbuild.Entry{Kind: kind, Table: en.Table, From: en.From, To: en.To, IDs: en.IDs, Scores: en.Scores,
		Each: en.Each, About: en.About, Set: en.Set, Unset: en.Unset, BeforeFields: en.BeforeFields, Add: en.Add, Del: en.Del}
	if en.Revs != nil {
		out.Revs = make([]string, len(en.Revs))
		for i, r := range en.Revs {
			out.Revs[i] = string(r)
		}
	}
	if len(en.Meta) != 0 {
		if err := json.Unmarshal(en.Meta, &out.Meta); err != nil {
			return out, false, fmt.Errorf("meta is not an object of strings: %w", err)
		}
	}
	return out, true, nil
}

// ---- verbs in parts (1.5.4)

// PartsPlan is a verb in parts: its arguments (the intent's, L1 5), the chunk
// its parts start at, and each part's read and plan from the continuation the
// part before it left. The continuation is the verb's own text, at most what
// a caller result holds with the driver's fields beside it.
type PartsPlan struct {
	Verb string
	Args map[string]any
	// Chunk is the members a part changes at most; 0 is StepChunk. After a
	// LIMIT the part is planned again at half, and every part after it too.
	Chunk int
	// Read is part k's read from the continuation ("" for part 1).
	Read func(epoch tset.Decimal, cont string, chunk int) *sprintfn.ReadRequest
	// Plan is part k's step from its read and the continuation.
	Plan func(rd *sprintfn.ReadReply, cont string, chunk int) (Part, error)
}

// Part is one planned part: its request, the continuation the next part
// starts from, and whether it is the last.
type Part struct {
	Req  *sprintfn.Request
	Next string
	Last bool
}

// partResult is a part's caller result (1.5.4: at most 4 KiB): the part, the
// continuation after it, the chunk the parts run at, and in the last part the
// finished mark (errata 3, H4 cutmark), so a resume of a finished op and the
// cut judgment both know it ended.
type partResult struct {
	Part  int    `json:"k"`
	Next  string `json:"next"`
	Chunk int    `json:"chunk"`
	Fin   bool   `json:"fin,omitempty"`
}

// partID is part k's op identity (1.5.4; L1 5).
func partID(op string, k int) string { return op + "/p" + strconv.Itoa(k) }

// Parts runs a verb in parts (1.5.4). Given an op, it first asks done for the
// op's part identities <op>/p1 onwards (DoneWindow a query), takes the last
// applied part's continuation, and runs the part after it, so a resume
// neither skips nor repeats; a finished op (its last part's finished mark)
// returns at once. Given none, it makes one (NewOp) and prints it. Each part
// is one atomic step with its own identity and an intent of the verb's
// arguments and the part's index; its step is pipelined with the next part's
// read, so n parts cost n + 1 round trips (a resume one more, for done). A
// race is planned again on a fresh read, at most Retries times a part; a
// LIMIT plans the part again at half the chunk, and the parts after it keep
// the half. Any other refusal ends the op: the parts before stay applied and
// the refusal says how to go on.
func (e *Env) Parts(ctx context.Context, op string, pp PartsPlan) (Result, error) {
	res := Result{Verb: pp.Verb}
	resume := op != ""
	if !resume {
		op = NewOp()
	}
	res.Op = op
	if !validOp(op) {
		return res, refuseLocal(pp.Verb, sprintfn.CodeRequest, "--op %q is not an op (text of at most 200 bytes, no blank, '~' or '/')", op)
	}
	chunk := pp.Chunk
	if chunk <= 0 || chunk > StepChunk {
		chunk = StepChunk
	}
	epoch := dec(e.epoch())
	res.Epoch = e.epoch()
	intentOf := func(k int) (string, error) { return Intent(e.Names, epoch, pp.Verb, k, pp.Args) }
	k, cont := 1, ""
	if resume {
		last, rec, err := e.resumeAt(ctx, &res, pp.Verb, op, epoch, intentOf)
		if err != nil {
			return res, err
		}
		if last > 0 {
			res.Resumed = last
			if rec.Fin {
				res.Chunk = rec.Chunk
				res.Replay = true
				res.Said = fmt.Sprintf("%s: op %s finished at part %d; nothing was written", pp.Verb, op, last)
				return res, nil
			}
			k, cont = last+1, rec.Next
			if rec.Chunk > 0 && rec.Chunk < chunk {
				chunk = rec.Chunk
			}
		}
	}
	var rd *sprintfn.ReadReply // the read of part k, when a pipelined flush brought it
	retries := 0
	for {
		if rd == nil {
			r, err := e.readPart(ctx, &res, pp, epoch, cont, chunk)
			if err != nil {
				return res, err
			}
			rd = r
		}
		// AL2: an op not yet begun plans at the active epoch; one begun stays
		// at its own, where its receipts are (a moved epoch refuses it STALE).
		if active, ok := undec(rd.ActiveEpoch); ok && active != e.epoch() && k == 1 && retries < Retries {
			retries++
			e.setEpoch(active)
			epoch, res.Epoch, rd = dec(active), active, nil
			continue
		}
		part, err := pp.Plan(rd, cont, chunk)
		if err != nil {
			var rf *Refused
			if errors.As(err, &rf) {
				rf.Verb, rf.Op, rf.Part = pp.Verb, op, k
			}
			return res, err
		}
		if part.Req == nil {
			return res, refuseLocal(pp.Verb, sprintfn.CodeRequest, "part %d planned no step", k)
		}
		intent, err := intentOf(k)
		if err != nil {
			return res, refuseLocal(pp.Verb, sprintfn.CodeRequest, "%v", err)
		}
		pr, _ := json.Marshal(partResult{Part: k, Next: part.Next, Chunk: chunk, Fin: part.Last})
		if len(pr) > tset.MaxResultBytes {
			return res, refuseLocal(pp.Verb, sprintfn.CodeLimit, "part %d's continuation is %d bytes, over the caller result's %d", k, len(pr), tset.MaxResultBytes)
		}
		req := part.Req
		e.fill(req, pp.Verb, epoch)
		req.Body.Op = &sprintfn.Op{ID: partID(op, k), Intent: intent, Result: string(pr)}
		if rf := build(pp.Verb, req); rf != nil {
			if rf.Code() == sprintfn.CodeLimit && chunk > 1 {
				chunk /= 2 // cut locally: nothing was sent
				rd = nil
				continue
			}
			rf.Op, rf.Part = op, k
			return res, rf
		}
		items := []sprintfn.Item{{Step: req}}
		if !part.Last {
			items = append(items, sprintfn.Item{Read: e.partRead(pp, epoch, part.Next, chunk)})
		}
		out, err := e.C.Pipeline(ctx, items)
		res.Trips++
		if err != nil {
			return res, e.unknown(pp.Verb, op, k, err)
		}
		if len(out) != len(items) {
			return res, &Unknown{Verb: pp.Verb, Op: op, Part: k, Err: fmt.Errorf("a pipeline of %d returned %d results", len(items), len(out))}
		}
		step := out[0]
		if step.Err != nil {
			return res, e.unknown(pp.Verb, op, k, step.Err)
		}
		if ref := step.Refusal; ref != nil {
			switch {
			case ref.Code == sprintfn.CodeLimit && chunk > 1:
				chunk /= 2 // 1.5.4: the remaining parts at half the chunk
				rd = nil
				continue
			case IsRace(ref.Code) && !epochMoved(ref.Code) && retries < Retries:
				retries++
				res.Retries++
				if err := e.wait(ctx, retries); err != nil {
					return res, err
				}
				rd = nil
				continue
			}
			rf := &Refused{Verb: pp.Verb, Refusal: ref, Retries: retries, Op: op, Part: k,
				Hint: fmt.Sprintf("parts 1 to %d stay applied; fix, then run the same command with --op %s", k-1, op)}
			if epochMoved(ref.Code) {
				rf.Hint = fmt.Sprintf("the sprint's epoch moved (a clear): parts 1 to %d stay applied at epoch %s, and the op cannot go on", k-1, epoch)
			}
			return res, rf
		}
		res.Parts++
		res.Step, res.Chunk = step.Step, chunk
		if part.Last {
			res.Said = fmt.Sprintf("%s: op %s finished in %d parts", pp.Verb, op, k)
			return res, nil
		}
		k, cont, retries = k+1, part.Next, 0
		rd = nil
		switch next := out[1]; {
		case next.Err != nil:
			return res, next.Err
		case next.Refusal != nil:
			return res, &Refused{Verb: pp.Verb, Refusal: next.Refusal, Op: op, Part: k,
				Hint: fmt.Sprintf("parts 1 to %d are applied; run the same command with --op %s", k-1, op)}
		default:
			rd = next.Read
		}
	}
}

// partRead is part k's read; a plan that reads nothing still reads (the
// sprint's clock), so each part plans on one snapshot of the store's time.
func (e *Env) partRead(pp PartsPlan, epoch tset.Decimal, cont string, chunk int) *sprintfn.ReadRequest {
	var rr *sprintfn.ReadRequest
	if pp.Read != nil {
		rr = pp.Read(epoch, cont, chunk)
	}
	if rr == nil || len(rr.Tset)+len(rr.Sprint) == 0 {
		q, ref := sprintfn.EncodeKeyQ(sprintfn.KeyQ{Kind: sprintfn.KeyClock})
		if ref != nil {
			panic(ref) // a fixed, valid query
		}
		rr = &sprintfn.ReadRequest{Sprint: []sprintfn.SprintQuery{q}}
	}
	rr.Epoch = epoch
	return rr
}

// readPart reads part k alone (the first part, and a part planned again).
func (e *Env) readPart(ctx context.Context, res *Result, pp PartsPlan, epoch tset.Decimal, cont string, chunk int) (*sprintfn.ReadReply, error) {
	r, err := sprintfn.Read(ctx, e.C, e.partRead(pp, epoch, cont, chunk))
	res.Trips++
	if err != nil {
		return nil, err
	}
	if r.Err != nil {
		return nil, r.Err
	}
	if r.Refusal != nil {
		return nil, &Refused{Verb: pp.Verb, Refusal: r.Refusal, Op: res.Op}
	}
	return r.Read, nil
}

// resumeAt asks done for the op's parts, DoneWindow at a time (L1 5; 1.5.4),
// and returns the last part applied in order and its recorded result. A
// conflict is the same op with other arguments (OPCONFLICT); a fenced part
// was settled as not applied, and the op cannot go on under its name.
func (e *Env) resumeAt(ctx context.Context, res *Result, verb, op string, epoch tset.Decimal, intentOf func(int) (string, error)) (int, partResult, error) {
	last, rec := 0, partResult{}
	for first := 1; ; first += DoneWindow {
		ids := make([]tset.DoneIdentity, 0, DoneWindow)
		for k := first; k < first+DoneWindow; k++ {
			intent, err := intentOf(k)
			if err != nil {
				return 0, rec, refuseLocal(verb, sprintfn.CodeRequest, "%v", err)
			}
			ids = append(ids, tset.DoneIdentity{Epoch: epoch, Op: partID(op, k), IntentDigest: digest(intent)})
		}
		r, err := sprintfn.Read(ctx, e.C, &sprintfn.ReadRequest{Epoch: epoch, Tset: []tset.ReadQuery{{Kind: "done", Ops: ids}}})
		res.Trips++
		if err != nil {
			return 0, rec, err
		}
		if r.Err != nil {
			return 0, rec, r.Err
		}
		if r.Refusal != nil {
			return 0, rec, &Refused{Verb: verb, Refusal: r.Refusal, Op: op}
		}
		if len(r.Read.Tset) != 1 || len(r.Read.Tset[0].Done) != len(ids) {
			return 0, rec, fmt.Errorf("%s: done answered %d slots for %d parts", verb, len(r.Read.Tset[0].Done), len(ids))
		}
		for i, s := range r.Read.Tset[0].Done {
			k := first + i
			switch s.Status {
			case "absent":
				return last, rec, nil
			case "conflict":
				return 0, rec, refuseLocal(verb, "OPCONFLICT", "op %s part %d was applied with other arguments", op, k)
			case "match":
				if s.Receipt == nil || s.Receipt.Status != "ok" {
					return 0, rec, refuseLocal(verb, "FENCED", "op %s part %d was fenced (settled as not applied); run the verb under a new op", op, k)
				}
				var pr partResult
				if err := json.Unmarshal([]byte(s.Receipt.Result), &pr); err != nil || pr.Part != k {
					return 0, rec, fmt.Errorf("%s: op %s part %d's receipt holds no continuation", verb, op, k)
				}
				last, rec = k, pr
				if pr.Fin {
					return last, rec, nil
				}
			default:
				return 0, rec, fmt.Errorf("%s: done answered %q", verb, s.Status)
			}
		}
	}
}

// ---- reads a verb decodes

// keyQuery is a sprint-key read (IT30), which cannot fail to encode for the
// fixed kinds the verbs name.
func keyQuery(q sprintfn.KeyQ) sprintfn.SprintQuery {
	sq, ref := sprintfn.EncodeKeyQ(q)
	if ref != nil {
		panic(fmt.Sprintf("verbs: a fixed key query refused: %v", ref))
	}
	return sq
}

// clockOf decodes the clock answer at a sprint slot: whether the sprint has a
// clock at all (init writes it), whether it runs, and the answer.
func clockOf(rd *sprintfn.ReadReply, slot int) (sprintfn.ClockResult, bool, error) {
	if rd == nil || slot >= len(rd.Sprint) {
		return sprintfn.ClockResult{}, false, errors.New("verbs: the read has no clock answer")
	}
	qr, err := sprintfn.DecodeResult(sprintfn.KeyClock, rd.Sprint[slot])
	if err != nil {
		return sprintfn.ClockResult{}, false, err
	}
	c, ok := qr.(sprintfn.ClockResult)
	if !ok {
		return sprintfn.ClockResult{}, false, errors.New("verbs: the clock answer is of another kind")
	}
	has := c.Clock.StoppedMS != nil || c.Clock.StoppedSinceMS != nil
	return c, has, nil
}

// running says the clock runs: no stopped_since_ms (1.2).
func running(c sprintfn.ClockResult) bool {
	return c.Clock.StoppedSinceMS == nil || *c.Clock.StoppedSinceMS == ""
}

// ---- a counting client

// Counting counts the round trips a verb makes (1.5.3): each Pipeline call is
// one flush, one round trip. It stands where E8's trip counter stands for the
// store's client, for the twin, which has no connection to count.
type Counting struct {
	C     sprintfn.Client
	mu    sync.Mutex
	trips int
	steps int
}

// Pipeline counts the flush and its steps and passes it on.
func (c *Counting) Pipeline(ctx context.Context, items []sprintfn.Item) ([]sprintfn.Result, error) {
	c.mu.Lock()
	c.trips++
	for _, it := range items {
		if it.Step != nil {
			c.steps++
		}
	}
	c.mu.Unlock()
	return c.C.Pipeline(ctx, items)
}

// Trips is the round trips so far, and Steps the steps sent in them.
func (c *Counting) Trips() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.trips
}

// Steps is the steps sent so far.
func (c *Counting) Steps() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.steps
}

// Reset starts the counts again.
func (c *Counting) Reset() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.trips, c.steps = 0, 0
}
