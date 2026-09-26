// moves.go: the table moves (nova-tools #3929; rowan-new
// specs/table-moves.md). The primary is a task card (task:<id>, in its
// stream's ws:<stream>:<where> set, never leaving it); a Consumer (a bench
// or a friend, one shape) holds COPIES of primaries in its four sets
// <consumer>:cards:ready|working|ok|fail. Each function here is one FCALL
// of the TM functions in fn/lua/02_card_move.lua, the only writer of those
// sets and of the copy and primary pointers:
//
//	Deal   primary waiting -> working (a work copy) or review (a read copy);
//	       the copy -> <consumer>:cards:ready
//	Work   copy ready -> working, k = min(free, |ready|), free = slots - |working|
//	End    copy working -> ok|fail and, in the same call, the primary's move
//	       (review, its read copies cut | landed | done | waiting for a work
//	       copy; merging | review + a fix copy | review for a read copy;
//	       review + fresh read copies for a fix copy's new head)
//	Rehead a PR's new head: its review primaries' open copies retire and
//	       fresh read copies are cut (pr record --head)
//	EnsureReads the review reads duty on each deal pass: a moved head
//	       re-headed, a review primary with no live copy given its reads
//	       (review | landed | done for a work copy; merging |
//	       working + a fix copy | review for a read copy)
//	Cancel a copy given back, or a primary cancelled with its live copy
//	Beat   a working copy's lease; Expire returns lapsed copies as fails
//	Review the one way out of review (#4072): a copy's fail moves its
//	       primary to ws:<stream>:review; a typed verdict moves it on
//	FsckMoves both links both ways
//
// The table reads only ZCARDs (ReadCells): done = ok + fail and ok% are
// derived from the cells, never stored.
package taskcard

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/pipeerr"
	"github.com/redis/go-redis/v9"
)

// The table move functions (fn/lua/02_card_move.lua, TM).
const (
	FnDeal   = "ns_cm_deal"
	FnWork   = "ns_cm_work"
	FnEnd    = "ns_cm_end"
	FnCancel = "ns_cm_cancel"
	// FnCancelEach is card cancel --each (#4309): every id on its own.
	FnCancelEach = "ns_cm_cancel_each"
	FnBeatCopy   = "ns_cm_beat"
	FnExpireCp   = "ns_cm_expire"
	FnFsckMove   = "ns_cm_fsck"
	FnRepair     = "ns_cm_repair"
	FnAssign     = "ns_cm_assign"
	FnHead       = "ns_cm_head"
	FnReads      = "ns_cm_reads"
	FnReview     = "ns_cm_review"
)

// Verdicts are the typed ways out of review (#4072); reassign names its
// consumer: reassign:<consumer>.
var Verdicts = []string{"recut", "redeal", "reassign", "drop"}

// Cols are a consumer's four sets, in table order.
var Cols = []string{"ready", "working", "ok", "fail"}

// ConsumersKey is the SET of consumers the deal duty deals to (a consumer
// joins when its harness runs copies: card consumers --add).
const ConsumersKey = "consumers"

// Pass is the read score that moves a primary to merging (TM.PASS).
const Pass = 8

// Consumer is one consumer of copies: bench:<b> or friend:<f>. Nothing
// else about it depends on the kind.
type Consumer struct{ Kind, Name string }

var consumerRE = regexp.MustCompile(`^(bench|friend):([A-Za-z0-9][A-Za-z0-9._-]*)$`)

// ParseConsumer reads bench:<b> or friend:<f>.
func ParseConsumer(s string) (Consumer, error) {
	m := consumerRE.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return Consumer{}, fmt.Errorf("consumer %q is not bench:<b> or friend:<f>", s)
	}
	return Consumer{Kind: m[1], Name: m[2]}, nil
}

func (c Consumer) String() string { return c.Kind + ":" + c.Name }

// Key is the consumer's set at col (ready, working, ok, fail).
func (c Consumer) Key(col string) string { return c.String() + ":cards:" + col }

