package friend

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// scriptExec answers each command with the next scripted output and exit,
// recording the last argument (the turn's text for opencode run).
type scriptExec struct {
	outs  []string
	exits []int
	texts []string
}

func (s *scriptExec) run(_ context.Context, _, _ string, args []string, _ string) (string, int, error) {
	s.texts = append(s.texts, args[len(args)-1])
	i := len(s.texts) - 1
	if i >= len(s.outs) {
		return "", 0, nil
	}
	return s.outs[i], s.exits[i], nil
}

// limitClock is the test's clock, in the zone a harness prints its reset in.
type limitClock struct{ t time.Time }

func (c *limitClock) now() time.Time { return c.t }

var edt = time.FixedZone("EDT", -4*3600)

func TestALimitedHarnessIsDownUntilItsResetThenWoken(t *testing.T) {
	t.Parallel()
	clock := &limitClock{t: time.Date(2026, 10, 4, 17, 40, 0, 0, edt)}
	var downs []string
	var woken []string
	nonces := []string{"stale1", "wake22"}
	l := &Limits{
		Now:   clock.now,
		Nonce: func() string { n := nonces[0]; nonces = nonces[1:]; return n },
		Down: func(until time.Time, reason string) {
			downs = append(downs, until.Format(time.RFC3339)+" "+reason)
		},
		Up: func(nonce string) { woken = append(woken, nonce) },
	}
	se := &scriptExec{
		outs: []string{
			"working on it\nInsufficient AI Credits. Your credits will refresh 6:52 PM.\n", // the turn that hits the limit
			"I am back: stale0\n", // the first wake answers no current nonce
			"wake22\n",            // the second does
			"I ran the card.\n",   // the message, delivered after the wake
		},
		exits: []int{1, 0, 0, 0},
	}
	d := l.Gate(&OpenCode{Dir: "/w/bob", Session: "s1", Run: l.Watch(se.run)})

	exit, err := d.Deliver(context.Background(), "hello")
	var deferred Deferred
	require.ErrorAs(t, err, &deferred, "a turn that hit the limit is deferred: the message stays in hand, counted toward nothing")
	assert.Equal(t, 0, exit)
	assert.Contains(t, deferred.Reason, "until 2026-10-04T18:52:00-04:00")
	until, reason, limited := l.Limited()
	require.True(t, limited)
	assert.Equal(t, time.Date(2026, 10, 4, 18, 52, 0, 0, edt), until, "the reset the harness printed, in its clock's zone, today")
	assert.Contains(t, reason, "Insufficient AI Credits")
	require.Equal(t, []string{"2026-10-04T18:52:00-04:00 " + reason}, downs, "the friend is sent down once, until the reset")

	clock.t = time.Date(2026, 10, 4, 18, 51, 59, 0, edt)
	_, err = d.Deliver(context.Background(), "hello")
	require.ErrorAs(t, err, &deferred, "before the reset nothing runs")
	assert.Len(t, se.texts, 1, "the harness is not run while it is limited")

	clock.t = time.Date(2026, 10, 4, 18, 52, 30, 0, edt)
	_, err = d.Deliver(context.Background(), "hello")
	require.ErrorAs(t, err, &deferred, "a wake that answers no current nonce is no wake")
	require.Len(t, se.texts, 2)
	assert.Contains(t, se.texts[1], "stale1", "the wake turn carries its nonce")
	assert.Empty(t, woken)
	_, _, limited = l.Limited()
	assert.True(t, limited, "still down until a wake answers the current nonce")

	clock.t = time.Date(2026, 10, 4, 18, 53, 0, 0, edt)
	exit, err = d.Deliver(context.Background(), "hello")
	require.NoError(t, err)
	assert.Equal(t, 0, exit)
	require.Len(t, se.texts, 4)
	assert.Contains(t, se.texts[2], "wake22")
	assert.Equal(t, "hello", se.texts[3], "after the wake the message itself is delivered")
	assert.Equal(t, []string{"wake22"}, woken, "woken once, by the current nonce")
	_, _, limited = l.Limited()
	assert.False(t, limited)
	assert.Len(t, downs, 1)
}

