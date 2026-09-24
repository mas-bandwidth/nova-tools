// Package land is the gate-retry rule of sprint land (nova-sprint spec 4.9,
// control 42). A GATE-RED batch whose members each pass alone on a green base
// is nova-merge's EXCEPTION class=gate-red: the lane retries that batch once
// and, when the retry is green, lands it. The failing test is recorded at
// flaky:<repo>:<pkg>.<test>. The first hit files one issue; a later hit of
// the same key files nothing.
//
// A member whose mergeable word is UNKNOWN is read up to PollLimit times,
// PollGap apart. UNKNOWN twice and then MERGEABLE is kept. PollLimit UNKNOWN
// reads drop the member for this pass with reason mergeable-unknown. The
// reason is not stored: the next pass polls again, which is how that drop
// clears. This package does not take from landable, write a facts file, or
// close pull requests.
package land

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/merge"
	"github.com/mas-bandwidth/nova-tools/internal/metrics"
)

const (
	// PollLimit is how many times one member's mergeable word is read while
	// it stays UNKNOWN. The third read is inside the budget: UNKNOWN, UNKNOWN,
	// MERGEABLE is kept. A third UNKNOWN drops.
	PollLimit = 3
	// PollGap is the wait between those reads (spec 4.9). The lane calls
	// Clock.Sleep; a test clock records the gap instead of blocking.
	PollGap = 5 * time.Second
	// DropMergeableUnknown is the drop reason after PollLimit UNKNOWN reads.
	DropMergeableUnknown = "mergeable-unknown"
)

// Member is one pull request in a lane batch.
type Member struct {
	Number int
	Head   string
}

// Batch is the set the lane is gating, already taken from landable.
type Batch struct {
	Repo    string
	Name    string
	Members []Member
}

// Verdict is one gate run. A red test step names the package and the test
// the way nova-merge's BATCH FAIL line does (step=test, packages, tests).
type Verdict struct {
	OK      bool
	Step    string
	Package string
	Test    string
}

// FlakyRecord is the hash at flaky:<repo>:<pkg>.<test>: first_seen, lanes_hit,
// issue, last_at.
type FlakyRecord struct {
	FirstSeen string
	LanesHit  int
	Issue     int
	LastAt    string
}

// MemberResult is one member after the mergeable reads.
type MemberResult struct {
	Number int
	State  string
	Polls  int
	// Reason is DropMergeableUnknown when the member was dropped, else empty.
	Reason string
}

// Result is one pass of the retry rule.
type Result struct {
	Landed        bool
	LandedNumbers []int
	Runs          int
	Retries       int
	// Class is merge.ExceptionGateRed when the red batch earned the retry,
	// else empty.
	Class    string
	Kept     []MemberResult
	Dropped  []MemberResult
	FlakyKey string
	Record   FlakyRecord
	// Filed is true only when this pass created the issue. A second hit of
	// the same key leaves Filed false and keeps Record.Issue.
	Filed bool
}

// Gate runs the batch. attempt is 0 for the first run and 1 for the one retry.
type Gate interface {
	Run(ctx context.Context, batch Batch, attempt int) (Verdict, error)
}

// Bisect is the alone-on-base measurement. memberGreen is parallel to
// batch.Members: true when that member passes the failing test alone.
type Bisect interface {
	Alone(ctx context.Context, batch Batch, v Verdict) (baseGreen bool, memberGreen []bool, err error)
}

// Forge reads one member's mergeable word: MERGEABLE, CONFLICTING, or UNKNOWN.
type Forge interface {
	Mergeable(ctx context.Context, repo string, number int) (string, error)
}

// Lander lands a green batch. The lane calls it only after a green gate.
type Lander interface {
	Land(ctx context.Context, batch Batch, v Verdict) error
}

// Store is the flaky hash. Observe records one hit. file is called only for
// the first hit of key; a later hit must not call it.
type Store interface {
	Observe(ctx context.Context, key, at string, file func() (int, error)) (FlakyRecord, bool, error)
}

