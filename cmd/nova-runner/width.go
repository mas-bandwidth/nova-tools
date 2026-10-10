package main

import (
	"fmt"
	"strings"
	"time"
)

// The fill, width and beat decisions. A work lane is two half-slots and a read
// lane is one; width N is 2N half-slots. Eight work and four reads are ten slots
// (twenty half-slots). No clock and no socket: the process hands the inputs in.
// docs/SPEC-RUNNER.md.

const (
	KindWork = "work"
	KindRead = "read"

	// FaultPrefix is the report line of a lane that ended with no report.
	FaultPrefix = "harness fault: "

	// RowEvery is how often the row is read. A tick sooner keeps the row it has.
	RowEvery = 10 * time.Second
)

// Lane is one card the runner is holding. Kind is KindWork or KindRead.
type Lane struct {
	ID   string
	Kind string
}

// Lanes is the set of lanes in the order they started. A width change never
// removes one; a lane leaves only when the world says it ended.
type Lanes struct {
	list []Lane
}

// Width is the friend's row, in whole slots. It is the only max.
type Width int

func halvesOf(kind string) int {
	if kind == KindRead {
		return 1
	}
	return 2
}

func (w Width) halves() int {
	if w <= 0 {
		return 0
	}
	return int(w) * 2
}

func (l Lanes) clone() Lanes {
	return Lanes{list: append([]Lane(nil), l.list...)}
}

// List is a copy of the lanes in start order.
func (l Lanes) List() []Lane { return l.clone().list }

// IDs is the card ids in start order.
func (l Lanes) IDs() []string {
	out := make([]string, len(l.list))
	for i, n := range l.list {
		out[i] = n.ID
	}
	return out
}

// Len is the number of lanes, the true lane count.
func (l Lanes) Len() int { return len(l.list) }

// Busy is the occupied half-slots.
func (l Lanes) Busy() int {
	n := 0
	for _, ln := range l.list {
		n += halvesOf(ln.Kind)
	}
	return n
}

// Has reports whether id is already a lane.
func (l Lanes) Has(id string) bool {
	for _, n := range l.list {
		if n.ID == id {
			return true
		}
	}
	return false
}

// Add returns the lanes plus n.
func (l Lanes) Add(n Lane) Lanes {
	out := l.clone()
	out.list = append(out.list, n)
	return out
}

// Without returns the lanes with those ids removed. The order of the rest holds.
func (l Lanes) Without(ids ...string) Lanes {
	if len(ids) == 0 {
		return l.clone()
	}
	drop := make(map[string]bool, len(ids))
	for _, id := range ids {
		drop[id] = true
	}
	out := Lanes{}
	for _, n := range l.list {
		if !drop[n.ID] {
			out.list = append(out.list, n)
		}
	}
	return out
}

// Card is one card the queue offered, in the queue's order.
type Card struct {
	ID       string `json:"id"`
	Kind     string `json:"kind"`
	Tier     string `json:"tier,omitempty"`
	Model    string `json:"model,omitempty"`
	Deadline int    `json:"deadline,omitempty"`
	Job      string `json:"job,omitempty"`
	Epoch    uint64 `json:"epoch,omitempty"`
	Attempt  int    `json:"attempt,omitempty"`
	Gen      int    `json:"gen,omitempty"`
	Brief    string `json:"brief,omitempty"`
	Repo     string `json:"repo,omitempty"`
	Base     string `json:"base,omitempty"`
	BaseSha  string `json:"base_sha,omitempty"`
	Branch   string `json:"branch,omitempty"`
	Stream   string `json:"stream,omitempty"`
	Col      string `json:"col,omitempty"`
	// A read card's: the work it reads, at its pinned branch and head.
	Primary    string `json:"primary,omitempty"`
	Head       string `json:"head,omitempty"`
	WorkBranch string `json:"work_branch,omitempty"`
	WorkBase   string `json:"work_base,omitempty"`
	Report     string `json:"report,omitempty"`
}

// End is a lane that stopped. HasReport is false when the harness wrote nothing
// the runner can finish from; ErrLine is then the harness's first error line.
// For a read lane, Verdict is "ok" or "broken" and Finding names the defect the
// report's HOLD named.
type End struct {
	ID        string
	HasReport bool
	Verdict   string
	Head      string
	Finding   string
	Report    string
	ErrLine   string
}

// Fail is the finish of a lane that wrote no report.
type Fail struct {
	ID     string
	Report string
}

// Judgment is one note to the seat. A streak raises it once.
type Judgment struct {
	Subject string
	Body    string
	Fault   string
}

// Beat is one `nova-sprint friend beat`. Working is the lane count.
type Beat struct {
	Working int
	Queue   int
	Width   int
	Running []string
}

// World is one tick's inputs. Block lists cards that must not start (a finish
// already decided). Fault, FaultCount and Judged are the streak so far.
type World struct {
	Friend        string
	Lanes         Lanes
	Width         Width
	Queue         []Card
	Block         []string
	BinaryVersion string
	RunnerVersion string
	Ended         []End
	Fault         string
	FaultCount    int
	Judged        bool
}