// claudeEvent is a Claude Code stream-json rate_limit_event line.
func claudeEvent(status string, overage bool, five, seven float64, fiveReset, sevenReset time.Time) string {
	b := map[bool]string{true: "true", false: "false"}[overage]
	return `{"type":"rate_limit_event","rate_limit_info":{"status":"` + status + `","isUsingOverage":` + b +
		`,"unifiedWindows":{"five_hour":{"utilization":` + ftoa(five) + `,"resetsAt":` + itoa(fiveReset.Unix()) +
		`},"seven_day":{"utilization":` + ftoa(seven) + `,"resetsAt":` + itoa(sevenReset.Unix()) + `}}},"session_id":"x"}`
}

func TestClaudesRateLimitEventIsTheMeasuredUsageAndItsRejectionALimit(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 4, 18, 30, 0, 0, time.UTC)
	fiveReset, sevenReset := now.Add(90*time.Minute), now.Add(50*time.Hour)

	ok := claudeEvent("allowed", false, 0.83, 0.41, fiveReset, sevenReset)
	lim, found := ReadLimit(`{"type":"system"}`+"\n"+ok+"\n", now)
	require.True(t, found)
	assert.False(t, lim.Limited, "allowed is measured usage, not a limit")
	assert.InDelta(t, 0.83, lim.Usage.FiveHour, 1e-9)
	assert.InDelta(t, 0.41, lim.Usage.SevenDay, 1e-9)
	assert.True(t, lim.Usage.FiveHourResets.Equal(fiveReset))
	assert.True(t, lim.Usage.SevenDayResets.Equal(sevenReset))
	assert.True(t, lim.Usage.At.Equal(now))

	lim, _ = ReadLimit(claudeEvent("rejected", false, 0.6, 1.0, fiveReset, sevenReset), now)
	assert.True(t, lim.Limited)
	assert.True(t, lim.Until.Equal(sevenReset), "rejected: down until the exhausted window resets")
	assert.Contains(t, lim.Reason, "seven_day")

	// paid overage reads down unless the owner allows it for this friend
	over := claudeEvent("allowed", true, 1.0, 0.7, fiveReset, sevenReset)
	l := &Limits{Now: func() time.Time { return now }}
	se := &scriptExec{outs: []string{over}, exits: []int{0}}
	d := l.Gate(&OpenCode{Dir: "/w/ada", Session: "s", Run: l.Watch(se.run)})
	_, err := d.Deliver(context.Background(), "x")
	var deferred Deferred
	require.ErrorAs(t, err, &deferred)
	until, reason, limited := l.Limited()
	assert.True(t, limited)
	assert.True(t, until.Equal(fiveReset))
	assert.Contains(t, reason, "overage")

	l = &Limits{Now: func() time.Time { return now }, AllowOverage: true}
	se = &scriptExec{outs: []string{over}, exits: []int{0}}
	d = l.Gate(&OpenCode{Dir: "/w/ada", Session: "s", Run: l.Watch(se.run)})
	exit, err := d.Deliver(context.Background(), "x")
	require.NoError(t, err, "overage allowed by the owner: the friend stays up")
	assert.Equal(t, 0, exit)
	_, _, limited = l.Limited()
	assert.False(t, limited)
}

func TestALimitLineIsReadOnlyWithItsResetAndNeverFromTheBodyOfAReply(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 4, 22, 0, 0, 0, edt)
	for _, tc := range []struct{ out, until string }{
		{"Insufficient AI Credits. Your credits will refresh 6:52 PM.", "2026-10-05T18:52:00-04:00"}, // past today: tomorrow
		{"Claude usage limit reached. Your limit will reset at 11pm.", "2026-10-04T23:00:00-04:00"},
		{"You've hit your usage limit. Try again in 2 hours.", "2026-10-05T00:00:00-04:00"},
		{"Claude AI usage limit reached|" + itoa(now.Add(3*time.Hour).Unix()), "2026-10-05T01:00:00-04:00"},
	} {
		lim, found := ReadLimit("some work\n"+tc.out+"\n", now)
		require.True(t, found, tc.out)
		assert.True(t, lim.Limited, tc.out)
		assert.Equal(t, tc.until, lim.Until.In(edt).Format(time.RFC3339), tc.out)
	}
	for _, out := range []string{
		"I read the card about usage limits and credits.",                                                        // the words alone, no reset
		`{"type":"error","error":{"type":"rate_limit_error"}}`,                                                   // a transient 429, not a limit
		"the friend is down until its usage limit resets at 6 PM\n" + strings.Repeat("more of the reply\n", 200), // far from the tail
	} {
		lim, _ := ReadLimit(out, now)
		assert.False(t, lim.Limited, out)
	}
	assert.True(t, errors.As(Deferred{Reason: "x"}, new(Deferred)))
}

