package sprint

import (
	"github.com/stretchr/testify/assert"
	"testing"
	"time"
)

func TestThePushSetRejectsStaleForeignFutureAndFailedProofs(t *testing.T) {
	t.Parallel()
	now := time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)
	for _, source := range []string{"bus", "friends", "transitions"} {
		for _, mutation := range []string{"boundary", "stale", "future", "old-seat", "old-epoch", "old-target", "old-session", "failed"} {
			t.Run(source+"/"+mutation, func(t *testing.T) {
				t.Parallel()
				set := SeatPushSet{PushRecord: PushRecord{Name: "seat-a", Harness: "codex", Target: "job", Session: "session-a", Proven: now, PongOf: "n1"}, Watches: map[string]SeatWatchProof{}}
				for _, s := range []string{"bus", "friends", "transitions"} {
					set.Watches[s] = SeatWatchProof{At: now, Epoch: 1, Generation: 2, Target: set.Target, Session: set.Session}
				}
				p := set.Watches[source]
				switch mutation {
				case "boundary":
					p.At = now.Add(-3 * SeatPushPeriods()[source])
				case "stale":
					p.At = now.Add(-3*SeatPushPeriods()[source] - time.Second)
				case "future":
					p.At = now.Add(time.Second)
				case "old-seat":
					p.Generation--
				case "old-epoch":
					p.Epoch--
				case "old-target":
					p.Target = "another-job"
				case "old-session":
					p.Session = "another-session"
				case "failed":
					p.Failed = "delivery failed"
				}
				set.Watches[source] = p
				for _, r := range SeatPushLines(set, true, 1, 2, now) {
					if r.Source == source {
						assert.Equal(t, mutation == "boundary", r.Live)
						assert.NotEmpty(t, r.Command)
					} else {
						assert.True(t, r.Live, r.Source)
					}
				}
			})
		}
	}
}

func TestAWatcherBeatCannotStandForTheJudgmentsNonceProof(t *testing.T) {
	t.Parallel()
	now := time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)
	set := SeatPushSet{PushRecord: PushRecord{Name: "seat-a", Harness: "codex"}, Watches: map[string]SeatWatchProof{}}
	for _, s := range []string{"bus", "friends", "transitions"} {
		set.Watches[s] = SeatWatchProof{At: now, Generation: 1}
	}
	lines := SeatPushLines(set, true, 0, 1, now)
	assert.False(t, lines[0].Live)
	set.Proven, set.PongOf = now.Add(time.Second), "n1"
	assert.False(t, SeatPushLines(set, true, 0, 1, now)[0].Live, "a future session receipt proves nothing")
}
