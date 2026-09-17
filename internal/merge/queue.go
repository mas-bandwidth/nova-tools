package merge

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// The queue section of docs/SPEC-MERGE.md (#1142): one queue, one hold file, one order.
// The queue is the order `run` walks; the hold is a person's and never the tool's; a park
// is a skip plus a record naming why.

const (
	QueueName    = "queue.json"
	QueueTmpName = "queue.json.tmp"
	HoldName     = "hold"
	ClassifyDir  = "classify"
	classifyTmp  = "classify.json.tmp"
)

// Park is the record a poison pull request carries: the test, the package it changed and
// the run ids that failed twice.
type Park struct {
	PR      int    `json:"pr"`
	Test    string `json:"test,omitempty"`
	Package string `json:"package,omitempty"`
	Runs    int    `json:"runs,omitempty"`
	Issue   string `json:"issue,omitempty"`
	At      string `json:"at,omitempty"`
}

// Queue is <lane>/queue.json: the ordered pull requests and the two sets the sweep must
// not touch.
type Queue struct {
	Queued  []int  `json:"queued"`
	Skipped []int  `json:"skipped"`
	Parked  []Park `json:"parked"`
}

// Carries reports whether pr is in the named set.
func containsInt(list []int, pr int) bool {
	for _, x := range list {
		if x == pr {
			return true
		}
	}
	return false
}

// HasSkip reports whether pr is skipped.
func (q *Queue) HasSkip(pr int) bool { return containsInt(q.Skipped, pr) }

// IsParked reports whether pr carries a park record.
func (q *Queue) IsParked(pr int) bool {
	for _, p := range q.Parked {
		if p.PR == pr {
			return true
		}
	}
	return false
}

// Remove takes pr out of a list.
func QueueRemove(list []int, pr int) []int {
	out := list[:0]
	for _, x := range list {
		if x != pr {
			out = append(out, x)
		}
	}
	return out
}

func (q *Queue) DropPark(pr int) {
	out := q.Parked[:0]
	for _, p := range q.Parked {
		if p.PR != pr {
			out = append(out, p)
		}
	}
	q.Parked = out
}

// ApplyToState removes the queue's settled entries from the walk order and reports the
// order the pass walks: the queued pull requests in queue order, then every branch. A
// skipped or parked pull request is in neither list and is not walked. A queue whose
// queued list is nil (no queue.json) leaves the lane's own order untouched.
func (q *Queue) WalkOrder(s *State) (order []string) {
	if q == nil {
		return nil
	}
	byID := map[string]*Entry{}
	for _, e := range s.Entries() {
		byID[e.ID()] = e
	}
	seen := map[string]bool{}
	for _, pr := range q.Queued {
		id := fmt.Sprintf("%d", pr)
		if _, ok := byID[id]; !ok {
			continue
		}
		if seen[id] {
			continue
		}
		seen[id] = true
		order = append(order, id)
	}
	for _, e := range s.Branches {
		order = append(order, e.ID())
	}
	return order
}

// prIDs is the state's pull requests in their own order.
func prIDs(s *State) []int {
	out := make([]int, 0, len(s.PRs))
	for _, e := range s.PRs {
		out = append(out, e.PR)
	}
	return out
}

// LoadQueue reads <lane>/queue.json. A missing file is not an error: the queue is then the
// lane's own pull-request order, which is what `add` appends to. Every queue already in
// the file is reconciled against the lane: a pull request added since the file was written
// joins the tail of the order, unless it is skipped or parked, and a pull request no longer
// in the lane leaves it.
func LoadQueue(lane string, s *State) (*Queue, error) {
	q := &Queue{}
	raw, err := os.ReadFile(filepath.Join(lane, QueueName))
	switch {
	case err == nil:
		if err := json.Unmarshal(raw, q); err != nil {
			return nil, fmt.Errorf("%s does not parse: %w", QueueName, err)
		}
	case errors.Is(err, fs.ErrNotExist):
		// The queue is derived from the lane's own order on first sight.
	default:
		return nil, err
	}
	return q.reconcile(s), nil
}