func ftoa(f float64) string { return strconv.FormatFloat(f, 'f', -1, 64) }
func itoa(n int64) string   { return strconv.FormatInt(n, 10) }

// A harness that opens a session per lane keeps its lanes under the gate: its
// batch turn is held while it is down, and a lane turn goes straight to it,
// its output still read for the limit (gatedLanes says why lanes are exempt).
func TestTheGateKeepsALaneHarnessAndHoldsOnlyItsBatchTurn(t *testing.T) {
	t.Parallel()
	clock := &limitClock{t: time.Date(2026, 10, 4, 17, 40, 0, 0, edt)}
	var downs []string
	l := &Limits{Now: clock.now, Down: func(until time.Time, reason string) { downs = append(downs, until.Format(time.RFC3339)) }}
	se := &scriptExec{
		outs:  []string{"lane work\nYou've hit your usage limit. Try again in 2 hours.\n", "lane again\n"},
		exits: []int{0, 0},
	}
	d := l.Gate(&OpenCode{Dir: t.TempDir(), Session: "s1", Run: l.Watch(se.run)})
	lh, ok := d.(LaneHarness)
	require.True(t, ok, "the gate keeps the harness's lanes")

	_, err := lh.DeliverTo(context.Background(), "ses_lane1", "card c1")
	var usage UsageLimited
	require.ErrorAs(t, err, &usage, "a lane turn's own end is its answer: the lanes pause until the reset")
	assert.NotErrorAs(t, err, new(Deferred), "never the gate's deferral")
	assert.Equal(t, []string{"2026-10-04T19:40:00-04:00"}, downs, "a lane turn that hits the limit sends the friend down")

	_, err = d.Deliver(context.Background(), "hello")
	var deferred Deferred
	require.ErrorAs(t, err, &deferred, "the batch turn is held while she is down")
	assert.Len(t, se.texts, 1, "the held batch turn runs nothing")

	_, err = lh.DeliverTo(context.Background(), "ses_lane1", "card c1 again")
	require.NoError(t, err)
	assert.Equal(t, []string{"card c1", "card c1 again"}, se.texts, "the lanes are not held")
}