// DesiredKey holds the consumer's slots (and paused, tiers, kinds).
func (c Consumer) DesiredKey() string { return c.String() + ":desired" }

// DownKey marks the consumer down (a string or a hash; either means down).
func (c Consumer) DownKey() string { return c.String() + ":down" }

var copyRE = regexp.MustCompile(`^\S+~[0-9]+$`)

// IsCopy says whether id is a consumer copy's id, <primary>~<n>.
func IsCopy(id string) bool { return copyRE.MatchString(id) }

// PrimaryOf is a copy id's primary id ("" for a non-copy).
func PrimaryOf(id string) string {
	if !IsCopy(id) {
		return ""
	}
	return id[:strings.LastIndex(id, "~")]
}

func refusal(out []string) error {
	if len(out) == 2 && out[0] == "REFUSED" {
		return &Refused{Why: out[1]}
	}
	return nil
}

func fcall(ctx context.Context, c redis.Cmdable, fn string, args ...any) ([]string, error) {
	reply, err := c.FCall(ctx, fn, nil, args...).Result()
	if err != nil {
		return nil, fmt.Errorf("%s: %w", fn, err)
	}
	out, err := list(reply)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", fn, err)
	}
	if err := refusal(out); err != nil {
		return nil, err
	}
	return out, nil
}

// Dealt is one copy cut: its primary and its id.
type Dealt struct{ Primary, Copy string }

// DealRequest is one card deal: the named primaries (all or nothing), else
// the N oldest primaries To may take (read legs first, then work legs; in
// Stream only, or every stream in rank order).
type DealRequest struct {
	To     Consumer
	N      int
	Stream string
	IDs    []string
	By     string
}

// Deal is card deal: one call.
func Deal(ctx context.Context, c redis.Cmdable, r DealRequest) ([]Dealt, error) {
	args := []any{r.To.String(), r.By, r.N, r.Stream}
	for _, id := range r.IDs {
		args = append(args, id)
	}
	out, err := fcall(ctx, c, FnDeal, args...)
	if err != nil {
		return nil, err
	}
	if len(out) < 2 || out[0] != "DEALT" {
		return nil, fmt.Errorf("%s: unexpected reply %v", FnDeal, out)
	}
	var d []Dealt
	for i := 2; i+1 < len(out); i += 2 {
		d = append(d, Dealt{Primary: out[i], Copy: out[i+1]})
	}
	return d, nil
}

// Worked is one card work: the copies moved to working, the token each
// end may present (a stale one is FENCED), and the free slots left.
type Worked struct {
	IDs, Tokens []string
	Free        int
}

// Work is card work: ready -> working for as, n copies (fill: as many as
// are free), or the named ones; one call.
func Work(ctx context.Context, c redis.Cmdable, as Consumer, by string, n int, fill bool, ids ...string) (Worked, error) {
	return WorkAs(ctx, c, as, by, n, fill, Who{}, ids...)
}

// WorkAs is Work that also names who works the copies: ns_cm_work writes
// who's fields onto each worked copy's record in the same move
// (seat-keeps-beat: the one writer of task:<id>, never a second HSET).
func WorkAs(ctx context.Context, c redis.Cmdable, as Consumer, by string, n int, fill bool, who Who, ids ...string) (Worked, error) {
	if err := who.Check(); err != nil {
		return Worked{}, err
	}
	k := any(n)
	if fill {
		k = "fill"
	}
	args := []any{as.String(), by, k}
	for _, a := range who.Args() {
		args = append(args, a)
	}
	for _, id := range ids {
		args = append(args, id)
	}
	out, err := fcall(ctx, c, FnWork, args...)
	if err != nil {
		return Worked{}, err
	}
	if len(out) < 3 || out[0] != "WORKED" || (len(out)-3)%2 != 0 {
		return Worked{}, fmt.Errorf("%s: unexpected reply %v", FnWork, out)
	}
	free, _ := strconv.Atoi(out[2])
	w := Worked{Free: free}
	for i := 3; i+1 < len(out); i += 2 {
		w.IDs = append(w.IDs, out[i])
		w.Tokens = append(w.Tokens, out[i+1])
	}
	return w, nil
}

