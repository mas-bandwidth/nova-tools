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
	"crypto/sha256"
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
// epoch the verb plans at (AL2). For a verb with no op of the caller's, Do
// and Parts move Epoch to the active epoch when a read or a refusal shows it
// moved (STALE, EPOCHAHEAD). For an op the caller gave, Epoch is the op's
// epoch (its --epoch, or the active epoch when the command names none), and
// the op never moves from it once it may have run (L1 5); a clear of the op's
// own moves Epoch on.
type Env struct {
	C     sprintfn.Client
	Names sprint.Names
	Actor string
	Epoch uint64
	// FirstEpoch is the sprint's first kept epoch: a repeat's look-back for
	// its op's receipt never asks below it, since Layer 1 refuses a done
	// query that names an epoch whose marker is gone (L1 5 and 7, EPOCHGONE).
	// 0 until a look-back is refused EPOCHGONE, when the driver finds it
	// (learnFirst) and keeps it here; a command that knows it may set it.
	FirstEpoch uint64
	mu         sync.Mutex // guards Epoch and FirstEpoch against two verbs of one Env at once
	noWait     bool       // tests: retry without the jitter's wait
}

func (e *Env) epoch() uint64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.Epoch
}

func (e *Env) firstEpoch() uint64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.FirstEpoch
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

// ID is a card's id as a verb names it in its result.
type ID = string

// RefusedID is a named id a verb refused: its code and why (1.5.3, 1.5.4).
type RefusedID struct {
	ID   ID
	Code string
	Why  string
}

// Planned is a verb of one step (1.5.3): the read its plan names, and the
// plan, a pure function of that read. Op and Args make the step's identity
// when the caller gave --op: a repeat with the same op and arguments returns
// the recorded result, and other arguments are refused (L1 5; 3, "--op").
type Planned struct {
	// Verb is the verb's name, the step's Meta.Verb.
	Verb string
	// Op is the op identity, "" for none.
	Op string
	// Made says the verb made Op itself this run (NewOp): no copy of it was
	// ever sent, so no done is asked for it, it plans at the active epoch
	// when the read shows the epoch moved, and a refusal of its send is
	// final. An op the caller gave is pinned to its epoch (Do).
	Made bool
	// Args are the verb's semantic arguments, the intent's (L1 5): every flag
	// the caller gave, never an observation.
	Args map[string]any
	// Read is the read of the plan at an epoch; nil reads nothing (a verb of
	// one round trip).
	Read func(epoch tset.Decimal) *sprintfn.ReadRequest
	// Plan is the step from the read (nil when Read is nil): Part.Req, nil
	// for a verb with nothing to write, and the ids the step moves and the
	// named ids the plan refused. Part.Next and Part.Last are Parts' and are
	// not read. A *Refused error refuses the verb before any write is sent.
	Plan func(rd *sprintfn.ReadReply) (Part, error)
}

// Result is what a verb did.
type Result struct {
	Verb string
	// Op is the op identity the verb ran under, "" for none; for a verb in
	// parts, part k's identity is Op + "/p" + k.
	Op string
	// Epoch is the op's epoch (the epoch its intent names, and the one a
	// repeat asks done at), and EpochAfter the epoch after the last step (one
	// more after a clear).
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
	// Moved are the ids the applied steps changed; Refused the named ids
	// refused, by the verb's plan or by the store in a refused step or part;
	// NotWritten every other id of a refused step or part (1.5.3: "names its
	// refused ids and every other id of the part as not written"). Each is
	// the total over the parts this run sent, not the last part's.
	Moved      []ID
	Refused    []RefusedID
	NotWritten []ID
	// Notes is how many notes the applied steps asked J for.
	Notes int
	// Said is the verb's one line.
	Said string
}

// applied adds an applied step's or part's ids and notes to the totals.
func (r *Result) applied(p Part) {
	r.Moved = append(r.Moved, p.Moved...)
	r.Refused = append(r.Refused, p.Refused...)
	if p.Req != nil {
		r.Notes += len(p.Req.Body.Notes)
	}
}

// refusedStep adds a refused step's or part's ids: the ids the refusal names
// are refused with its code, and every other id the step would have moved is
// not written (1.5.3). The plan's own named refusals stay refused.
func (r *Result) refusedStep(p Part, ref *sprintfn.Refusal) {
	named := map[string]bool{}
	for _, id := range ref.Detail.IDs {
		if !named[id] {
			named[id] = true
			r.Refused = append(r.Refused, RefusedID{ID: id, Code: ref.Code, Why: ref.Message})
		}
	}
	for _, id := range p.Moved {
		if !named[id] {
			r.NotWritten = append(r.NotWritten, id)
		}
	}
	r.Refused = append(r.Refused, p.Refused...)
}