// A harness that stops on its credits (the finding of 2026-10-04: a friend's
// harness stopped on "Insufficient AI Credits ... will refresh 6:52 PM" while
// her row read up with six working cards, because the beat came from the
// daemon, not from the harness) makes its friend down with the reset as
// --until: no beat goes out and nothing is delivered from the turn that said
// it; a reset that passes while the harness is not running keeps her down,
// since only an answered wake proves the session; then the wake is answered,
// she is up, beats, and the message goes in (docs/SPEC-FRIEND.md, a harness
// at its limit).
func TestAHarnessOutOfCreditsMakesItsFriendDownUntilTheReset(t *testing.T) {
	t.Parallel()
	clock := &limitClock{t: time.Date(2026, 10, 4, 17, 40, 0, 0, edt)}
	type told struct {
		until  time.Time
		reason string
	}
	var downs []told
	var ups []string
	nonces := []string{"gone11", "back22"}
	l := &Limits{
		Now:   clock.now,
		Nonce: func() string { n := nonces[0]; nonces = nonces[1:]; return n },
		Down:  func(until time.Time, reason string) { downs = append(downs, told{until, reason}) },
		Up:    func(nonce string) { ups = append(ups, nonce) },
	}
	// the fake harness: out of credits, then not running, then back
	running := true
	var texts []string
	harness := func(_ context.Context, _, _ string, args []string, _ string) (string, int, error) {
		text := args[len(args)-1]
		if !running {
			return "", -1, errors.New("exec: the harness app is not running")
		}
		texts = append(texts, text)
		switch {
		case len(texts) == 1:
			return "working on card c1\nError: Insufficient AI Credits. Your credits will refresh 6:52 PM.\n", 1, nil
		case strings.Contains(text, "back22"):
			return "back22\n", 0, nil
		}
		return "I ran card c1.\n", 0, nil
	}
	d := l.Gate(&OpenCode{Dir: "/w/bob", Session: "s1", Run: l.Watch(harness)})
	beats := 0
	beat := l.Beat(func(context.Context) error { beats++; return nil })

	require.NoError(t, beat(context.Background()), "up before her harness says anything")
	_, err := d.Deliver(context.Background(), "card c1")
	var deferred Deferred
	require.ErrorAs(t, err, &deferred, "the turn that hit the limit is deferred, the message in hand")
	reset := time.Date(2026, 10, 4, 18, 52, 0, 0, edt)
	require.Len(t, downs, 1, "told down once")
	assert.True(t, reset.Equal(downs[0].until), "--until is the reset the harness said: %s", downs[0].until)
	assert.Contains(t, downs[0].reason, "Insufficient AI Credits")
	require.Error(t, beat(context.Background()), "no beat while she is down: her row reads down")
	assert.Equal(t, 1, beats)

	clock.t = reset.Add(-time.Second)
	_, err = d.Deliver(context.Background(), "card c1")
	require.ErrorAs(t, err, &deferred)
	assert.Len(t, texts, 1, "nothing runs before the reset")

	// the reset passes while the harness is not running: no answer, no wake
	clock.t, running = reset.Add(time.Minute), false
	_, err = d.Deliver(context.Background(), "card c1")
	require.ErrorAs(t, err, &deferred, "a wake into an absent harness is no wake")
	assert.Empty(t, ups)
	require.Error(t, beat(context.Background()), "still down after the reset until the session answers")
	assert.Equal(t, 1, beats)

	clock.t, running = reset.Add(2*time.Minute), true
	_, err = d.Deliver(context.Background(), "card c1")
	require.NoError(t, err)
	assert.Equal(t, []string{"back22"}, ups, "woken once, by the answered nonce")
	require.Len(t, texts, 3)
	assert.Contains(t, texts[1], "back22", "the wake carries its nonce")
	assert.Equal(t, "card c1", texts[2], "then the message goes in")
	require.NoError(t, beat(context.Background()), "she beats again once woken")
	assert.Equal(t, 2, beats)
	assert.Len(t, downs, 1)

	subject, body := LimitDownText("bob", downs[0].until, downs[0].reason)
	assert.Contains(t, subject, "friend bob down")
	assert.Contains(t, body, "nova-sprint friend down bob --reason 'harness limit: Error: Insufficient AI Credits. Your credits will refresh 6:52 PM.' --until 2026-10-04T22:52:00Z")
	_, body = LimitUpText("bob")
	assert.Contains(t, body, "nova-sprint friend up bob")
}