// Who is who works a copy (seat-keeps-beat, 2026-09-26: a working copy
// named no model, harness or child, so neither the card nor the table could
// say who held it): the model, the harness it runs in and the child's id,
// each one word, written onto task:<copy> as model, harness and child by
// ns_cm_work as it moves the copy to working (WorkAs: card work, friend
// pull, task take). An empty field is not written.
type Who struct{ Model, Harness, Child string }

// Args is who as ns_cm_work's who words (field=value), empty ones left out.
func (w Who) Args() []string {
	var a []string
	for _, kv := range [][2]string{{"model", w.Model}, {"harness", w.Harness}, {"child", w.Child}} {
		if v := strings.TrimSpace(kv[1]); v != "" {
			a = append(a, kv[0]+"="+v)
		}
	}
	return a
}

// Check refuses a field that is not one word.
func (w Who) Check() error {
	for _, kv := range [][2]string{{"model", w.Model}, {"harness", w.Harness}, {"child", w.Child}} {
		if v := strings.TrimSpace(kv[1]); v != "" && strings.ContainsAny(v, " \t\r\n") {
			return fmt.Errorf("--%s wants one word, got %q", kv[0], kv[1])
		}
	}
	return nil
}

// EndRequest is one card end over IDs (copies), each with the same result.
// OK with PR (and Head, which the PR record pr:<name>:<n> must hold, on the
// card's base) returns a work copy's primary to working to wait for CI at
// that head (#3093: OK moves it to review with a read copy, FAIL cuts a fix
// copy; a verdict already there applies at once); OK with DoneAlready (a
// sha) moves it to landed; OK with neither to done/ok; a fail moves it to
// review with Why and the evidence (#4072; Review is the way out). A read
// copy ends with Score (1-10) or a fail. The same
// end again is To "already" (nothing moves); other evidence on an ended
// copy is a CONFLICT refusal; a Token that is not the copy's, or a lapsed
// lease with it, is a FENCED refusal (#3488).
type EndRequest struct {
	IDs         []string
	OK          bool
	Why         string
	Repo, PR    string // the PR: repo (owner/name or name) and number
	Head        string
	DoneAlready string // ABSTAIN done-already <sha>
	Score       int    // a read copy's score, 1-10
	Reader      string // the SCORE line's who (else the consumer's name)
	Gates       string
	Finding     string
	// Fields are more result fields, name then value: line1 line2 check
	// paths branch commit base base_sha model route wall evidence tier key exit.
	Fields []string
	Token  string
	By     string
}

// Ended is one copy returned: the primary's move and the next copies, comma
// joined (the read copies a move into review cut, a fix copy cut on the
// author's queue, fresh reads at a fix's new head), if any.
type Ended struct {
	Copy, Primary, From, To, Next string
	// Why is set on a copy the expire sweep could NOT end (the move
	// refused it: DRIFT, a lost primary); To is then "". The copy stays
	// where it is, and the why is the line the sweep prints for it.
	Why string
}

// End is card end: one call.
func End(ctx context.Context, c redis.Cmdable, r EndRequest) ([]Ended, error) {
	outcome := "fail"
	if r.OK {
		outcome = "ok"
	}
	var fields []string
	add := func(k, v string) {
		if v != "" {
			fields = append(fields, k, v)
		}
	}
	add("repo", r.Repo)
	add("pr", r.PR)
	add("head", r.Head)
	add("gates", r.Gates)
	add("finding", r.Finding)
	for i := 0; i+1 < len(r.Fields); i += 2 {
		add(r.Fields[i], r.Fields[i+1])
	}
	score := ""
	if r.Score > 0 {
		score = strconv.Itoa(r.Score)
	}
	args := []any{r.By, outcome, r.Why, r.DoneAlready, score, r.Reader, r.Token, len(fields) / 2}
	for _, f := range fields {
		args = append(args, f)
	}
	for _, id := range r.IDs {
		args = append(args, id)
	}
	out, err := fcall(ctx, c, FnEnd, args...)
	if err != nil {
		return nil, err
	}
	return parseEnded(FnEnd, "ENDED", out, 5)
}