// reconcile appends what arrived and keeps the queue's order for everything the file
// already named. Nothing is dropped here: a pull request that leaves the lane is removed
// from the queue by the pass that landed it.
func (q *Queue) reconcile(s *State) *Queue {
	inOrder := map[int]bool{}
	for _, pr := range q.Queued {
		inOrder[pr] = true
	}
	skip := map[int]bool{}
	for _, pr := range q.Skipped {
		skip[pr] = true
	}
	parked := map[int]bool{}
	for _, p := range q.Parked {
		parked[p.PR] = true
	}
	// Everything the lane holds and neither set names joins the tail, in lane order.
	for _, e := range s.PRs {
		if inOrder[e.PR] || skip[e.PR] || parked[e.PR] {
			continue
		}
		inOrder[e.PR] = true
		q.Queued = append(q.Queued, e.PR)
	}
	// A queue with no file at all is the lane's own order.
	if q.Queued == nil {
		q.Queued = prIDs(s)
	}
	return q
}

// Encode renders the queue the way it is stored.
func (q *Queue) Encode() ([]byte, error) {
	if q.Queued == nil {
		q.Queued = []int{}
	}
	if q.Skipped == nil {
		q.Skipped = []int{}
	}
	if q.Parked == nil {
		q.Parked = []Park{}
	}
	raw, err := json.MarshalIndent(q, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(raw, '\n'), nil
}

// SaveQueue writes the queue through the fixed temp name and one rename (rule 1). The
// caller holds the state lock.
func SaveQueue(lane string, q *Queue) error {
	raw, err := q.Encode()
	if err != nil {
		return err
	}
	tmp := filepath.Join(lane, QueueTmpName)
	if err := writeWhole(tmp, raw, 0o644); err != nil {
		return err
	}
	if err := replaceState(tmp, filepath.Join(lane, QueueName)); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// UpdateQueue is the one read-modify-write of the queue, under the lane's state lock and
// through a fixed temp name (rule 1).
func UpdateQueue(lane string, s *State, wait time.Duration, change func(*Queue) error) (*Queue, error) {
	release, err := Lock(filepath.Join(lane, StateLock), wait)
	if err != nil {
		return nil, err
	}
	defer release()
	q, err := LoadQueue(lane, s)
	if err != nil {
		return nil, err
	}
	if err := change(q); err != nil {
		return nil, err
	}
	if err := SaveQueue(lane, q); err != nil {
		return nil, err
	}
	return q, nil
}

// PutPark records one poison decision in the queue under the state lock. It is the sweep's
// own write; the caller has already decided.
func PutPark(lane string, p Park) error {
	s, err := Load(lane)
	if err != nil {
		return err
	}
	_, err = UpdateQueue(lane, s, LockWait, func(q *Queue) error {
		q.DropPark(p.PR)
		q.Parked = append(q.Parked, p)
		q.Skipped = append(q.Skipped, p.PR)
		q.Queued = QueueRemove(q.Queued, p.PR)
		return nil
	})
	return err
}

// Hold is the state of <lane>/hold: its first line is the reason.
type Hold struct {
	Reason string
	By     string
	At     string
}

// HoldPath is where the hold lives.
func HoldPath(lane string) string { return filepath.Join(lane, HoldName) }

// ReadHold reads the hold file. present=false means no hold is standing. The first line is
// the reason; the machine fields follow it.
func ReadHold(lane string) (h Hold, present bool, err error) {
	raw, err := os.ReadFile(HoldPath(lane))
	if errors.Is(err, fs.ErrNotExist) {
		return Hold{}, false, nil
	}
	if err != nil {
		return Hold{}, false, err
	}
	lines := strings.Split(string(raw), "\n")
	if len(lines) == 0 {
		return Hold{}, true, nil
	}
	h.Reason = lines[0]
	for _, line := range lines[1:] {
		switch {
		case strings.HasPrefix(line, "by="):
			h.By = strings.TrimPrefix(line, "by=")
		case strings.HasPrefix(line, "at="):
			h.At = strings.TrimPrefix(line, "at=")
		}
	}
	return h, true, nil
}

// WriteHold writes the reason on the first line, then the person and the instant, through
// the fixed temp name and one rename.
func WriteHold(lane, reason, by string, now time.Time) error {
	body := reason + "\nby=" + by + "\nat=" + now.UTC().Format(Stamp) + "\n"
	tmp := HoldPath(lane) + ".tmp"
	if err := writeWhole(tmp, []byte(body), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, HoldPath(lane))
}

// ClearHold removes the hold file, returning the hold that stood so a caller can print how
// long it held.
func ClearHold(lane string) (Hold, bool, error) {
	h, present, err := ReadHold(lane)
	if err != nil || !present {
		return h, present, err
	}
	if err := os.Remove(HoldPath(lane)); err != nil {
		return h, true, err
	}
	return h, true, nil
}

// ClassRecord is one typed classify decision: flaky-under-load, own-change or environment.
type ClassRecord struct {
	Run     string `json:"run"`
	Head    string `json:"head"`
	Entry   string `json:"entry"`
	Verdict string `json:"verdict"`
	Note    string `json:"note,omitempty"`
	By      string `json:"by,omitempty"`
	At      string `json:"at"`
	File    string `json:"file"`
}

// The three classes, closed.
const (
	ClassFlaky       = "flaky-under-load"
	ClassOwnChange   = "own-change"
	ClassEnvironment = "environment"
)

// ValidClass is the closed vocabulary of a classify verdict.
func ValidClass(v string) bool {
	return v == ClassFlaky || v == ClassOwnChange || v == ClassEnvironment
}

// ClassifyFile is where one classify record lives:
// classify/<run>-<at>-<rand6>.json
func ClassifyFile(run string, s Submission) string {
	return filepath.Join(ClassifyDir, safeName(run)+"-"+s.ID()+".json")
}

// NewClassRecord builds the one immutable record and its bytes.
func NewClassRecord(run, head, entry, verdict, note, by string, s Submission) (Item, ClassRecord, error) {
	if strings.TrimSpace(run) == "" {
		return Item{}, ClassRecord{}, errors.New("a classify record needs the run id it is about")
	}
	file := ClassifyFile(run, s)
	rec := ClassRecord{Run: run, Head: head, Entry: entry, Verdict: verdict, Note: note, By: by, At: s.At, File: file}
	body, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return Item{}, ClassRecord{}, err
	}
	body = append(body, '\n')
	return Item{Path: file, Body: body}, rec, nil
}

// LoadClassifies reads every classify record under <lane>/classify and returns the newest
// record per run id: the newest `at` for a run wins.
func LoadClassifies(lane string) (map[string]ClassRecord, error) {
	out := map[string]ClassRecord{}
	dir := filepath.Join(lane, ClassifyDir)
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return err
		}
		if d.IsDir() || !strings.HasSuffix(p, ".json") {
			return nil
		}
		raw, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		var rec ClassRecord
		if err := json.Unmarshal(raw, &rec); err != nil {
			return nil
		}
		if rec.Run == "" {
			return nil
		}
		if prev, ok := out[rec.Run]; !ok || rec.At > prev.At {
			out[rec.Run] = rec
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// ClassifyForHead is the newest class recorded for one head sha and run, or "" when none.
func ClassifyForHead(recs map[string]ClassRecord, run, head string) string {
	if rec, ok := recs[run]; ok && (head == "" || rec.Head == head) {
		return rec.Verdict
	}
	return ""
}

// Failure is one test that failed, as the poison detector reads it: the test name, the
// package it lives in, and how many times it failed.
type Failure struct {
	Test    string
	Package string
	Count   int
}

// sortedIDs is a helper for deterministic output.
func sortedIDs(list []int) []int {
	out := append([]int(nil), list...)
	sort.Ints(out)
	return out
}
