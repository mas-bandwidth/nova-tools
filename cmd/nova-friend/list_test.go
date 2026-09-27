package main

import (
	"testing"
	"time"
)

// TestFriendViewJudgesUpFromTheBeatsAge: up is a beat at most a minute old
// with no away flag; away is the flag whatever the beat; down is the rest.
// Keys do not expire; the reader judges.
func TestFriendViewJudgesUpFromTheBeatsAge(t *testing.T) {
	t.Parallel()

	now := time.Unix(1790186400, 0)
	rows := []struct {
		v    friendView
		want string
		age  string
	}{
		{friendView{BeatAt: now.Add(-30 * time.Second)}, "up", "30s"},
		{friendView{BeatAt: now.Add(-60 * time.Second)}, "up", "1m0s"},
		{friendView{BeatAt: now.Add(-61 * time.Second)}, "down", "1m1s"},
		{friendView{}, "down", "-"},
		{friendView{BeatAt: now.Add(-1 * time.Second), IsAway: true, Away: "vacation"}, "away", "1s"},
		{friendView{BeatAt: now.Add(5 * time.Second)}, "up", "0s"},
	}
	for _, r := range rows {
		if got := r.v.state(now); got != r.want {
			t.Errorf("%+v: state %s, want %s", r.v, got, r.want)
		}
		if got := r.v.beatAge(now); got != r.age {
			t.Errorf("%+v: age %s, want %s", r.v, got, r.age)
		}
	}
}

// TestListAndShowLinesEndWithTheAppliedRevision: the two typed lines, every
// field, a dash for what is not there, rev last.
func TestListAndShowLinesEndWithTheAppliedRevision(t *testing.T) {
	t.Parallel()

	now := time.Unix(1790186400, 0)
	v := friendView{Name: "rowan", Slots: "4", Tiers: "frontier", Roles: "coordinator", Working: 2,
		Host: "studio", Harness: "claude code", Session: "s1", Load: "1.25", Models: "opus", BeatAt: now.Add(-3 * time.Second)}
	if got := v.listLine(now, "7"); got != "FRIEND name=rowan state=up slots=4 tiers=frontier roles=coordinator host=studio working=2 rev=7" {
		t.Fatalf("list: %s", got)
	}
	if got := v.showLine(now, "7"); got != `FRIEND name=rowan state=up slots=4 tiers=frontier roles=coordinator host=studio working=2 session=s1 harness="claude code" load=1.25 models=opus beat=3s away=- rev=7` {
		t.Fatalf("show: %s", got)
	}
	bare := friendView{Name: "stella", IsAway: true, Away: "out of credits"}
	if got := bare.listLine(now, ""); got != "FRIEND name=stella state=away slots=- tiers=- roles=- host=- working=0 rev=-" {
		t.Fatalf("bare list: %s", got)
	}
	if got := bare.showLine(now, ""); got != `FRIEND name=stella state=away slots=- tiers=- roles=- host=- working=0 session=- harness=- load=- models=- beat=- away="out of credits" rev=-` {
		t.Fatalf("bare show: %s", got)
	}
}