func parseEnded(fn, head string, out []string, width int) ([]Ended, error) {
	if len(out) < 2 || out[0] != head || (len(out)-2)%width != 0 {
		return nil, fmt.Errorf("%s: unexpected reply %v", fn, out)
	}
	var e []Ended
	for i := 2; i+width-1 < len(out); i += width {
		switch width {
		case 5:
			e = append(e, Ended{Copy: out[i], Primary: out[i+1], From: out[i+2], To: out[i+3], Next: out[i+4]})
		default:
			e = append(e, Ended{Copy: out[i], To: out[i+1]})
		}
	}
	return e, nil
}

// Assigned is one card assign: the copy cut on the consumer and the live
// copy it revoked ("" when there was none).
type Assigned struct{ Primary, Copy, Revoked string }

// Assign moves primary id's copy to consumer to (#2940): refused while the
// primary has a live copy unless revoke, which gives that copy back (its
// holder's end or beat is refused: the fence) and cuts the new one, in one
// call.
func Assign(ctx context.Context, c redis.Cmdable, to Consumer, id string, revoke bool, by, why string) (Assigned, error) {
	rv := "0"
	if revoke {
		rv = "1"
	}
	out, err := fcall(ctx, c, FnAssign, to.String(), id, rv, by, why)
	if err != nil {
		return Assigned{}, err
	}
	if len(out) != 4 || out[0] != "ASSIGNED" {
		return Assigned{}, fmt.Errorf("%s: unexpected reply %v", FnAssign, out)
	}
	return Assigned{Primary: out[1], Copy: out[2], Revoked: out[3]}, nil
}

// Reviewed is one review verdict applied: the primary's new where and, for
// reassign, the copy cut on the named consumer.
type Reviewed struct{ ID, Verdict, To, Copy string }

// Review is `review post` (#4072): the one way out of review, one call.
// verdict is recut, redeal, drop or reassign:<consumer>; why is required.
// A primary not in review, an untyped verdict or no why is a *Refused.
func Review(ctx context.Context, c redis.Cmdable, id, verdict, why, by string) (Reviewed, error) {
	out, err := fcall(ctx, c, FnReview, by, id, verdict, why)
	if err != nil {
		return Reviewed{}, err
	}
	if len(out) != 5 || out[0] != "REVIEWED" {
		return Reviewed{}, fmt.Errorf("%s: unexpected reply %v", FnReview, out)
	}
	return Reviewed{ID: out[1], Verdict: out[2], To: out[3], Copy: out[4]}, nil
}

// CancelCards is card cancel: a copy is given back (its primary returns,
// no attempt counted), a primary is cancelled (done/fail) with its live
// copy retired to fail. Ended.Copy is the id named, Ended.To its where.
func CancelCards(ctx context.Context, c redis.Cmdable, by, why string, ids ...string) ([]Ended, error) {
	args := []any{by, why}
	for _, id := range ids {
		args = append(args, id)
	}
	out, err := fcall(ctx, c, FnCancel, args...)
	if err != nil {
		return nil, err
	}
	return parseEnded(FnCancel, "CANCELLED", out, 2)
}

// Cancelled is one id of card cancel --each: To is where it went when it
// was cancelled, Why the refusal when it was not (one of the two is set).
type Cancelled struct{ ID, To, Why string }

// CancelEach is card cancel --each (#4309): every id is cancelled on its
// own in one call, so a refusal names its id and the rest still move. The
// only whole-call refusal is a missing why.
func CancelEach(ctx context.Context, c redis.Cmdable, by, why string, ids ...string) ([]Cancelled, error) {
	args := []any{by, why}
	for _, id := range ids {
		args = append(args, id)
	}
	out, err := fcall(ctx, c, FnCancelEach, args...)
	if err != nil {
		return nil, err
	}
	if len(out) < 3 || out[0] != "CANCEL" || (len(out)-3)%3 != 0 {
		return nil, fmt.Errorf("%s: unexpected reply %v", FnCancelEach, out)
	}
	var r []Cancelled
	for i := 3; i+2 < len(out); i += 3 {
		switch out[i+1] {
		case "CANCELLED":
			r = append(r, Cancelled{ID: out[i], To: out[i+2]})
		case "REFUSED":
			r = append(r, Cancelled{ID: out[i], Why: out[i+2]})
		default:
			return nil, fmt.Errorf("%s: unexpected reply %v", FnCancelEach, out)
		}
	}
	return r, nil
}