// Filer files one issue and returns its number.
type Filer interface {
	File(ctx context.Context, repo, title, body string) (int, error)
}

// Clock is the lane's time. Sleep is the gap between UNKNOWN re-polls.
// Tests inject a clock that records the gap and does not block.
type Clock interface {
	Now() time.Time
	Sleep(time.Duration)
}

// WallClock is the process clock. Sleep waits PollGap for real.
type WallClock struct{}

// Now returns the current UTC time.
func (WallClock) Now() time.Time { return time.Now().UTC() }

// Sleep waits d.
func (WallClock) Sleep(d time.Duration) { time.Sleep(d) }

// Lane is the collaborators the retry rule needs. Every field is required.
type Lane struct {
	Gate   Gate
	Bisect Bisect
	Forge  Forge
	Land   Lander
	Store  Store
	Filer  Filer
	Clock  Clock
	// Metrics receives the batch depth, the members admitted to the gate
	// and one latency per forge read and gate run (nx-g61); nil exports
	// nothing, and it is the one optional field.
	Metrics *metrics.Set
}

// FlakyKey is flaky:<repo>:<pkg>.<test>, the dedup key of spec 2.2.
func FlakyKey(repo, pkg, test string) string {
	return "flaky:" + repo + ":" + pkg + "." + test
}

// Run admits members, gates the ones that are MERGEABLE, and on class
// gate-red retries that same set once. A green gate lands. A red gate that
// is not class gate-red does not retry and does not land: a member that
// fails alone is not this rule's drop-and-re-gate path.
func (l Lane) Run(ctx context.Context, batch Batch) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	if l.Gate == nil || l.Bisect == nil || l.Forge == nil || l.Land == nil || l.Store == nil || l.Filer == nil || l.Clock == nil {
		return Result{}, fmt.Errorf("land: gate, bisect, forge, lander, store, filer and clock are required")
	}
	if strings.TrimSpace(batch.Repo) == "" || len(batch.Members) == 0 {
		return Result{}, fmt.Errorf("land: a batch needs a repo and a member")
	}
	l.Metrics.QueueDepth(metrics.Lander, len(batch.Members))
	kept, keptRes, dropped, err := l.admit(ctx, batch)
	if err != nil {
		return Result{}, err
	}
	l.Metrics.LeasesHeld(metrics.Lander, len(kept))
	res := Result{Kept: keptRes, Dropped: dropped}
	if len(kept) == 0 {
		return res, nil
	}
	gated := Batch{Repo: batch.Repo, Name: batch.Name, Members: kept}
	v, err := l.gate(ctx, gated, 0)
	res.Runs = 1
	if err != nil {
		return res, err
	}
	if v.OK {
		return l.land(ctx, res, gated, v)
	}
	if !namedTest(v) {
		return res, nil
	}
	baseGreen, memberGreen, err := l.Bisect.Alone(ctx, gated, v)
	if err != nil {
		return res, err
	}
	if len(memberGreen) != len(gated.Members) {
		return res, fmt.Errorf("land: bisect reported %d members, batch has %d", len(memberGreen), len(gated.Members))
	}
	if !merge.GateRedException(baseGreen, memberGreen) {
		return res, nil
	}
	res.Class = merge.ExceptionGateRed
	key := FlakyKey(batch.Repo, strings.TrimSpace(v.Package), strings.TrimSpace(v.Test))
	at := l.Clock.Now().UTC().Format(time.RFC3339)
	title, body := flakyIssue(key, batch, v)
	rec, filed, err := l.Store.Observe(ctx, key, at, func() (int, error) {
		return l.Filer.File(ctx, batch.Repo, title, body)
	})
	if err != nil {
		return res, err
	}
	res.FlakyKey = key
	res.Record = rec
	res.Filed = filed

	v2, err := l.gate(ctx, gated, 1)
	res.Runs = 2
	res.Retries = 1
	if err != nil {
		return res, err
	}
	if !v2.OK {
		return res, nil
	}
	return l.land(ctx, res, gated, v2)
}