// Refused is a verb refused: nothing of its step (or of its part) was
// written. Local says the verb refused itself from its read before sending
// anything.
type Refused struct {
	Verb    string
	Refusal *sprintfn.Refusal
	Retries int
	Op      string
	// OpEpoch is the op's epoch, named when the sprint's epoch moved under
	// the op (L1 5) or the op was fenced; 0 otherwise.
	OpEpoch uint64
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

// emptyDetail is Layer 1's detail with its always-present arrays (L1 8).
func emptyDetail() sprintfn.RefusalDetail {
	return sprintfn.RefusalDetail{RefusalDetail: tset.RefusalDetail{IDs: []string{}, Cells: []string{}, Rows: []string{}}}
}

// refuseLocal is a refusal the verb makes from its read, before any write.
func refuseLocal(verb, code, format string, args ...any) *Refused {
	msg := fmt.Sprintf(format, args...)
	return &Refused{Verb: verb, Local: true,
		Refusal: &sprintfn.Refusal{Code: code, Message: msg + "; nothing was changed", Detail: emptyDetail()}}
}

// opMoved is the refusal of an op whose epoch the sprint moved from (L1 5: an
// op never changes epoch; L1 5, "a new stale op never gets that privilege"):
// STALE when the sprint is past the op's epoch, EPOCHAHEAD when the op's
// epoch is past the sprint's. The detail names the active epoch, the refusal
// the op's; parts the op applied before stay applied at its epoch.
func opMoved(verb, op string, part int, opEpoch, active uint64, applied int) *Refused {
	code := sprintfn.CodeStale
	if active < opEpoch {
		code = sprintfn.CodeEpochAhead
	}
	d := emptyDetail()
	d.ActiveEpoch = dec(active)
	rf := &Refused{Verb: verb, Op: op, Part: part, OpEpoch: opEpoch, Local: true,
		Refusal: &sprintfn.Refusal{Code: code, Detail: d,
			Message: fmt.Sprintf("op %s was at epoch %d; the sprint is at %d; nothing was changed", op, opEpoch, active)},
		Hint: fmt.Sprintf("an op never changes epoch: run the verb under a new op at epoch %d", active)}
	if applied > 0 {
		rf.Hint = fmt.Sprintf("parts 1 to %d stay applied at epoch %d, and the op cannot go on: %s", applied, opEpoch, rf.Hint)
	}
	return rf
}

// Unknown is a step whose reply was lost (L1 8, OUTCOMEUNKNOWN): whether it
// applied is not known, and done did not settle it. The same command with
// --op <Op> --epoch <Epoch> settles it (1.5.4; L1 5: the caller retains the
// op's epoch); a verb run without an op names what it did.
type Unknown struct {
	Verb  string
	Op    string
	Epoch uint64 // the op's epoch
	Part  int
	Err   error
}

// Error says the outcome is unknown and how to settle it.
func (u *Unknown) Error() string {
	if u.Op == "" {
		return fmt.Sprintf("%s: the outcome is unknown (%v): read the sprint before running it again", u.Verb, u.Err)
	}
	what := "the outcome"
	if u.Part > 0 {
		what = fmt.Sprintf("part %d's outcome", u.Part)
	}
	return fmt.Sprintf("%s: %s is unknown (%v): run the same command with --op %s --epoch %d", u.Verb, what, u.Err, u.Op, u.Epoch)
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

// IDsDigest is a list of named ids as an intent carries it (1.5.4: "a named
// id list replaced by its SHA256 over the sorted ids"), one id a line.
func IDsDigest(sorted []string) string {
	sum := sha256.Sum256([]byte(strings.Join(sorted, "\n")))
	return "sha256:" + hex.EncodeToString(sum[:])
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

// OpEpochsMax is how many epochs a repeat of an op looks back for its
// receipt: the epoch it runs at and the 63 before it. An op never changes
// epoch, and its receipt stays at the epoch it ran at (L1 5: "original
// epoch ... must be retained by the caller across clear/restart"); a repeat
// given no --epoch runs at the active epoch, so done asks the epochs before
// it too, in the same query (L1 5: "Mixed original epochs are allowed in this
// one query"). An op name reused after 64 clears is not found, and runs as a
// new op. The window assumes the sprint's epochs are kept, as their receipts
// are (L1 5: "retained through clear until authorized teardown").
const OpEpochsMax = 64

// opWindow is one identity per epoch, from hi down to lo (lo <= hi), each
// with its intent at that epoch (the intent names the op's epoch, L1 5).
func opWindow(id string, hi, lo uint64, intentAt func(epoch uint64) (string, error)) ([]tset.DoneIdentity, []uint64, error) {
	var ids []tset.DoneIdentity
	var eps []uint64
	for ep := hi; ; ep-- {
		intent, err := intentAt(ep)
		if err != nil {
			return nil, nil, err
		}
		ids = append(ids, tset.DoneIdentity{Epoch: dec(ep), Op: id, IntentDigest: digest(intent)})
		eps = append(eps, ep)
		if ep == lo || ep == 0 {
			return ids, eps, nil
		}
	}
}

// windowLow is the oldest epoch a window ending at hi asks: OpEpochsMax
// epochs, never below the sprint's first kept epoch (FirstEpoch), nor above hi.
func (e *Env) windowLow(hi uint64) uint64 {
	lo := uint64(0)
	if hi >= OpEpochsMax-1 {
		lo = hi - (OpEpochsMax - 1)
	}
	return min(max(lo, e.firstEpoch()), hi)
}

// codeEpochGone is Layer 1's refusal of a read that names an epoch whose
// marker is gone (L1 7, 9).
const codeEpochGone = "EPOCHGONE"

// zeroDigest is an intent digest no intent has in practice: learnFirst's
// probe asks done for it only to learn whether its epoch is kept.
var zeroDigest = strings.Repeat("0", 40)

// learnFirst finds the sprint's first kept epoch after a look-back from hi
// was refused EPOCHGONE, and keeps it in FirstEpoch. The kept epochs are the
// ones from the first up to the active one (L1 5: receipts and epoch markers
// are retained through clear until teardown; L1 9), so the first is found by
// halving the window, one done probe of a single identity a round trip, at
// most log2(OpEpochsMax) + 1 of them. It says whether the floor rose, so a
// read again can succeed; when hi itself is gone it does not rise, and the
// caller returns the refusal.
func (e *Env) learnFirst(ctx context.Context, res *Result, hi uint64, id string) (bool, error) {
	gone := func(ep uint64) (bool, error) {
		r, err := sprintfn.Read(ctx, e.C, &sprintfn.ReadRequest{Epoch: dec(ep),
			Tset: []tset.ReadQuery{{Kind: "done", Ops: []tset.DoneIdentity{{Epoch: dec(ep), Op: id, IntentDigest: zeroDigest}}}}})
		res.Trips++
		switch {
		case err != nil:
			return false, err
		case r.Err != nil:
			return false, r.Err
		case r.Refusal != nil && r.Refusal.Code == codeEpochGone:
			return true, nil
		case r.Refusal != nil:
			return false, &Refused{Verb: res.Verb, Refusal: r.Refusal, Op: res.Op}
		}
		return false, nil
	}
	lo := e.windowLow(hi)
	if lo >= hi {
		return false, nil
	}
	if g, err := gone(hi); err != nil || g {
		return false, err
	}
	// Invariant: hi is kept, and the refusal says an epoch at lo or above it
	// is gone, so lo is taken as below the first kept epoch.
	for hi-lo > 1 {
		mid := lo + (hi-lo)/2
		g, err := gone(mid)
		if err != nil {
			return false, err
		}
		if g {
			lo = mid
		} else {
			hi = mid
		}
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if hi <= e.FirstEpoch {
		return false, nil
	}
	e.FirstEpoch = hi
	return true, nil
}

// doneOf is the done answer at slot i of a read, checked: a reply that does
// not answer every identity asked is refused CONFIG (the store's function
// answers another shape), never indexed past its end.
func doneOf(verb string, rd *sprintfn.ReadReply, i, want int) ([]tset.DoneSlot, *Refused) {
	if rd == nil || i < 0 || i >= len(rd.Tset) || len(rd.Tset[i].Done) != want {
		got := -1
		if rd != nil && i >= 0 && i < len(rd.Tset) {
			got = len(rd.Tset[i].Done)
		}
		return nil, refuseLocal(verb, sprintfn.CodeConfig, "done answered %d slots for %d identities", got, want)
	}
	return rd.Tset[i].Done, nil
}

// settleDo reads what done said of a verb of one step, epochs aligned with the
// slots: the first match is the op's receipt, a repeat that writes nothing
// (L1 5); a fenced receipt was settled as not applied, and the op cannot run
// again under its name; a conflict is the op run with other arguments.
func settleDo(res *Result, verb, op string, slots []tset.DoneSlot, eps []uint64) (bool, error) {
	for i, s := range slots {
		switch s.Status {
		case "absent":
			continue
		case "conflict":
			return true, refuseLocal(verb, "OPCONFLICT", "op %s was applied at epoch %d with other arguments", op, eps[i])
		case "match":
			if s.Receipt != nil && s.Receipt.Status == "fenced" {
				rf := refuseLocal(verb, "FENCED", "op %s at epoch %d was fenced (settled as not applied)", op, eps[i])
				rf.Op, rf.OpEpoch, rf.Hint = op, eps[i], "run the verb under a new op"
				return true, rf
			}
			res.Replay, res.Epoch = true, eps[i]
			if s.Receipt != nil {
				res.Recorded = s.Receipt.Result
				if after, ok := undec(s.Receipt.EpochAfter); ok {
					res.EpochAfter = after
				}
			}
			res.Said = fmt.Sprintf("%s: op %s already applied at epoch %d; nothing was written", verb, op, eps[i])
			return true, nil
		default:
			return true, refuseLocal(verb, sprintfn.CodeConfig, "done answered %q", s.Status)
		}
	}
	return false, nil
}

// Do runs a verb of one step (1.5.3): read, plan, build, send. A race is
// planned again on a fresh read at most Retries times; any other refusal is
// returned at once, not retried (1.3.5). Two round trips, one more for each
// retry and each reload; the model's actions are VerbPlan (the read and the
// plan) and VerbApply (the guards checked and the step committed, or nothing),
// tla/SprintEvents.tla.
//
// With no op, or an op the verb made (Made), a read or a refusal that shows
// the epoch moved (AL2; STALE, EPOCHAHEAD) moves Env.Epoch and plans again at
// the active epoch. With an op the caller gave, the op never changes epoch
// (L1 5): Env.Epoch is the op's epoch, the plan's read asks done for the op at
// it and the OpEpochsMax - 1 epochs before it, and a match returns the
// recorded result with no step, wherever the receipt is (L1 5). An op done
// finds nowhere from the look-back up to the active epoch never ran, and takes
// the active epoch (L1 5); otherwise a sprint whose epoch moved from the op's
// is refused, naming both epochs, never planned again at the new one. The
// look-back never asks below the sprint's first kept epoch (FirstEpoch,
// learnFirst). A lost reply is settled by done before it is
// reported (L1 5, 8), and a refused send of a caller's op, which may be a
// resend of a copy still in flight, is reported final only once the fence
// settles it (fence).
func (e *Env) Do(ctx context.Context, p Planned) (Result, error) {
	res := Result{Verb: p.Verb, Op: p.Op}
	if p.Op != "" && !validOp(p.Op) {
		return res, refuseLocal(p.Verb, sprintfn.CodeRequest, "--op %q is not an op (text of at most 200 bytes, no blank, '~' or '/')", p.Op)
	}
	pinned := p.Op != "" && !p.Made
	opEpoch := e.epoch()
	intentAt := func(ep uint64) (string, error) { return Intent(e.Names, dec(ep), p.Verb, 0, p.Args) }
	askDone := pinned
	learned := false // the look-back's floor was found after an EPOCHGONE
	for {
		at := e.epoch()
		if pinned {
			at = opEpoch
		}
		epoch := dec(at)
		res.Epoch = at
		var intent string
		if p.Op != "" {
			var err error
			if intent, err = intentAt(at); err != nil {
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
		doneAt, eps := -1, []uint64(nil)
		if askDone {
			ids, ws, err := opWindow(p.Op, at, e.windowLow(at), intentAt)
			if err != nil {
				return res, refuseLocal(p.Verb, sprintfn.CodeRequest, "%v", err)
			}
			rr.Tset = append(append([]tset.ReadQuery(nil), rr.Tset...), tset.ReadQuery{Kind: "done", Ops: ids})
			doneAt, eps = len(rr.Tset)-1, ws
		}
		var rd *sprintfn.ReadReply
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
				if ref.Code == codeEpochGone && doneAt >= 0 && !learned {
					// The look-back named an epoch that is gone: find the
					// sprint's first kept epoch, and ask again above it.
					learned = true
					rose, err := e.learnFirst(ctx, &res, at, p.Op)
					if err != nil {
						return res, err
					}
					if rose {
						continue
					}
				}
				if epochMoved(ref.Code) {
					if active, ok := undec(ref.Detail.ActiveEpoch); ok && pinned {
						return res, opMoved(p.Verb, p.Op, 0, at, active, 0)
					}
					if !pinned && res.Retries < Retries && e.reload(ref) {
						res.Retries++
						continue
					}
				}
				return res, &Refused{Verb: p.Verb, Refusal: ref, Retries: res.Retries, Op: p.Op}
			}
			rd = r.Read
			if doneAt >= 0 {
				slots, rf := doneOf(p.Verb, rd, doneAt, len(eps))
				if rf != nil {
					return res, rf
				}
				rd.Tset = rd.Tset[:doneAt]
				if settled, err := settleDo(&res, p.Verb, p.Op, slots, eps); settled {
					res.Read = rd
					return res, err
				}
				askDone = false
			}
			// AL2: the read names the active epoch.
			if active, ok := undec(rd.ActiveEpoch); ok && active != at {
				if pinned {
					if doneAt >= 0 && active > at {
						// The caller's epoch is behind the sprint's: the op's
						// receipt may be at an epoch after it. One more done.
						if settled, err := e.lookAhead(ctx, &res, p.Verb, p.Op, at, active, intentAt); settled {
							return res, err
						}
						// Done is absent from the look-back below the op's
						// epoch up to the active one: the op never ran, so it
						// takes the active epoch (never refuse an op that never
						// ran). A copy at an older epoch can no longer commit
						// (S.open refuses STALE), and a copy at the active one
						// is caught by the step's own receipt check (L1 5).
						// Only when the look-ahead covered every epoch between.
						if active-at <= OpEpochsMax && res.Retries < Retries {
							res.Retries++
							opEpoch, askDone = active, false
							e.setEpoch(active)
							continue
						}
					}
					return res, opMoved(p.Verb, p.Op, 0, at, active, 0)
				}
				if res.Retries >= Retries {
					return res, &Refused{Verb: p.Verb, Retries: res.Retries, Op: p.Op,
						Refusal: &sprintfn.Refusal{Code: sprintfn.CodeStale, Message: "the epoch kept moving under the verb", Detail: emptyDetail()}}
				}
				res.Retries++
				e.setEpoch(active)
				continue
			}
		}
		res.Read = rd
		if p.Plan == nil {
			return res, nil
		}
		part, err := p.Plan(rd)
		if err != nil {
			var rf *Refused
			if errors.As(err, &rf) && rf.Verb == "" {
				rf.Verb = p.Verb
			}
			return res, err
		}
		req := part.Req
		if req == nil {
			res.Refused = append(res.Refused, part.Refused...)
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
		if err == nil && out.Err != nil {
			err = out.Err
		}
		if err != nil {
			rec, err := e.settleLost(ctx, &res, p.Verb, p.Op, 0, at, epoch, p.Op, intent, err)
			if err != nil {
				return res, err
			}
			e.fromReceipt(&res, rec)
			res.applied(part)
			return res, nil
		}
		if ref := out.Refusal; ref != nil {
			if IsRace(ref.Code) && res.Retries < Retries {
				if epochMoved(ref.Code) && pinned {
					active, _ := undec(ref.Detail.ActiveEpoch)
					rf := opMoved(p.Verb, p.Op, 0, at, active, 0)
					rf.Local = false
					res.refusedStep(part, ref)
					return res, rf
				}
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
			res.refusedStep(part, ref)
			rf := &Refused{Verb: p.Verb, Refusal: ref, Retries: res.Retries, Op: p.Op, Hint: raceHint(ref.Code)}
			if pinned {
				rec, err := e.fence(ctx, &res, rf, at, epoch, p.Op, intent)
				if err != nil {
					return res, err
				}
				// The fence replayed the original's receipt: it applied.
				res.Refused, res.NotWritten = nil, nil
				e.fromReceipt(&res, rec)
				res.Replay = true
				res.Said = fmt.Sprintf("%s: op %s had applied at epoch %d; nothing was written", p.Verb, p.Op, at)
				return res, nil
			}
			return res, rf
		}
		res.Step = out.Step
		res.Replay = out.Step.Reply.Replay
		res.Recorded = out.Step.Reply.Result
		if after, ok := undec(out.Step.Reply.EpochAfter); ok {
			res.EpochAfter = after
			if after > e.epoch() { // the step's own advance (a clear)
				e.setEpoch(after)
			}
		}
		if !res.Replay {
			res.applied(part)
		}
		return res, nil
	}
}

// raceHint is how to go on after a race refused past the retries: what the
// code means, so a drop that lasts minutes or a STOPPED machine is not read as
// a race that the next run wins (1.5.3; 1.0, the sprint's refusal codes).
func raceHint(code string) string {
	switch code {
	case sprintfn.CodeDropping:
		return "a stream the step names is being dropped (DROPPING), a drop in parts that may last minutes: run it again when the drop has ended (nova-sprint where shows it)"
	case sprintfn.CodeStopped:
		return "the machine is STOPPED (STOPPED): run it again after start"
	}
	if IsRace(code) {
		return fmt.Sprintf("the sprint kept moving under the verb for %d retries; run it again", Retries)
	}
	return ""
}

// lookAhead asks done once more for a caller's op whose epoch is behind the
// active one: the epochs after the op's, up to the active (at most
// OpEpochsMax). A match there is the op's receipt (settleDo).
func (e *Env) lookAhead(ctx context.Context, res *Result, verb, op string, at, active uint64, intentAt func(uint64) (string, error)) (bool, error) {
	lo := at + 1
	if active-lo >= OpEpochsMax {
		lo = active - (OpEpochsMax - 1)
	}
	ids, eps, err := opWindow(op, active, lo, intentAt)
	if err != nil {
		return true, refuseLocal(verb, sprintfn.CodeRequest, "%v", err)
	}
	r, err := sprintfn.Read(ctx, e.C, &sprintfn.ReadRequest{Epoch: dec(active), Tset: []tset.ReadQuery{{Kind: "done", Ops: ids}}})
	res.Trips++
	if err != nil {
		return true, err
	}
	if r.Err != nil {
		return true, r.Err
	}
	if r.Refusal != nil {
		return true, &Refused{Verb: verb, Refusal: r.Refusal, Op: op}
	}
	slots, rf := doneOf(verb, r.Read, 0, len(ids))
	if rf != nil {
		return true, rf
	}
	return settleDo(res, verb, op, slots, eps)
}

// fromReceipt takes a step's outcome from its receipt, when done or a fence
// settled it as applied rather than its reply.
func (e *Env) fromReceipt(res *Result, rec *tset.DoneReceipt) {
	res.Recorded = rec.Result
	if after, ok := undec(rec.EpochAfter); ok {
		res.EpochAfter = after
		if after > e.epoch() {
			e.setEpoch(after)
		}
	}
}

// settleLost settles a lost reply by done before it is reported (L1 5, 8): a
// match is the step applied, and its receipt is returned; a conflict or a
// fenced receipt is final; an absent receipt is not proof (the copy may still
// be in flight), and neither is a done that fails, so both report the outcome
// unknown. A step with no op has no receipt to ask for.
func (e *Env) settleLost(ctx context.Context, res *Result, verb, op string, part int, opEpoch uint64, epoch tset.Decimal, id, intent string, cause error) (*tset.DoneReceipt, error) {
	var ie *sprintfn.ItemError
	if errors.As(cause, &ie) {
		return nil, &Refused{Verb: verb, Refusal: ie.Refusal, Op: op, Part: part, Local: true}
	}
	unknown := &Unknown{Verb: verb, Op: op, Epoch: opEpoch, Part: part, Err: cause}
	if op == "" {
		return nil, unknown
	}
	r, err := sprintfn.Read(ctx, e.C, &sprintfn.ReadRequest{Epoch: epoch, Tset: []tset.ReadQuery{{Kind: "done",
		Ops: []tset.DoneIdentity{{Epoch: epoch, Op: id, IntentDigest: digest(intent)}}}}})
	res.Trips++
	if err != nil || r.Err != nil || r.Refusal != nil {
		return nil, unknown
	}
	slots, rf := doneOf(verb, r.Read, 0, 1)
	if rf != nil {
		return nil, unknown
	}
	switch s := slots[0]; {
	case s.Status == "match" && s.Receipt != nil && s.Receipt.Status == "ok":
		return s.Receipt, nil
	case s.Status == "match":
		rf := refuseLocal(verb, "FENCED", "%s at epoch %s was fenced (settled as not applied)", id, epoch)
		rf.Op, rf.OpEpoch, rf.Part, rf.Hint = op, opEpoch, part, "run the verb under a new op"
		return nil, rf
	case s.Status == "conflict":
		rf := refuseLocal(verb, "OPCONFLICT", "%s at epoch %s was applied with other arguments", id, epoch)
		rf.Op, rf.Part = op, part
		return nil, rf
	}
	return nil, unknown
}

// fence settles a caller's op whose send was refused (L1 5): the send may be
// a resend of a copy still in flight (after a crash every unfinished part is
// presumed dispatched), and a refusal other than STALE or OPCONFLICT does not
// settle it. The fence sends the same epoch, identity and intent with nothing
// else. It wins (status fenced): the identity is settled as not applied, and
// the refusal is final, naming the fence. It replays the original's ok
// receipt: the original applied, and its receipt is returned. It is refused
// STALE or OPCONFLICT: settled, the refusal is final. Anything else (another
// refusal, a lost reply) settles nothing: the outcome stays unknown
// (OUTCOMEUNKNOWN), never a final refusal.
func (e *Env) fence(ctx context.Context, res *Result, rf *Refused, opEpoch uint64, epoch tset.Decimal, id, intent string) (*tset.DoneReceipt, error) {
	if code := rf.Code(); code == sprintfn.CodeStale || code == "OPCONFLICT" {
		return nil, rf
	}
	req := &sprintfn.Request{Fence: true, Body: sprintfn.Body{Op: &sprintfn.Op{ID: id, Intent: intent}}}
	e.fill(req, rf.Verb, epoch)
	out, err := sprintfn.Step(ctx, e.C, req)
	res.Trips++
	open := func(why string) error {
		return &Unknown{Verb: rf.Verb, Op: rf.Op, Epoch: opEpoch, Part: rf.Part,
			Err: fmt.Errorf("%w: the send was refused %s and the fence %s", tset.ErrOutcomeUnknown, rf.Code(), why)}
	}
	switch {
	case err != nil:
		return nil, open(fmt.Sprintf("was lost (%v)", err))
	case out.Err != nil:
		return nil, open(fmt.Sprintf("was lost (%v)", out.Err))
	case out.Refusal != nil:
		if c := out.Refusal.Code; c == sprintfn.CodeStale || c == "OPCONFLICT" {
			return nil, rf
		}
		return nil, open("was refused " + out.Refusal.Code)
	case out.Step.Reply.Status == "fenced":
		rf.OpEpoch = opEpoch
		rf.Hint = strings.TrimPrefix(rf.Hint+"; ", "; ") + fmt.Sprintf("%s at epoch %d is fenced (settled as not applied): run the verb under a new op", id, opEpoch)
		return nil, rf
	case out.Step.Reply.Status == "ok":
		r := out.Step.Reply
		return &tset.DoneReceipt{Status: "ok", EpochBefore: r.EpochBefore, EpochAfter: r.EpochAfter,
			FirstSeq: r.FirstSeq, LastSeq: r.LastSeq, Changed: r.Changed, Result: r.Result}, nil
	}
	return nil, open(fmt.Sprintf("answered status %q", out.Step.Reply.Status))
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
		cfg.Ident = func(i int) stepbuild.Ident {
			if i == 0 {
				return id
			}
			// A cut past one step is refused LIMIT below and never sent;
			// each cut step gets its own identity only so the builder's
			// check of repeated identities does not mask the LIMIT.
			c := id
			c.Op = id.Op + "-cut" + strconv.Itoa(i)
			return c
		}
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
	// Read is part k's read from the continuation ("" for part 1), at the
	// epoch the part is sent at.
	Read func(epoch tset.Decimal, cont string, chunk int) *sprintfn.ReadRequest
	// Plan is part k's step from its read and the continuation.
	Plan func(rd *sprintfn.ReadReply, cont string, chunk int) (Part, error)
}

// Part is one planned step or part: its request, the ids it moves and the
// named ids its plan refused (1.5.4: a verb names the ineligible "in the
// result"), the continuation the next part starts from, and whether it is
// the last.
type Part struct {
	Req     *sprintfn.Request
	Moved   []ID
	Refused []RefusedID
	Next    string
	Last    bool
}

// partResult is a part's caller result (1.5.4: at most 4 KiB): the part, the
// continuation after it, the chunk the parts run at, and in the last part the
// finished mark (errata 3, H4 cutmark), so a resume of a finished op knows it
// ended.
//
// Owed, not built here: the cut entry cut:<op> in {p}cut@e on the write path.
// The model's cutmark (tla/SprintEvents.tla, cutj) guards on the op's
// dropping marks, and PartApply clears them and sets cut to -1 in the final
// part; R18's cut judgment knows only cut:<op>, and cannot read this mark
// (done needs <op>/p<k> and the intent's digest). Carrying cut:<op> on the
// write path, and removing it with the mark in the last part's step, is
// IT21's and IT13's; until then H4 holds on the resume side only.
type partResult struct {
	Part  int    `json:"k"`
	Next  string `json:"next"`
	Chunk int    `json:"chunk"`
	Fin   bool   `json:"fin,omitempty"`
}

// partID is part k's op identity (1.5.4; L1 5).
func partID(op string, k int) string { return op + "/p" + strconv.Itoa(k) }

// firstWindow is how many part identities a caller's op's first trip asks
// done for at its epoch, beside part 1's read and part 1 at the epochs before
// it; a resume past it asks the rest DoneWindow at a time.
const firstWindow = 64

// advances says a request advances the epoch (a clear).
func advances(req *sprintfn.Request) bool {
	for _, en := range req.Body.Entries {
		if en.Kind == "advance" {
			return true
		}
	}
	return false
}

// Parts runs a verb in parts (1.5.4). Each part is one atomic step with its
// own identity <op>/p<k> and an intent of the verb's arguments, the op's
// epoch and the part's index; its step is pipelined with the next part's
// read, so n parts cost n + 1 round trips. A race is planned again on a fresh
// read, at most Retries times a part; a LIMIT, from the builder before
// anything is sent or from the store, plans the part again at half the chunk,
// and the parts after it keep the half. Any other refusal ends the op: the
// parts before stay applied and the refusal says how to go on. The model's
// actions are PartApply (a part applies once, its receipt holding its
// continuation and, in the last part, final) and VerbCrash (the verb dies
// between its read and its step, or between parts), tla/SprintEvents.tla.
//
// Given no op, Parts makes one (NewOp) and prints it; until its first part
// applies it plans at the active epoch. Given an op, the op never changes
// epoch (L1 5): the first trip is part 1's read with done for the op's parts at
// Env.Epoch and for part 1 at the epochs before it (resumeRead), so a first
// run costs n + 1 round trips and a resume finds the op's receipts where they
// are; a finished op (its last part's finished mark) returns at once, and the
// parts go on from the last applied part's continuation, so a resume neither
// skips nor repeats. An op's parts are sent at its epoch, and after a part of
// the op's own that advances (a clear) at the epoch after it; a sprint whose
// epoch moved otherwise is refused, naming the op's epoch, never resumed at
// the new one. A lost reply is settled by done, and the first send of a
// caller's op, which may repeat a copy in flight, is fenced before a refusal
// of it is reported final (L1 5; Do).
func (e *Env) Parts(ctx context.Context, op string, pp PartsPlan) (Result, error) {
	res := Result{Verb: pp.Verb}
	made := op == ""
	if made {
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
	opEpoch := e.epoch() // the intent's epoch, fixed once a part applied
	reqEpoch := opEpoch  // the epoch the next part is sent at
	res.Epoch = opEpoch
	intentAt := func(ep uint64, k int) (string, error) { return Intent(e.Names, dec(ep), pp.Verb, k, pp.Args) }
	k, cont := 1, ""
	begun := false             // a part of the op applied: its epoch is fixed
	resend := !made            // the next send may repeat a copy still in flight (L1 5)
	var rd *sprintfn.ReadReply // the read of part k, when a trip brought it
	if !made {
		r, rs, err := e.resumeRead(ctx, &res, pp, op, opEpoch, chunk, intentAt)
		if err != nil {
			return res, err
		}
		rd = r
		if rs.last > 0 {
			begun = true
			opEpoch, reqEpoch, res.Epoch, res.Resumed = rs.opEpoch, rs.reqEpoch, rs.opEpoch, rs.last
			if rs.rec.Fin {
				res.Chunk, res.Replay, res.EpochAfter = rs.rec.Chunk, true, rs.reqEpoch
				res.Said = fmt.Sprintf("%s: op %s finished at part %d; nothing was written", pp.Verb, op, rs.last)
				return res, nil
			}
			k, cont = rs.last+1, rs.rec.Next
			if rs.rec.Chunk > 0 && rs.rec.Chunk < chunk {
				chunk = rs.rec.Chunk
			}
		}
	}
	retries := 0
	move := func(active uint64) bool { // a made op not yet begun plans at the active epoch (AL2)
		if !made || begun || retries >= Retries {
			return false
		}
		retries++
		res.Retries++
		e.setEpoch(active)
		opEpoch, reqEpoch, res.Epoch, rd = active, active, active, nil
		return true
	}
	for {
		if rd == nil {
			r, err := e.readPart(ctx, &res, pp, dec(reqEpoch), cont, chunk)
			if err != nil {
				var rf *Refused
				if errors.As(err, &rf) && epochMoved(rf.Code()) {
					if active, ok := undec(rf.Refusal.Detail.ActiveEpoch); ok {
						if move(active) {
							continue
						}
						if !made || begun {
							return res, opMoved(pp.Verb, op, k, opEpoch, active, k-1)
						}
					}
				}
				return res, err
			}
			rd = r
		}
		if active, ok := undec(rd.ActiveEpoch); ok && active != reqEpoch {
			if move(active) {
				continue
			}
			if made && !begun {
				return res, &Refused{Verb: pp.Verb, Retries: retries, Op: op, Part: k,
					Refusal: &sprintfn.Refusal{Code: sprintfn.CodeStale, Message: "the epoch kept moving under the verb", Detail: emptyDetail()}}
			}
			return res, opMoved(pp.Verb, op, k, opEpoch, active, k-1)
		}
		part, err := pp.Plan(rd, cont, chunk)
		if err != nil {
			var rf *Refused
			if errors.As(err, &rf) {
				rf.Verb, rf.Op, rf.Part = pp.Verb, op, k
				if rf.Hint == "" && k > 1 {
					rf.Hint = fmt.Sprintf("parts 1 to %d stay applied; fix, then run the same command with --op %s --epoch %d", k-1, op, opEpoch)
				}
			}
			return res, err
		}
		if part.Req == nil {
			return res, refuseLocal(pp.Verb, sprintfn.CodeRequest, "part %d planned no step", k)
		}
		intent, err := intentAt(opEpoch, k)
		if err != nil {
			return res, refuseLocal(pp.Verb, sprintfn.CodeRequest, "%v", err)
		}
		pr, _ := json.Marshal(partResult{Part: k, Next: part.Next, Chunk: chunk, Fin: part.Last})
		if len(pr) > tset.MaxResultBytes {
			return res, refuseLocal(pp.Verb, sprintfn.CodeLimit, "part %d's continuation is %d bytes, over the caller result's %d", k, len(pr), tset.MaxResultBytes)
		}
		req := part.Req
		e.fill(req, pp.Verb, dec(reqEpoch))
		id := partID(op, k)
		req.Body.Op = &sprintfn.Op{ID: id, Intent: intent, Result: string(pr)}
		if rf := build(pp.Verb, req); rf != nil {
			if rf.Code() == sprintfn.CodeLimit && chunk > 1 {
				chunk /= 2 // the builder's cut: nothing was sent
				rd = nil
				continue
			}
			rf.Op, rf.Part = op, k
			return res, rf
		}
		after := reqEpoch // the epoch the next part is read and sent at
		if advances(req) {
			after = reqEpoch + 1
		}
		items := []sprintfn.Item{{Step: req}}
		if !part.Last {
			items = append(items, sprintfn.Item{Read: e.partRead(pp, dec(after), part.Next, chunk)})
		}
		out, err := e.C.Pipeline(ctx, items)
		res.Trips++
		lost := err
		switch {
		case lost != nil:
		case len(out) != len(items):
			lost = fmt.Errorf("%w: a pipeline of %d returned %d results", tset.ErrOutcomeUnknown, len(items), len(out))
		case out[0].Err != nil:
			lost = out[0].Err
		}
		// rec is the part's receipt when done or the fence settled it, and
		// earlier says an earlier copy applied it (a replay).
		var rec *tset.DoneReceipt
		earlier := false
		if lost != nil {
			r, err := e.settleLost(ctx, &res, pp.Verb, op, k, opEpoch, dec(reqEpoch), id, intent, lost)
			if err != nil {
				return res, err
			}
			rec = r
		} else if ref := out[0].Refusal; ref != nil {
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
			case epochMoved(ref.Code):
				active, _ := undec(ref.Detail.ActiveEpoch)
				if move(active) {
					continue
				}
				res.refusedStep(part, ref)
				rf := opMoved(pp.Verb, op, k, opEpoch, active, k-1)
				rf.Local = false
				return res, rf
			}
			rf := &Refused{Verb: pp.Verb, Refusal: ref, Retries: retries, Op: op, Part: k,
				Hint: fmt.Sprintf("parts 1 to %d stay applied; fix, then run the same command with --op %s --epoch %d", k-1, op, opEpoch)}
			if k == 1 {
				rf.Hint = "no part was applied; fix, then run it again"
			}
			if h := raceHint(ref.Code); h != "" {
				rf.Hint = h + "; " + rf.Hint
			}
			if !resend {
				res.refusedStep(part, ref)
				return res, rf
			}
			r, err := e.fence(ctx, &res, rf, opEpoch, dec(reqEpoch), id, intent)
			if err != nil {
				res.refusedStep(part, ref)
				return res, err
			}
			rec, earlier = r, true
		}
		begun, resend, retries = true, false, 0
		res.Parts++
		res.Chunk = chunk
		if rec == nil {
			res.Step = out[0].Step
			earlier = out[0].Step.Reply.Replay
			if earlier {
				r := out[0].Step.Reply
				rec = &tset.DoneReceipt{Status: r.Status, EpochAfter: r.EpochAfter, Result: r.Result}
			} else if a, ok := undec(out[0].Step.Reply.EpochAfter); ok {
				after = a
			}
		}
		if !earlier {
			res.applied(part)
		}
		if rec != nil {
			// The part's outcome is its receipt's: the continuation it
			// recorded, and the epoch after it. The read that rode with the
			// step is dropped (it was planned from this run's continuation).
			var got partResult
			if err := json.Unmarshal([]byte(rec.Result), &got); err != nil || got.Part != k {
				return res, refuseLocal(pp.Verb, sprintfn.CodeConfig, "op %s part %d's receipt holds no continuation", op, k)
			}
			part.Next, part.Last = got.Next, got.Fin
			if got.Chunk > 0 && got.Chunk < chunk {
				chunk = got.Chunk
			}
			if a, ok := undec(rec.EpochAfter); ok {
				after = a
			}
		}
		res.EpochAfter = after
		if after != reqEpoch {
			reqEpoch = after
			if after > e.epoch() {
				e.setEpoch(after)
			}
		}
		if part.Last {
			res.Said = fmt.Sprintf("%s: op %s finished in %d parts", pp.Verb, op, k)
			return res, nil
		}
		k, cont = k+1, part.Next
		rd = nil
		if rec != nil {
			continue
		}
		switch next := out[1]; {
		case next.Err != nil:
			return res, next.Err
		case next.Refusal != nil:
			return res, &Refused{Verb: pp.Verb, Refusal: next.Refusal, Op: op, Part: k,
				Hint: fmt.Sprintf("parts 1 to %d are applied; run the same command with --op %s --epoch %d", k-1, op, opEpoch)}
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
		rr = &sprintfn.ReadRequest{Sprint: []sprintfn.SprintQuery{keyQuery(sprintfn.KeyQ{Kind: sprintfn.KeyClock})}}
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

// resumeState is where a caller's op stands: the last part applied in order,
// its recorded result, the op's epoch, and the epoch its next part is sent
// at (after a part of its own that advanced, the one after the op's).
type resumeState struct {
	last              int
	rec               partResult
	opEpoch, reqEpoch uint64
}

// partWindow is done's identities of parts first..first+n-1 of an op of
// epoch opEpoch, sent at reqEpoch.
func partWindow(op string, opEpoch, reqEpoch uint64, first, n int, intentAt func(uint64, int) (string, error)) ([]tset.DoneIdentity, error) {
	ids := make([]tset.DoneIdentity, 0, n)
	for k := first; k < first+n; k++ {
		intent, err := intentAt(opEpoch, k)
		if err != nil {
			return nil, err
		}
		ids = append(ids, tset.DoneIdentity{Epoch: dec(reqEpoch), Op: partID(op, k), IntentDigest: digest(intent)})
	}
	return ids, nil
}

// resumeRead is a caller's op's first trip (1.5.4; L1 5): part 1's read at
// the op's epoch, with done for parts 1 to firstWindow at that epoch and for
// part 1 at the OpEpochsMax - 1 epochs before it, in one atomic read, so a
// first run costs n + 1 round trips. When no part 1 is found the op is new,
// and the read is part 1's. When part 1 is found at the op's epoch or an
// earlier one, that epoch is the op's (L1 5), the resume walks its receipts
// (walkParts), and the read is dropped: the next part is read from the last
// receipt's continuation.
func (e *Env) resumeRead(ctx context.Context, res *Result, pp PartsPlan, op string, opEpoch uint64, chunk int, intentAt func(uint64, int) (string, error)) (*sprintfn.ReadReply, resumeState, error) {
	rs := resumeState{opEpoch: opEpoch, reqEpoch: opEpoch}
	here, err := partWindow(op, opEpoch, opEpoch, 1, firstWindow, intentAt)
	if err != nil {
		return nil, rs, refuseLocal(pp.Verb, sprintfn.CodeRequest, "%v", err)
	}
	var before []tset.DoneIdentity
	var eps []uint64
	var rr *sprintfn.ReadRequest
	var r sprintfn.Result
	for learned := false; ; learned = true {
		before, eps = nil, nil
		if lo := e.windowLow(opEpoch); lo < opEpoch {
			before, eps, err = opWindow(partID(op, 1), opEpoch-1, lo, func(ep uint64) (string, error) { return intentAt(ep, 1) })
			if err != nil {
				return nil, rs, refuseLocal(pp.Verb, sprintfn.CodeRequest, "%v", err)
			}
		}
		rr = e.partRead(pp, dec(opEpoch), "", chunk)
		rr.Tset = append(append([]tset.ReadQuery(nil), rr.Tset...), tset.ReadQuery{Kind: "done", Ops: append(append([]tset.DoneIdentity(nil), here...), before...)})
		r, err = sprintfn.Read(ctx, e.C, rr)
		res.Trips++
		if err != nil {
			return nil, rs, err
		}
		if r.Err != nil {
			return nil, rs, r.Err
		}
		if ref := r.Refusal; ref != nil && ref.Code == codeEpochGone && len(before) != 0 && !learned {
			// The look-back named an epoch that is gone: find the sprint's
			// first kept epoch, and ask again above it.
			rose, err := e.learnFirst(ctx, res, opEpoch, partID(op, 1))
			if err != nil {
				return nil, rs, err
			}
			if rose {
				continue
			}
		}
		break
	}
	at := len(rr.Tset) - 1
	if ref := r.Refusal; ref != nil {
		if active, ok := undec(ref.Detail.ActiveEpoch); ok && epochMoved(ref.Code) {
			return nil, rs, opMoved(pp.Verb, op, 1, opEpoch, active, 0)
		}
		return nil, rs, &Refused{Verb: pp.Verb, Refusal: ref, Op: op}
	}
	rd := r.Read
	slots, rf := doneOf(pp.Verb, rd, at, len(here)+len(before))
	if rf != nil {
		return nil, rs, rf
	}
	rd.Tset = rd.Tset[:at]
	if slots[0].Status != "absent" {
		rs, err := e.walkParts(ctx, res, pp.Verb, op, opEpoch, slots[:len(here)], intentAt)
		return nil, rs, err
	}
	for i, s := range slots[len(here):] {
		if s.Status != "absent" { // the op began at an earlier epoch: that one is its
			rs, err := e.walkParts(ctx, res, pp.Verb, op, eps[i], []tset.DoneSlot{s}, intentAt)
			return nil, rs, err
		}
	}
	return rd, rs, nil
}

// walkParts reads an op's receipts from part 1 in order, slots being done's
// answer for parts 1 onwards at the op's epoch, and returns the last part
// applied in order and its recorded result (L1 5; 1.5.4). The parts after a
// part that advanced (a clear's) are at the epoch after it, and are asked
// there. A conflict is the same op with other arguments (OPCONFLICT); a
// fenced part was settled as not applied, and the op cannot go on under its
// name (FENCED); a receipt that holds no continuation, or a reply that does
// not answer each identity, is refused CONFIG, never indexed past its end.
func (e *Env) walkParts(ctx context.Context, res *Result, verb, op string, opEpoch uint64, slots []tset.DoneSlot, intentAt func(uint64, int) (string, error)) (resumeState, error) {
	rs := resumeState{opEpoch: opEpoch, reqEpoch: opEpoch}
	at, first := opEpoch, 1
	for {
		moved := false
		for i, s := range slots {
			k := first + i
			switch s.Status {
			case "absent":
				return rs, nil
			case "conflict":
				return rs, refuseLocal(verb, "OPCONFLICT", "op %s part %d was applied with other arguments", op, k)
			case "match":
				if s.Receipt == nil {
					return rs, refuseLocal(verb, sprintfn.CodeConfig, "done matched op %s part %d with no receipt", op, k)
				}
				if s.Receipt.Status != "ok" {
					rf := refuseLocal(verb, "FENCED", "op %s part %d was fenced (settled as not applied)", op, k)
					rf.Op, rf.OpEpoch, rf.Part, rf.Hint = op, opEpoch, k, "run the verb under a new op"
					return rs, rf
				}
				var pr partResult
				if err := json.Unmarshal([]byte(s.Receipt.Result), &pr); err != nil || pr.Part != k {
					return rs, refuseLocal(verb, sprintfn.CodeConfig, "op %s part %d's receipt holds no continuation", op, k)
				}
				rs.last, rs.rec, rs.reqEpoch = k, pr, at
				if a, ok := undec(s.Receipt.EpochAfter); ok && a > at {
					rs.reqEpoch = a
				}
				if pr.Fin {
					return rs, nil
				}
				if rs.reqEpoch != at { // the part advanced: ask the rest at the new epoch
					at, first, moved = rs.reqEpoch, k+1, true
				}
			default:
				return rs, refuseLocal(verb, sprintfn.CodeConfig, "done answered %q", s.Status)
			}
			if moved {
				break
			}
		}
		if !moved {
			first += len(slots)
		}
		ids, err := partWindow(op, opEpoch, at, first, DoneWindow, intentAt)
		if err != nil {
			return rs, refuseLocal(verb, sprintfn.CodeRequest, "%v", err)
		}
		r, err := sprintfn.Read(ctx, e.C, &sprintfn.ReadRequest{Epoch: dec(at), Tset: []tset.ReadQuery{{Kind: "done", Ops: ids}}})
		res.Trips++
		if err != nil {
			return rs, err
		}
		if r.Err != nil {
			return rs, r.Err
		}
		if r.Refusal != nil {
			return rs, &Refused{Verb: verb, Refusal: r.Refusal, Op: op}
		}
		got, rf := doneOf(verb, r.Read, 0, len(ids))
		if rf != nil {
			return rs, rf
		}
		slots = got
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
