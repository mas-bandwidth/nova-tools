package life_test

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/life"
)

// evBase is the fixtures' epoch (UTC ms); every timeline is an offset from it.
const evBase int64 = 1790000000000

// evAt is the clock at a timeline offset in milliseconds.
func evAt(ms int64) time.Time { return time.UnixMilli(evBase + ms) }

// timeline builds events in append order: a beat every 10 s over
// [0, beatsTo] s, plus extra events, sorted by at (ties keep the extra after
// the beat). Ids are "<at>-<n>", the shape Redis gives.
func timeline(beatsTo int64, extra ...life.Event) []life.Event {
	var evs []life.Event
	for s := int64(0); s <= beatsTo; s += 10 {
		evs = append(evs, life.Event{Kind: life.EventBeat, At: evBase + s*1000})
	}
	for _, e := range extra {
		e.At += evBase
		evs = append(evs, e)
	}
	sort.SliceStable(evs, func(i, j int) bool { return evs[i].At < evs[j].At })
	for i := range evs {
		if evs[i].ID == "" {
			evs[i].ID = fmt.Sprintf("%d-%d", evs[i].At, i)
		}
	}
	return evs
}

func mustClass(t *testing.T, evs []life.Event, atMS int64, want string) {
	t.Helper()
	if got := life.Classify(evs, evAt(atMS)).State; got != want {
		t.Fatalf("at %d ms: %s, want %s", atMS, got, want)
	}
}

// TestProcessBeatsWithoutTurnStartIsOfflineModel: 3,960 beats every 10 s
// for 11 h and no turn-start (Stella's 11 h idle) is OFFLINE-MODEL at every
// minute from minute 20 to hour 11, never UP: a beat is process life only.
func TestProcessBeatsWithoutTurnStartIsOfflineModel(t *testing.T) {
	t.Parallel()

	f, err := os.Open("testdata/beats-11h-no-turns.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var evs []life.Event
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var e life.Event
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
			t.Fatal(err)
		}
		if e.Kind != life.EventBeat {
			t.Fatalf("fixture holds a %s event", e.Kind)
		}
		evs = append(evs, e)
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	if len(evs) != 3960 {
		t.Fatalf("fixture holds %d beats, want 3960", len(evs))
	}
	start := evs[0].At
	for m := int64(20); m <= 11*60; m++ {
		now := time.UnixMilli(start + m*60000)
		if got := life.Classify(evs, now).State; got != life.ClassOfflineModel {
			t.Fatalf("minute %d: %s, want %s", m, got, life.ClassOfflineModel)
		}
	}
}

// TestScheduledWakeWithoutTurnEscalates: a delivery D with no caused
// turn-start is WAKE-PENDING through exactly 120,000 ms and WAKE-MISSED from
// 120,001 ms; an uncaused turn-start never answers it.
func TestScheduledWakeWithoutTurnEscalates(t *testing.T) {
	t.Parallel()

	d := life.Event{ID: "d1", Kind: life.EventDeliver, At: 30000}
	const dAt = 30000
	evs := timeline(300, d)
	mustClass(t, evs, dAt+119000, life.ClassWakePending)
	mustClass(t, evs, dAt+120000, life.ClassWakePending)
	mustClass(t, evs, dAt+120001, life.ClassWakeMissed)
	mustClass(t, evs, dAt+121000, life.ClassWakeMissed)
	if c := life.Classify(evs, evAt(dAt+121000)); c.Basis != "d1" {
		t.Fatalf("WAKE-MISSED basis %q, want the delivery d1", c.Basis)
	}
	uncaused := timeline(300, d, life.Event{Kind: life.EventTurnStart, At: dAt + 30000})
	mustClass(t, uncaused, dAt+121000, life.ClassWakeMissed)
	other := timeline(300, d, life.Event{Kind: life.EventTurnStart, Cause: "d0", At: dAt + 30000})
	mustClass(t, other, dAt+121000, life.ClassWakeMissed)
}

// TestWakeMissedRecoversOnLaterCausedTurn: D1 at 0 s is never answered; D2
// at 300 s supersedes it. Only the newest delivery is consulted.
func TestWakeMissedRecoversOnLaterCausedTurn(t *testing.T) {
	t.Parallel()

	d1 := life.Event{ID: "d1", Kind: life.EventDeliver, At: 0}
	d2 := life.Event{ID: "d2", Kind: life.EventDeliver, At: 300000}
	turn := func(atMS int64) life.Event {
		return life.Event{ID: "t2", Kind: life.EventTurnStart, Cause: "d2", At: atMS}
	}
	t.Run("pending", func(t *testing.T) {
		evs := timeline(600, d1, d2)
		mustClass(t, evs, 121000, life.ClassWakeMissed)
		mustClass(t, evs, 330000, life.ClassWakePending)
	})
	t.Run("answered", func(t *testing.T) {
		evs := timeline(600, d1, d2, turn(360000))
		mustClass(t, evs, 121000, life.ClassWakeMissed)
		mustClass(t, evs, 330000, life.ClassWakePending)
		mustClass(t, evs, 361000, life.ClassUp)
	})
	t.Run("turn_at_120s", func(t *testing.T) {
		evs := timeline(600, d1, d2, turn(420000))
		for ms := int64(300000); ms <= 420000; ms += 500 {
			if got := life.Classify(evs, evAt(ms)).State; got == life.ClassWakeMissed {
				t.Fatalf("at %d ms: WAKE-MISSED for D2 answered at exactly 120 s", ms)
			}
		}
		mustClass(t, evs, 420000, life.ClassUp)
	})
	t.Run("turn_at_121s", func(t *testing.T) {
		evs := timeline(600, d1, d2, turn(421000))
		mustClass(t, evs, 420500, life.ClassWakeMissed)
		mustClass(t, evs, 421000, life.ClassUp)
	})
}