// gate runs the batch through Gate and records its latency.
func (l Lane) gate(ctx context.Context, batch Batch, attempt int) (Verdict, error) {
	start := l.Clock.Now()
	v, err := l.Gate.Run(ctx, batch, attempt)
	l.Metrics.ProviderLatency(metrics.Lander, "gate", l.Clock.Now().Sub(start))
	return v, err
}

func (l Lane) land(ctx context.Context, res Result, batch Batch, v Verdict) (Result, error) {
	if err := l.Land.Land(ctx, batch, v); err != nil {
		return res, err
	}
	res.Landed = true
	res.LandedNumbers = make([]int, len(batch.Members))
	for i, m := range batch.Members {
		res.LandedNumbers[i] = m.Number
	}
	return res, nil
}

func (l Lane) admit(ctx context.Context, batch Batch) (kept []Member, keptRes []MemberResult, dropped []MemberResult, err error) {
	for _, m := range batch.Members {
		if m.Number <= 0 {
			return nil, nil, nil, fmt.Errorf("land: member number %d", m.Number)
		}
		res, take, err := l.readMergeable(ctx, batch.Repo, m.Number)
		if err != nil {
			return nil, nil, nil, err
		}
		if take {
			kept = append(kept, m)
			keptRes = append(keptRes, res)
			continue
		}
		dropped = append(dropped, res)
	}
	return kept, keptRes, dropped, nil
}

// readMergeable polls while the word is UNKNOWN. MERGEABLE keeps the member.
// Anything else is not a mergeable-unknown drop and is not taken: the lane
// has no rebase card.
func (l Lane) readMergeable(ctx context.Context, repo string, number int) (MemberResult, bool, error) {
	res := MemberResult{Number: number}
	for res.Polls < PollLimit {
		if err := ctx.Err(); err != nil {
			return MemberResult{}, false, err
		}
		start := l.Clock.Now()
		state, err := l.Forge.Mergeable(ctx, repo, number)
		l.Metrics.ProviderLatency(metrics.Lander, "forge", l.Clock.Now().Sub(start))
		if err != nil {
			return MemberResult{}, false, err
		}
		res.Polls++
		word := strings.ToUpper(strings.TrimSpace(state))
		switch word {
		case "MERGEABLE":
			res.State = word
			return res, true, nil
		case "UNKNOWN":
			res.State = word
			if res.Polls == PollLimit {
				res.Reason = DropMergeableUnknown
				return res, false, nil
			}
			l.Clock.Sleep(PollGap)
		default:
			return MemberResult{}, false, fmt.Errorf("land: #%d mergeable %q is not MERGEABLE or UNKNOWN", number, state)
		}
	}
	return MemberResult{}, false, fmt.Errorf("land: #%d mergeable poll did not finish", number)
}

func namedTest(v Verdict) bool {
	if v.OK || v.Step != "test" {
		return false
	}
	pkg := strings.TrimSpace(v.Package)
	test := strings.TrimSpace(v.Test)
	return pkg != "" && pkg != "none" && test != "" && test != "none"
}

func flakyIssue(key string, batch Batch, v Verdict) (string, string) {
	pkg := strings.TrimSpace(v.Package)
	test := strings.TrimSpace(v.Test)
	title := fmt.Sprintf("flaky: %s.%s fails a gate batch while every member passes alone", pkg, test)
	body := fmt.Sprintf("dedup=%s\n\nBatch %s on %s went GATE-RED on %s.%s while every member passed alone on a green base (EXCEPTION class=%s). The lane retries that batch once. A second hit of this key files nothing.\n",
		key, batch.Name, batch.Repo, pkg, test, merge.ExceptionGateRed)
	return title, body
}