// A provider failure is one typed record per attempt (kind, status where the
// harness exposed it, retry-after, request id) and the limit layer is driven by
// its kind, never by the text again (docs/SPEC-FRIEND.md, a provider failure is
// typed): a 429 with a reset in 90 s parks the lanes until the reset plus slack
// and the beat reports the limit; an out-of-funds exit parks with no wake time,
// never a guessed one.
func TestAProviderFailureIsTypedAndDrivesTheLimitLayer(t *testing.T) {
	t.Parallel()
	clock := &limitClock{t: time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)}

	// the typed record, read once from the turn's own text
	out := "working on the card\n429 Too Many Requests\nretry-after: 90\n" + `{"request_id":"req_abc123"}` + "\n"
	f, ok := ClassifyProviderFailure("ses_1", out, clock.now())
	require.True(t, ok)
	assert.Equal(t, FailureRateLimited, f.Kind)
	assert.Equal(t, 429, f.Status)
	assert.Equal(t, 90*time.Second, f.RetryAfter)
	assert.Equal(t, "req_abc123", f.RequestID)

	// the layer parks the lanes until the reset plus slack and records the kind
	var records []string
	nonces := []string{"wake01"}
	l := &Limits{
		Now:     clock.now,
		Harness: "opencode", // the daemon's --harness: the typed path must still govern
		Nonce:   func() string { n := nonces[0]; nonces = nonces[1:]; return n },
		Slack:   10 * time.Second,
		Record:  func(line string) { records = append(records, line) },
	}
	se := &scriptExec{outs: []string{out, "wake01\n", "ran it\n"}, exits: []int{1, 0, 0}}
	d := l.Gate(&OpenCode{Dir: "/w/f", Session: "s1", Run: l.Watch(se.run)})

	exit, err := d.Deliver(context.Background(), "card c1")
	var deferred Deferred
	require.ErrorAs(t, err, &deferred, "the turn that hit the provider limit is deferred, the message in hand")
	assert.Equal(t, 0, exit)
	assert.Equal(t, FailureRateLimited, l.Kind(), "driven by the classified kind")
	until, reason, limited := l.Limited()
	require.True(t, limited)
	assert.Equal(t, clock.t.Add(90*time.Second+10*time.Second), until, "the reset the provider named plus slack")
	assert.Contains(t, reason, "429 Too Many Requests")
	require.Len(t, records, 1, "one typed record for the attempt")
	assert.Contains(t, records[0], "kind=rate-limited")
	assert.Contains(t, records[0], "status=429")
	assert.Contains(t, records[0], "retry-after=1m30s")
	assert.Contains(t, records[0], "request-id=req_abc123")

	// the beat reports the limit, never an up beat while it stands
	var beats, downs int
	err = l.BeatOrDown(func(context.Context) error { beats++; return nil },
		func(_ context.Context, u time.Time, r string) error { downs++; return nil })(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 0, beats)
	assert.Equal(t, 1, downs)

	// nothing runs before the reset plus slack, then a wake answers and the message goes in
	clock.t = until.Add(-time.Second)
	_, err = d.Deliver(context.Background(), "card c1")
	require.ErrorAs(t, err, &deferred)
	assert.Len(t, se.texts, 1, "the harness is not run while parked")
	clock.t = until
	exit, err = d.Deliver(context.Background(), "card c1")
	require.NoError(t, err)
	assert.Equal(t, 0, exit)
	require.Len(t, se.texts, 3)
	assert.Contains(t, se.texts[1], "wake01", "the wake carries its nonce")
	assert.Equal(t, "card c1", se.texts[2], "then the message goes in")
	_, _, limited = l.Limited()
	assert.False(t, limited, "up after the wake answered")

	// out of funds: a gate with no wake time, never a guessed one
	clock.t = time.Date(2026, 10, 9, 13, 0, 0, 0, time.UTC)
	var records2 []string
	l2 := &Limits{Now: clock.now, Harness: "opencode", Nonce: func() string { return "wake01" }, Record: func(line string) { records2 = append(records2, line) }}
	se2 := &scriptExec{outs: []string{"402 Payment Required: insufficient balance\n"}, exits: []int{1}}
	d2 := l2.Gate(&OpenCode{Dir: "/w/f", Session: "s1", Run: l2.Watch(se2.run)})
	_, err = d2.Deliver(context.Background(), "card c2")
	require.ErrorAs(t, err, &deferred)
	until2, _, limited2 := l2.Limited()
	require.True(t, limited2)
	assert.True(t, until2.IsZero(), "out of funds names no reset: no wake time, no guess")
	require.Len(t, records2, 1)
	assert.Contains(t, records2[0], "kind=out-of-funds")
	assert.Contains(t, records2[0], "retry-after=none")
	clock.t = clock.t.Add(48 * time.Hour)
	_, err = d2.Deliver(context.Background(), "card c2")
	require.ErrorAs(t, err, &deferred, "no wake, however long the wait")
	assert.Len(t, se2.texts, 1, "the harness is never run for a guessed wake")
}

// A limit with no reset known is one record line saying until=none, said once
// while it stands, then session=ok when it clears: a zero reset cannot be told
// from "no limit said" by the reset alone (a-provider-failure-is-typed.w1).
func TestTheDaemonRecordSaysUntilNoneForALimitWithNoWakeTime(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	var records []string
	d := &Daemon{
		Friend:  "bob",
		m:       Start(now),
		Now:     func() time.Time { return now },
		Limited: func() (string, time.Time, bool) { return FailureOutOfFunds, time.Time{}, true },
		Record:  func(line string) { records = append(records, line) },
		Status:  func(Status) error { return nil },
	}
	d.flush(now)
	require.Len(t, records, 1)
	assert.Contains(t, records[0], "session=limited kind=out-of-funds until=none")
	d.flush(now)
	require.Len(t, records, 1, "said once while it stands")
	d.Limited = func() (string, time.Time, bool) { return "", time.Time{}, false }
	d.flush(now)
	require.Len(t, records, 2)
	assert.Contains(t, records[1], "session=ok: the harness answered after its reset")
}