// Memory is the streak the next tick starts from.
type Memory struct {
	Fault      string
	FaultCount int
	Judged     bool
}

// Step is what this tick does. It never kills a lane.
type Step struct {
	Start    []Card
	Fail     []Fail
	Judgment *Judgment
	Beat     Beat
	Drain    bool
	Exec     string
	Lanes    Lanes
}

// RowDue reports whether the row may be read again. last zero is the first read.
func RowDue(last, now time.Time) bool {
	return last.IsZero() || !now.Before(last.Add(RowEvery))
}

// HarnessFault is the finish line of a lane with no report.
func HarnessFault(errLine string) string {
	line := strings.TrimSpace(errLine)
	if i := strings.IndexByte(line, '\n'); i >= 0 {
		line = strings.TrimSpace(line[:i])
	}
	if line == "" {
		line = "(no error line)"
	}
	return FaultPrefix + line
}

// Follow reports whether the row names a runner_version this binary is not.
// An empty version, or "-", is the row naming none.
func Follow(binary, row string) bool {
	row = strings.TrimSpace(row)
	if row == "" || row == "-" {
		return false
	}
	return row != strings.TrimSpace(binary)
}

// BeatOf is the beat for these lanes and the queue they have not taken.
func BeatOf(lanes Lanes, queue []Card, width Width) Beat {
	held := make(map[string]bool, lanes.Len())
	for _, n := range lanes.list {
		held[n.ID] = true
	}
	waiting := 0
	for _, c := range queue {
		if c.ID != "" && !held[c.ID] {
			waiting++
		}
	}
	return Beat{Working: lanes.Len(), Queue: waiting, Width: int(width), Running: lanes.IDs()}
}

// Next is one tick: drop lanes that ended, count a no-report streak, start
// what still fits, and follow a runner_version by draining. It does not kill.
func Next(w World) (Step, Memory) {
	mem := Memory{Fault: w.Fault, FaultCount: w.FaultCount, Judged: w.Judged}
	lanes := w.Lanes.clone()
	var fails []Fail
	var judgment *Judgment
	raise := func() {
		if mem.Fault == "" || mem.FaultCount < 3 || mem.Judged {
			return
		}
		mem.Judged = true
		j := judgmentFor(w.Friend, mem.Fault)
		judgment = &j
	}
	for _, e := range w.Ended {
		lanes = lanes.Without(e.ID)
		if e.HasReport {
			mem = Memory{}
			continue
		}
		line := HarnessFault(e.ErrLine)
		fails = append(fails, Fail{ID: e.ID, Report: line})
		if line == mem.Fault {
			mem.FaultCount++
		} else {
			mem.Fault = line
			mem.FaultCount = 1
			mem.Judged = false
		}
		raise()
	}
	// a send that failed leaves Judged clear; the next tick raises it again
	// with no new end
	raise()
	drain := Follow(w.BinaryVersion, w.RunnerVersion)
	var start []Card
	if !drain {
		lanes, start = fill(lanes, w.Width, w.Queue, blocked(w.Block, w.Ended))
	}
	exec := ""
	if drain && lanes.Len() == 0 {
		exec = strings.TrimSpace(w.RunnerVersion)
	}
	return Step{
		Start:    start,
		Fail:     fails,
		Judgment: judgment,
		Beat:     BeatOf(lanes, w.Queue, w.Width),
		Drain:    drain,
		Exec:     exec,
		Lanes:    lanes,
	}, mem
}

func blocked(block []string, ended []End) map[string]bool {
	out := make(map[string]bool, len(block)+len(ended))
	for _, id := range block {
		if id != "" {
			out[id] = true
		}
	}
	for _, e := range ended {
		out[e.ID] = true
	}
	return out
}

// fill takes cards in queue order while each one fits. A card that does not
// fit is left, and a later card that does fit may still start, so a leftover
// half-slot can take a read that sits behind a work card.
func fill(lanes Lanes, width Width, queue []Card, block map[string]bool) (Lanes, []Card) {
	busy := lanes.Busy()
	cap := width.halves()
	var start []Card
	seen := make(map[string]bool, lanes.Len())
	for _, n := range lanes.list {
		seen[n.ID] = true
	}
	for _, c := range queue {
		if c.ID == "" || seen[c.ID] || block[c.ID] {
			continue
		}
		kind := c.Kind
		if kind != KindRead {
			kind = KindWork
		}
		cost := halvesOf(kind)
		if busy+cost > cap {
			continue
		}
		busy += cost
		seen[c.ID] = true
		lanes = lanes.Add(Lane{ID: c.ID, Kind: kind})
		start = append(start, c)
	}
	return lanes, start
}

func judgmentFor(friend, fault string) Judgment {
	who := strings.TrimSpace(friend)
	if who == "" {
		who = "the friend"
	}
	return Judgment{
		Fault:   fault,
		Subject: fmt.Sprintf("judgment: %s harness fault three alike", who),
		Body:    fmt.Sprintf("%s. Three lanes in a row ended with no report and this same line. The runner raised this once and kept going. It did not stop and it did not kill a lane.\n", fault),
	}
}