// BeatCopies renews as's working copies' leases; it returns the new
// lease_until (ms).
func BeatCopies(ctx context.Context, c redis.Cmdable, as Consumer, ids ...string) (int64, error) {
	args := []any{as.String()}
	for _, id := range ids {
		args = append(args, id)
	}
	out, err := fcall(ctx, c, FnBeatCopy, args...)
	if err != nil {
		return 0, err
	}
	if len(out) != 3 || out[0] != "BEAT" {
		return 0, fmt.Errorf("%s: unexpected reply %v", FnBeatCopy, out)
	}
	return strconv.ParseInt(out[2], 10, 64)
}

// ExpireCopies returns every working copy whose lease lapsed (of the named
// consumers, else every consumer) to its primary as a fail. Ended.Copy is
// the copy, Ended.To its primary's where.
func ExpireCopies(ctx context.Context, c redis.Cmdable, by string, consumers ...Consumer) ([]Ended, error) {
	args := []any{by}
	for _, k := range consumers {
		args = append(args, k.String())
	}
	out, err := fcall(ctx, c, FnExpireCp, args...)
	if err != nil {
		return nil, err
	}
	e, err := parseEnded(FnExpireCp, "EXPIRED", out, 2)
	for i := range e {
		// The function answers a refused finish as the pair id, "REFUSED
		// <why>" (no line about a lapsed copy that stays was the silent shape).
		if why, ok := strings.CutPrefix(e[i].To, "REFUSED "); ok {
			e[i].To, e[i].Why = "", why
		}
	}
	return e, err
}

// Recut is one primary whose read copies were cut: by a head move
// (Rehead, EnsureReads) or because it held none (EnsureReads).
type Recut struct {
	Primary string
	Copies  []string
}

func parseRecut(fn, head string, out []string) ([]Recut, error) {
	if len(out) < 2 || out[0] != head || (len(out)-2)%2 != 0 {
		return nil, fmt.Errorf("%s: unexpected reply %v", fn, out)
	}
	var r []Recut
	for i := 2; i+1 < len(out); i += 2 {
		x := Recut{Primary: out[i]}
		if out[i+1] != "" {
			x.Copies = strings.Split(out[i+1], ",")
		}
		r = append(r, x)
	}
	return r, nil
}

// Rehead is pr record --head's event (#4094): every primary of repo#n in
// review at another head than the PR record's has its live fix copy ended
// ok (the new head is its result), its open read copies retired, and fresh
// read copies cut at the new head; one call.
func Rehead(ctx context.Context, c redis.Cmdable, repo string, n int, by string) ([]Recut, error) {
	out, err := fcall(ctx, c, FnHead, repo, n, by)
	if err != nil {
		return nil, err
	}
	return parseRecut(FnHead, "REHEAD", out)
}

// EnsureReads is the review reads duty (#4094 DONE-WHEN 4), one call
// per deal pass: a review primary whose PR head moved is re-headed, and
// one with no live copy has its read copies cut.
func EnsureReads(ctx context.Context, c redis.Cmdable, by string) ([]Recut, error) {
	out, err := fcall(ctx, c, FnReads, by)
	if err != nil {
		return nil, err
	}
	return parseRecut(FnReads, "READS", out)
}

// MovesFsck is ns_cm_fsck's (or ns_cm_repair's) reply.
type MovesFsck struct {
	Consumers, Live, Retired, Primaries, Drift, Fixed int64
	Lines                                             []string
}

