package fleetkube

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Lanes, in take order: the lanes are the order.
const (
	LaneRed   = "red"
	LaneGreen = "green"
	LaneSmall = "small"
	LaneNext  = "next"
)

// Lanes is the take order.
var Lanes = []string{LaneRed, LaneGreen, LaneSmall, LaneNext}

// Queue is the work queue on a shared volume: <root>/queue/lanes/<lane>/ and
// <root>/queue/taken/, the layout nova-pulse cut writes.
type Queue struct{ root string }

// OpenQueue creates (if absent) the queue layout under root.
func OpenQueue(root string) (*Queue, error) {
	q := &Queue{root: root}
	for _, l := range Lanes {
		if err := os.MkdirAll(q.lane(l), 0o755); err != nil {
			return nil, err
		}
	}
	if err := os.MkdirAll(q.taken(), 0o755); err != nil {
		return nil, err
	}
	return q, nil
}

func (q *Queue) lane(l string) string { return filepath.Join(q.root, "queue", "lanes", l) }
func (q *Queue) taken() string        { return filepath.Join(q.root, "queue", "taken") }

// Put writes a card into a lane.
func (q *Queue) Put(lane, name string, body []byte) error {
	if !validLane(lane) {
		return fmt.Errorf("fleetkube: unknown lane %q", lane)
	}
	return os.WriteFile(filepath.Join(q.lane(lane), name+".card"), body, 0o644)
}

// Taken is a card held by one worker.
type Taken struct {
	Worker, Name, Lane string
}

func (q *Queue) takenCard(t Taken) string {
	return filepath.Join(q.taken(), t.Worker+"-"+t.Name+".card")
}
func (q *Queue) takenLane(t Taken) string {
	return filepath.Join(q.taken(), t.Worker+"-"+t.Name+".lane")
}

// Take takes the first card in lane order by
// rename(lanes/<lane>/<name>.card, taken/<worker>-<name>.card). The rename is
// the lock: a card another puller renamed first is skipped. ok is false when
// every lane is empty.
func (q *Queue) Take(worker string) (Taken, bool, error) {
	for _, l := range Lanes {
		ents, err := os.ReadDir(q.lane(l))
		if err != nil {
			return Taken{}, false, err
		}
		names := make([]string, 0, len(ents))
		for _, e := range ents {
			if strings.HasSuffix(e.Name(), ".card") {
				names = append(names, strings.TrimSuffix(e.Name(), ".card"))
			}
		}
		sort.Strings(names)
		for _, n := range names {
			t := Taken{Worker: worker, Name: n, Lane: l}
			err := os.Rename(filepath.Join(q.lane(l), n+".card"), q.takenCard(t))
			if errors.Is(err, fs.ErrNotExist) {
				continue // another puller's rename won
			}
			if err != nil {
				return Taken{}, false, err
			}
			// Record the lane it came from so a died card goes back there.
			if err := os.WriteFile(q.takenLane(t), []byte(l), 0o644); err != nil {
				return t, true, err
			}
			return t, true, nil
		}
	}
	return Taken{}, false, nil
}

// Job phases the puller observes.
const (
	PhaseRunning   = "Running"
	PhaseSucceeded = "Succeeded"
	PhaseFailed    = "Failed"
)

// FakeJob is the puller's view of a card's Job: its phase, whether the clip
// ran, and the job dir holding any partial RESULT.md.
type FakeJob struct {
	Taken   Taken
	Phase   string
	Clipped bool
	Dir     string
}

// EvidencePath is where a died card's partial RESULT.md is kept.
func (q *Queue) EvidencePath(t Taken) string {
	return filepath.Join(q.root, "evidence", t.Worker+"-"+t.Name+".RESULT.md")
}

// Reconcile returns every card whose Job died before its clip to the lane it
// came from, keeping its partial RESULT.md as evidence. A card already
// returned is not in taken/ and is skipped, so observing twice returns once.
func (q *Queue) Reconcile(jobs []FakeJob) ([]Taken, error) {
	var back []Taken
	for _, j := range jobs {
		if j.Phase != PhaseFailed || j.Clipped {
			continue
		}
		t := j.Taken
		if _, err := os.Stat(q.takenCard(t)); errors.Is(err, fs.ErrNotExist) {
			continue
		}
		lane := t.Lane
		if b, err := os.ReadFile(q.takenLane(t)); err == nil && validLane(string(b)) {
			lane = string(b)
		}
		if !validLane(lane) {
			return back, fmt.Errorf("fleetkube: %s-%s: no lane to return to", t.Worker, t.Name)
		}
		if j.Dir != "" {
			if b, err := os.ReadFile(filepath.Join(j.Dir, "RESULT.md")); err == nil {
				if err := os.MkdirAll(filepath.Dir(q.EvidencePath(t)), 0o755); err != nil {
					return back, err
				}
				if err := os.WriteFile(q.EvidencePath(t), b, 0o644); err != nil {
					return back, err
				}
			}
		}
		if err := os.Rename(q.takenCard(t), filepath.Join(q.lane(lane), t.Name+".card")); err != nil {
			return back, err
		}
		_ = os.Remove(q.takenLane(t))
		t.Lane = lane
		back = append(back, t)
	}
	return back, nil
}

func validLane(l string) bool {
	for _, x := range Lanes {
		if x == l {
			return true
		}
	}
	return false
}