// TestWakeModeRequiresFiringReceipt: a receipt is a deliver D and a
// turn-start with cause D at most 120,000 ms later; the newest one wins.
func TestWakeModeRequiresFiringReceipt(t *testing.T) {
	t.Parallel()

	d := life.Event{ID: "d1", Kind: life.EventDeliver, At: 0}
	if _, ok := life.FindReceipt(timeline(200, d, life.Event{Kind: life.EventTurnStart, At: 5000})); ok {
		t.Fatal("a bare turn-start is a receipt")
	}
	if _, ok := life.FindReceipt(timeline(200, d, life.Event{Kind: life.EventTurnStart, Cause: "d1", At: 121000})); ok {
		t.Fatal("a caused turn-start at 121 s is a receipt")
	}
	rc, ok := life.FindReceipt(timeline(200, d, life.Event{ID: "t1", Kind: life.EventTurnStart, Cause: "d1", At: 120000}))
	if !ok || rc.LagMS != 120000 || rc.Deliver.ID != "d1" || rc.Turn.ID != "t1" {
		t.Fatalf("turn at exactly 120 s: %+v %v", rc, ok)
	}
	rc, ok = life.FindReceipt(timeline(200, d, life.Event{ID: "t1", Kind: life.EventTurnStart, Cause: "d1", At: 60000}))
	if !ok || rc.LagMS != 60000 {
		t.Fatalf("turn at 60 s: %+v %v", rc, ok)
	}
	two := timeline(600, d,
		life.Event{ID: "t1", Kind: life.EventTurnStart, Cause: "d1", At: 60000},
		life.Event{ID: "d2", Kind: life.EventDeliver, At: 300000},
		life.Event{ID: "t2", Kind: life.EventTurnStart, Cause: "d2", At: 310000})
	rc, ok = life.FindReceipt(two)
	if !ok || rc.Deliver.ID != "d2" || rc.Turn.ID != "t2" || rc.LagMS != 10000 {
		t.Fatalf("two receipts: %+v %v, want the newest (d2, t2)", rc, ok)
	}
}

// TestUsageLimitEventClassifiesOutOfCredits: a usage-limit in the window
// with no later turn-start is OUT-OF-CREDITS (before DOWN); a later
// turn-start clears it. This is the event contract; automatic capture is
// #3185's.
func TestUsageLimitEventClassifiesOutOfCredits(t *testing.T) {
	t.Parallel()

	t.Run("active", func(t *testing.T) {
		evs := timeline(120,
			life.Event{Kind: life.EventTurnStart, At: 10000},
			life.Event{ID: "u1", Kind: life.EventUsageLimit, At: 60000})
		c := life.Classify(evs, evAt(120000))
		if c.State != life.ClassOutOfCredits || c.Basis != "u1" {
			t.Fatalf("%+v, want OUT-OF-CREDITS on u1", c)
		}
		if want := evBase + 120000 + life.OfflineModelWindow.Milliseconds(); c.Until != want {
			t.Fatalf("until %d, want now + window %d", c.Until, want)
		}
		reset := timeline(120, life.Event{ID: "u1", Kind: life.EventUsageLimit, At: 60000, Reset: evBase + 3600000})
		if c := life.Classify(reset, evAt(120000)); c.State != life.ClassOutOfCredits || c.Until != evBase+3600000 {
			t.Fatalf("with reset: %+v", c)
		}
		// Rule 1 comes before DOWN: no beat at all still reads out of credits.
		alone := []life.Event{{ID: "u1", Kind: life.EventUsageLimit, At: evBase + 60000}}
		mustClass(t, alone, 120000, life.ClassOutOfCredits)
		// Outside the 20 min window it no longer counts.
		mustClass(t, timeline(1500, life.Event{Kind: life.EventTurnStart, At: 1000},
			life.Event{Kind: life.EventUsageLimit, At: 2000}, life.Event{Kind: life.EventTurnStart, At: 1400000}), 1500000, life.ClassUp)
	})
	t.Run("cleared_by_later_turn", func(t *testing.T) {
		evs := timeline(120,
			life.Event{Kind: life.EventUsageLimit, At: 60000},
			life.Event{Kind: life.EventTurnStart, At: 90000})
		mustClass(t, evs, 120000, life.ClassUp)
	})
}