// FsckMoves proves both links both ways over every consumer and every
// stream; repair also fixes what it finds (a repair is a finding: Drift
// counts it).
func FsckMoves(ctx context.Context, c redis.Cmdable, repair bool) (MovesFsck, error) {
	var reply any
	var err error
	if repair {
		reply, err = c.FCall(ctx, FnRepair, nil).Result()
	} else {
		reply, err = c.FCallRO(ctx, FnFsckMove, nil).Result()
	}
	if err != nil {
		return MovesFsck{}, fmt.Errorf("card fsck: %w", err)
	}
	out, err := list(reply)
	if err != nil || len(out) < 7 || out[0] != "FSCK" {
		return MovesFsck{}, fmt.Errorf("card fsck: unexpected reply %v %v", out, err)
	}
	num := func(s string) int64 { v, _ := strconv.ParseInt(s, 10, 64); return v }
	return MovesFsck{Consumers: num(out[1]), Live: num(out[2]), Retired: num(out[3]), Primaries: num(out[4]),
		Drift: num(out[5]), Fixed: num(out[6]), Lines: out[7:]}, nil
}

// Cells are one consumer's table row: the four ZCARDs and its slots.
type Cells struct {
	Consumer                 Consumer
	Ready, Working, OK, Fail int64
	Slots                    int64 // -1 when <consumer>:desired has none
}

// Done is ok + fail: derived, never stored.
func (c Cells) Done() int64 { return c.OK + c.Fail }

// OKPct is ok/done in whole percent, rounded; -1 when nothing is done.
func (c Cells) OKPct() int64 {
	if c.Done() == 0 {
		return -1
	}
	return (c.OK*200 + c.Done()) / (2 * c.Done())
}

// Line is the row as a receipt: consumer ready working done ok fail ok%.
func (c Cells) Line() string {
	pct := "-"
	if p := c.OKPct(); p >= 0 {
		pct = strconv.FormatInt(p, 10)
	}
	return fmt.Sprintf("%s ready=%d working=%d done=%d ok=%d fail=%d ok%%=%s", c.Consumer, c.Ready, c.Working,
		c.Done(), c.OK, c.Fail, pct)
}

// ReadCells reads every consumer's row in ONE pipeline of ZCARDs (and the
// slots field): the table's consumer cells.
func ReadCells(ctx context.Context, c redis.Cmdable, consumers []Consumer) ([]Cells, error) {
	pipe := c.Pipeline()
	cards := make([][]*redis.IntCmd, len(consumers))
	slots := make([]*redis.StringCmd, len(consumers))
	for i, k := range consumers {
		for _, col := range Cols {
			cards[i] = append(cards[i], pipe.ZCard(ctx, k.Key(col)))
		}
		slots[i] = pipe.HGet(ctx, k.DesiredKey(), "slots")
	}
	if err := pipeerr.Exec(ctx, pipe); err != nil {
		return nil, fmt.Errorf("cells: %w", err)
	}
	out := make([]Cells, len(consumers))
	for i, k := range consumers {
		row := Cells{Consumer: k, Ready: cards[i][0].Val(), Working: cards[i][1].Val(), OK: cards[i][2].Val(),
			Fail: cards[i][3].Val(), Slots: -1}
		if n, err := strconv.ParseInt(slots[i].Val(), 10, 64); err == nil {
			row.Slots = n
		}
		out[i] = row
	}
	return out, nil
}

// Roster is the consumers the deal duty deals to, sorted.
func Roster(ctx context.Context, c redis.Cmdable) ([]Consumer, error) {
	names, err := c.SMembers(ctx, ConsumersKey).Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		return nil, err
	}
	sort.Strings(names)
	var out []Consumer
	for _, n := range names {
		if k, err := ParseConsumer(n); err == nil {
			out = append(out, k)
		}
	}
	return out, nil
}

// Enroll adds a consumer to (on) or removes it from the deal duty's roster.
func Enroll(ctx context.Context, c redis.Cmdable, k Consumer, on bool) error {
	if on {
		return c.SAdd(ctx, ConsumersKey, k.String()).Err()
	}
	return c.SRem(ctx, ConsumersKey, k.String()).Err()
}
