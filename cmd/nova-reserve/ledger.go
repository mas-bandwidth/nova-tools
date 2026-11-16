package main

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

const betweenGrace = 5 * time.Second

var (
	identRe     = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)
	prioRanks   = map[string]int{"low": 0, "normal": 1, "high": 2, "read": 3, "critical": 4, "blocker": 5}
	errTimedOut = errors.New("timed out")
)

func priorityRank(p string) int {
	if r, ok := prioRanks[p]; ok {
		return r
	}
	return prioRanks["normal"]
}

type capRecord struct {
	Tags        []string       `json:"tags"`
	UsableCores int            `json:"usable_cores"`
	Resources   map[string]int `json:"resources"`
	SetByPID    int            `json:"set_by_pid,omitempty"`
	SetAt       string         `json:"set_at,omitempty"`
}

type holdRecord struct {
	Seq          uint64         `json:"seq"`
	PID          int            `json:"pid"`
	Since        string         `json:"since"`
	Holder       string         `json:"holder"`
	Consumer     string         `json:"consumer,omitempty"`
	Label        string         `json:"label"`
	Priority     string         `json:"priority"`
	Exclusive    bool           `json:"exclusive,omitempty"`
	Needs        map[string]int `json:"needs"`
	Cores        int            `json:"cores"`
	CPUs         []int          `json:"cpus,omitempty"`
	Paused       bool           `json:"paused"`
	WaitingStart bool           `json:"waiting_start,omitempty"`
	BetweenUntil int64          `json:"between_until,omitempty"`
	Why          string         `json:"why,omitempty"`
	Cmd          string         `json:"cmd,omitempty"`
}

type waiterRecord struct {
	Seq       uint64         `json:"seq"`
	PID       int            `json:"pid"`
	Since     string         `json:"since"`
	Holder    string         `json:"holder"`
	Consumer  string         `json:"consumer,omitempty"`
	Label     string         `json:"label"`
	Priority  string         `json:"priority"`
	Exclusive bool           `json:"exclusive,omitempty"`
	Needs     map[string]int `json:"needs"`
	Tags      []string       `json:"tags,omitempty"`
	Why       string         `json:"why,omitempty"`
	Cmd       string         `json:"cmd,omitempty"`
}

type needItem struct {
	name   string
	amount int
	hasNum bool
}

func parseNeedItems(raw string) ([]needItem, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, fmt.Errorf("empty needs list")
	}
	seen := map[string]bool{}
	var items []needItem
	for _, part := range strings.Split(raw, ",") {
		f := strings.Fields(part)
		if len(f) == 0 || len(f) > 2 || !identRe.MatchString(f[0]) || seen[f[0]] {
			return nil, fmt.Errorf("malformed or duplicate item %q", strings.TrimSpace(part))
		}
		seen[f[0]] = true
		if len(f) == 1 {
			items = append(items, needItem{name: f[0]})
			continue
		}
		n, err := strconv.Atoi(f[1])
		if err != nil || n < 1 {
			return nil, fmt.Errorf("%s wants a whole number >= 1, not %q", f[0], f[1])
		}
		items = append(items, needItem{name: f[0], amount: n, hasNum: true})
	}
	return items, nil
}

func effectiveOS(goos string) string {
	if goos == "darwin" {
		return "mac"
	}
	return goos
}

func effectiveTags(custom []string, w world) []string {
	set := map[string]bool{}
	for _, t := range custom {
		if t != "" {
			set[t] = true
		}
	}
	if osTag := effectiveOS(w.goos); osTag != "" {
		set[osTag] = true
	}
	if w.goarch != "" {
		set[w.goarch] = true
	}
	if out := slices.Sorted(maps.Keys(set)); out != nil {
		return out
	}
	return []string{}
}

type corrupt string

func (c corrupt) Error() string { return string(c) }

func capPath(dir string) string { return filepath.Join(dir, "capacity.json") }

func readCapacity(dir string) (capRecord, bool, error) {
	p := capPath(dir)
	line, ok, err := readRecord(p)
	if !ok || err != nil {
		return capRecord{}, false, err
	}
	var c capRecord
	if err := json.Unmarshal(line, &c); err != nil {
		return capRecord{}, false, corrupt(fmt.Sprintf("%s holds invalid JSON (%v)", p, err))
	}
	if c.UsableCores < 1 || c.Resources["cpu"] < 1 || c.Resources["memory"] < 1 {
		return capRecord{}, false, corrupt(fmt.Sprintf("%s is missing positive cpu, memory, or usable_cores", p))
	}
	for k, v := range c.Resources {
		if !identRe.MatchString(k) || v < 1 || slices.Contains(c.Tags, k) {
			return capRecord{}, false, corrupt(fmt.Sprintf("%s has invalid or colliding resource %s=%d", p, k, v))
		}
	}
	if c.Tags == nil {
		c.Tags = []string{}
	}
	return c, true, nil
}

func writeCapacity(dir string, c capRecord, pid int) error {
	if c.Tags == nil {
		c.Tags = []string{}
	}
	slices.Sort(c.Tags)
	b, err := json.Marshal(c)
	if err != nil {
		return err
	}
	if err := writeRecord(capPath(dir), b, pid); err != nil {
		if odd := (*exitError)(nil); errors.As(err, &odd) {
			return err
		}
		return refuse(2, "%v; run: sudo rm -f %s", err, shellQuote(capPath(dir)))
	}
	return nil
}

func resolveNeeds(o options, c capRecord, w world) (map[string]int, []string, error) {
	if o.exclusive {
		return maps.Clone(c.Resources), nil, nil
	}
	hasTag := map[string]bool{}
	for _, t := range effectiveTags(c.Tags, w) {
		hasTag[t] = true
	}
	needs := map[string]int{}
	var reqTags []string
	for _, it := range o.needs {
		if it.hasNum {
			if hasTag[it.name] {
				return nil, nil, refuse(2, "tag %q takes no number; run: %s", it.name, helpFor(o.verb))
			}
			if _, ok := c.Resources[it.name]; !ok {
				return nil, nil, refuse(1, "machine has no resource %q; run: %s", it.name, o.remedy("status"))
			}
			needs[it.name] = it.amount
		} else if it.name == "cpu" || it.name == "memory" {
			needs[it.name] = 1
		} else if hasTag[it.name] {
			reqTags = append(reqTags, it.name)
		} else if c.Resources[it.name] >= 1 {
			needs[it.name] = 1
		} else {
			return nil, nil, refuse(1, "machine lacks tag or resource %q; run: %s", it.name, o.remedy("status"))
		}
	}
	needs["cpu"] = cmp.Or(needs["cpu"], 1)
	needs["memory"] = cmp.Or(needs["memory"], 1)
	if err := checkStillFitsMachine(needs, reqTags, false, c, w, o); err != nil {
		return nil, nil, err
	}
	return needs, reqTags, nil
}

func checkStillFitsMachine(needs map[string]int, reqTags []string, excl bool, c capRecord, w world, o options) error {
	if excl {
		return nil
	}
	mtags := effectiveTags(c.Tags, w)
	for _, t := range reqTags {
		if !slices.Contains(mtags, t) {
			return refuse(1, "machine lacks tag %q; run: %s", t, o.remedy("status"))
		}
	}
	for r, v := range needs {
		if v > c.Resources[r] {
			return refuse(1, "needs %s %d exceeds machine capacity %d; run: %s", r, v, c.Resources[r], o.remedy("status"))
		}
	}
	return nil
}

func withMutex(w world, dir string, exclusive bool, deadline time.Time, fn func() error) error {
	mp := filepath.Join(dir, "mutex")
	for {
		if !deadline.IsZero() && !w.now().Before(deadline) {
			return errTimedOut
		}
		rel, ok, err := w.lock(mp, exclusive)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) && !exclusive {
				return fn()
			}
			return err
		}
		if ok {
			defer rel()
			return fn()
		}
		if !deadline.IsZero() && !w.now().Before(deadline) {
			return errTimedOut
		}
		d := time.Millisecond
		if !deadline.IsZero() {
			d = min(d, max(time.Microsecond, deadline.Sub(w.now())))
		}
		w.sleep(d)
	}
}

func nextSeqLocked(dir string, holds []*holdRecord, waiters []*waiterRecord, pid int) (uint64, error) {
	var cur uint64
	if line, ok, err := readRecord(filepath.Join(dir, "seq")); err != nil {
		return 0, err
	} else if ok {
		// ignored: invalid or empty seq file resets to max live seq + 1
		cur, _ = strconv.ParseUint(strings.TrimSpace(string(line)), 10, 64)
	}
	for _, h := range holds {
		cur = max(cur, h.Seq)
	}
	for _, wr := range waiters {
		cur = max(cur, wr.Seq)
	}
	cur++
	return cur, writeRecord(filepath.Join(dir, "seq"), []byte(strconv.FormatUint(cur, 10)), pid)
}

func notifyLocked(dir string, pid int, now time.Time) {
	// ignored: best-effort wakeup touch for kqueue/inotify watchers
	_ = writeRecord(filepath.Join(dir, "notify"), []byte(strconv.FormatInt(now.UnixNano(), 10)), pid)
}

func removePair(lp, jp string) {
	// ignored: best-effort metadata removal under mutex
	_ = os.Remove(jp)
	// ignored: best-effort lock removal under mutex
	_ = os.Remove(lp)
}

func seqFiles(dir, sub string, seq uint64) (string, string) {
	s := strconv.FormatUint(seq, 10)
	return filepath.Join(dir, sub, s+".lock"), filepath.Join(dir, sub, s+".json")
}

func lockPath(dir, sub string, seq uint64) string {
	return filepath.Join(dir, sub, strconv.FormatUint(seq, 10)+".lock")
}

func seqNumbersIn(dir string) []uint64 {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	nums := map[uint64]bool{}
	for _, e := range entries {
		name := strings.TrimSuffix(strings.TrimSuffix(e.Name(), ".lock"), ".json")
		if name != e.Name() {
			if n, err := strconv.ParseUint(name, 10, 64); err == nil && n > 0 {
				nums[n] = true
			}
		}
	}
	return slices.Sorted(maps.Keys(nums))
}

func loadStateLocked(w world, dir string, statusOnly bool) ([]*holdRecord, []*waiterRecord, error) {
	nowNano := w.now().UnixNano()
	var holds []*holdRecord
	for _, seq := range seqNumbersIn(filepath.Join(dir, "holds")) {
		lp, jp := seqFiles(dir, "holds", seq)
		line, hasJSON, err := readRecord(jp)
		if err != nil {
			return nil, nil, err
		}
		var h holdRecord
		valid := hasJSON && json.Unmarshal(line, &h) == nil && h.Seq == seq
		if valid && h.BetweenUntil > 0 {
			if nowNano < h.BetweenUntil {
				holds = append(holds, &h)
			} else if !statusOnly {
				removePair(lp, jp)
			}
			continue
		}
		alive, err := probeLock(w, lp, jp, statusOnly)
		if err != nil {
			return nil, nil, err
		}
		if alive && valid {
			holds = append(holds, &h)
		}
	}
	var waiters []*waiterRecord
	for _, seq := range seqNumbersIn(filepath.Join(dir, "line")) {
		lp, jp := seqFiles(dir, "line", seq)
		line, hasJSON, err := readRecord(jp)
		if err != nil {
			return nil, nil, err
		}
		alive, err := probeLock(w, lp, jp, statusOnly)
		if err != nil {
			return nil, nil, err
		}
		var wr waiterRecord
		if alive && hasJSON && json.Unmarshal(line, &wr) == nil && wr.Seq == seq {
			waiters = append(waiters, &wr)
		}
	}
	return holds, waiters, nil
}

func probeLock(w world, lp, jp string, statusOnly bool) (bool, error) {
	rel, unlocked, err := w.lock(lp, !statusOnly)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			if !statusOnly {
				removePair(lp, jp)
			}
			return false, nil
		}
		return false, err
	}
	if unlocked {
		if !statusOnly {
			removePair(lp, jp)
		}
		rel()
		return false, nil
	}
	return true, nil
}

func sumUnpaused(holds []*holdRecord) (map[string]int, bool) {
	used := map[string]int{}
	anyExcl := false
	for _, h := range holds {
		if !h.Paused {
			anyExcl = anyExcl || h.Exclusive
			for r, v := range h.Needs {
				used[r] += v
			}
		}
	}
	return used, anyExcl
}

func fitsResources(needs, used, total map[string]int, exclBlocked bool) bool {
	if exclBlocked {
		return false
	}
	for r, v := range needs {
		if used[r]+v > total[r] {
			return false
		}
	}
	return true
}

func coresForHold(cpuNeed int, excl bool, c capRecord) int {
	if excl || c.Resources["cpu"] <= 0 {
		return max(1, c.UsableCores)
	}
	return max(1, min(c.UsableCores, cpuNeed*c.UsableCores/c.Resources["cpu"]))
}

func assignHoldCPUs(w world, cores int, excl bool, holds []*holdRecord, skipSeq uint64) ([]int, error) {
	usable, err := w.usable()
	if err != nil || len(usable) == 0 {
		return nil, err
	}
	if excl {
		return slices.Clone(usable), nil
	}
	inUse := map[int]int{}
	for _, h := range holds {
		if !h.Paused && h.Seq != skipSeq {
			for _, cpu := range h.CPUs {
				inUse[cpu]++
			}
		}
	}
	return chooseCPUs(usable, w.groups(usable), cores, inUse), nil
}

func saveHoldLocked(dir string, h *holdRecord, pid int) error {
	_, jp := seqFiles(dir, "holds", h.Seq)
	b, err := json.Marshal(h)
	if err != nil {
		return err
	}
	if err := writeRecord(jp, b, pid); err != nil {
		if odd := (*exitError)(nil); errors.As(err, &odd) {
			return err
		}
		return refuse(2, "%v; status could not show this hold; run: sudo rm -f %s", err, shellQuote(jp))
	}
	return nil
}

func reconcileLocked(w world, dir string, c capRecord, holds []*holdRecord, waiters []*waiterRecord) ([]*holdRecord, error) {
	hasCI := slices.ContainsFunc(holds, func(h *holdRecord) bool { return !h.Paused && h.Holder == "ci" })
	if !hasCI && len(waiters) == 0 {
		holds = slices.DeleteFunc(holds, func(h *holdRecord) bool {
			if h.BetweenUntil > 0 {
				removePair(seqFiles(dir, "holds", h.Seq))
				return true
			}
			return false
		})
	}
	var cands []*holdRecord
	for _, h := range holds {
		if h.Paused && h.BetweenUntil == 0 {
			cands = append(cands, h)
		}
	}
	slices.SortFunc(cands, func(a, b *holdRecord) int {
		if d := cmp.Compare(priorityRank(b.Priority), priorityRank(a.Priority)); d != 0 {
			return d
		}
		return cmp.Compare(a.Seq, b.Seq)
	})
	for _, h := range cands {
		if h.WaitingStart && !h.Exclusive && !fitsResources(h.Needs, nil, c.Resources, false) {
			continue
		}
		used, anyExcl := sumUnpaused(holds)
		if !fitsResources(h.Needs, used, c.Resources, anyExcl || (h.Exclusive && len(used) > 0)) {
			continue
		}
		h.Paused = false
		if h.WaitingStart {
			h.WaitingStart = false
			if h.Exclusive {
				h.Needs = maps.Clone(c.Resources)
			}
			h.Cores = coresForHold(h.Needs["cpu"], h.Exclusive, c)
			// ignored: unpause updates metadata; launching process pins its own CPUs
			cpus, _ := assignHoldCPUs(w, h.Cores, h.Exclusive, holds, h.Seq)
			h.CPUs = cpus
		}
		if err := saveHoldLocked(dir, h, w.pid); err != nil {
			return holds, err
		}
	}
	return holds, nil
}

func displaceForCILocked(w world, dir string, c capRecord, holds []*holdRecord, ciNeeds map[string]int, ciExcl bool) error {
	for {
		// ignored: exclusive active holds are checked via h.Exclusive below
		used, _ := sumUnpaused(holds)
		var over []string
		for r, tot := range c.Resources {
			if used[r]+ciNeeds[r] > tot {
				over = append(over, r)
			}
		}
		var victim *holdRecord
		for _, h := range holds {
			if h.Paused || h.Holder == "ci" {
				continue
			}
			contributes := ciExcl || h.Exclusive || slices.ContainsFunc(over, func(r string) bool { return h.Needs[r] > 0 })
			if !contributes {
				continue
			}
			if victim == nil || priorityRank(h.Priority) < priorityRank(victim.Priority) ||
				(priorityRank(h.Priority) == priorityRank(victim.Priority) && h.Seq > victim.Seq) {
				victim = h
			}
		}
		if victim == nil {
			return nil
		}
		victim.Paused = true
		if err := saveHoldLocked(dir, victim, w.pid); err != nil {
			return err
		}
	}
}

type activeHold struct {
	w        world
	o        options
	rec      holdRecord
	unlock   func()
	pinWarn  error
	measured *capRecord
}

func (a *activeHold) release(leaveBetween bool) {
	if a == nil || a.unlock == nil {
		return
	}
	unlock := a.unlock
	a.unlock = nil
	lp, jp := seqFiles(a.o.dir, "holds", a.rec.Seq)
	// ignored: release runs at exit; mutex updates ledger and wakes waiters
	_ = withMutex(a.w, a.o.dir, true, time.Time{}, func() error {
		holds, waiters, err := loadStateLocked(a.w, a.o.dir, false)
		if err != nil {
			removePair(lp, jp)
			unlock()
			return nil
		}
		idx := slices.IndexFunc(holds, func(h *holdRecord) bool { return h.Seq == a.rec.Seq })
		hasCI := slices.ContainsFunc(holds, func(h *holdRecord) bool { return !h.Paused && h.Holder == "ci" })
		if leaveBetween && idx >= 0 && holds[idx].Paused && holds[idx].Consumer != "" && (hasCI || len(waiters) > 0) {
			holds[idx].BetweenUntil = a.w.now().Add(betweenGrace).UnixNano()
			// ignored: best-effort handoff write
			_ = saveHoldLocked(a.o.dir, holds[idx], a.w.pid)
			// ignored: lock file removed while JSON handoff stays until next step attaches
			_ = os.Remove(lp)
			unlock()
		} else {
			removePair(lp, jp)
			unlock()
			if idx >= 0 {
				holds = slices.Delete(holds, idx, idx+1)
			}
		}
		if c, have, cerr := readCapacity(a.o.dir); cerr == nil && have {
			// ignored: best-effort unpause pass during release
			_, _ = reconcileLocked(a.w, a.o.dir, c, holds, waiters)
		}
		notifyLocked(a.o.dir, a.w.pid, a.w.now())
		return nil
	})
}

func dropLockEntry(w world, dir, sub string, seq uint64, unlock *func(), reconcile bool) {
	if *unlock == nil {
		return
	}
	u := *unlock
	*unlock = nil
	lp, jp := seqFiles(dir, sub, seq)
	// ignored: cleanup on timeout or error
	_ = withMutex(w, dir, true, time.Time{}, func() error {
		removePair(lp, jp)
		u()
		if reconcile {
			if curCap, ok, cerr := readCapacity(dir); cerr == nil && ok {
				if hs, ws, lerr := loadStateLocked(w, dir, false); lerr == nil {
					// ignored: best-effort unpause pass during cleanup
					_, _ = reconcileLocked(w, dir, curCap, hs, ws)
				}
			}
		}
		notifyLocked(dir, w.pid, w.now())
		return nil
	})
}

func blockerSummary(holds []*holdRecord, waiters []*waiterRecord) string {
	var parts []string
	for _, h := range holds {
		state := map[bool]string{false: "held", true: "paused"}[h.Paused]
		s := fmt.Sprintf("hold %d %s holder=%s label=%s pid=%d", h.Seq, state, h.Holder, h.Label, h.PID)
		if h.Why != "" {
			s += " why=" + esc(h.Why)
		}
		parts = append(parts, s)
	}
	if len(waiters) > 0 {
		parts = append(parts, fmt.Sprintf("line=%d", len(waiters)))
	}
	return cmp.Or(strings.Join(parts, ", "), "resources busy")
}

func blockerPIDs(holds []*holdRecord, waiters []*waiterRecord, selfPID int) []int {
	var pids []int
	for _, h := range holds {
		if h.PID > 0 && h.PID != selfPID && h.BetweenUntil == 0 {
			pids = append(pids, h.PID)
		}
	}
	for _, wr := range waiters {
		if wr.PID > 0 && wr.PID != selfPID {
			pids = append(pids, wr.PID)
		}
	}
	return pids
}

func acquire(w world, o options) (*activeHold, error) {
	var deadline time.Time
	if o.wait > 0 {
		deadline = w.now().Add(o.wait)
	}
	c, have, err := readCapacity(o.dir)
	if bad := corrupt(""); errors.As(err, &bad) {
		return nil, refuse(2, "%s; run: %s", bad, o.remedy("measure", "--force"))
	} else if err != nil {
		return nil, err
	}
	var autoMeasured *capRecord
	if !have {
		if filepath.Clean(o.dir) != systemDir {
			return nil, refuse(2, "%s %s has no capacity.json; measure or set capacity first; run: %s",
				o.dirFrom, o.dir, o.remedy("measure", "--force"))
		}
		mc, didMeasure, merr := measureInto(w, o, true, deadline)
		if merr != nil {
			return nil, merr
		}
		c = mc
		if didMeasure {
			autoMeasured = &mc
		}
	}

	needs, reqTags, err := resolveNeeds(o, c, w)
	if err != nil {
		if o.consumer != "" {
			// ignored: clear between-command marker if next step can never fit
			_ = withMutex(w, o.dir, true, time.Time{}, func() error {
				holds, _, lerr := loadStateLocked(w, o.dir, false)
				if lerr == nil {
					for _, h := range holds {
						if h.Consumer == o.consumer && h.BetweenUntil > 0 {
							removePair(seqFiles(o.dir, "holds", h.Seq))
						}
					}
				}
				return nil
			})
		}
		return nil, err
	}

	wt := w.watch(o.dir)
	defer wt.close()

	var waitSeq, contSeq uint64
	var waitUnlock, contUnlock func()
	var summary string
	cmdStr := strings.Join(o.argv, " ")

	for {
		wt.drain()
		var granted *activeHold
		var pids []int

		merr := withMutex(w, o.dir, true, deadline, func() error {
			curCap, ok, cerr := readCapacity(o.dir)
			if bad := corrupt(""); errors.As(cerr, &bad) {
				return refuse(2, "%s; run: %s", bad, o.remedy("measure", "--force"))
			} else if cerr != nil {
				return cerr
			} else if !ok {
				return refuse(2, "%s %s has no capacity.json; run: %s", o.dirFrom, o.dir, o.remedy("measure", "--force"))
			}
			c = curCap
			if o.exclusive {
				needs = maps.Clone(c.Resources)
			}
			if err := checkStillFitsMachine(needs, reqTags, o.exclusive, c, w, o); err != nil {
				return err
			}
			holds, waiters, err := loadStateLocked(w, o.dir, false)
			if err != nil {
				return err
			}
			if holds, err = reconcileLocked(w, o.dir, c, holds, waiters); err != nil {
				return err
			}
			summary = blockerSummary(holds, waiters)
			pids = blockerPIDs(holds, waiters, w.pid)

			if contSeq > 0 {
				for _, h := range holds {
					if h.Seq == contSeq && !h.Paused {
						u := contUnlock
						contUnlock = nil
						granted = &activeHold{w: w, o: o, rec: *h, unlock: u, measured: autoMeasured}
					}
				}
				return nil
			}

			if o.holder == "ci" {
				if err := displaceForCILocked(w, o.dir, c, holds, needs, o.exclusive); err != nil {
					return err
				}
				used, anyExcl := sumUnpaused(holds)
				if fitsResources(needs, used, c.Resources, anyExcl || (o.exclusive && len(used) > 0)) {
					h, u, err := createHoldLocked(w, o, c, holds, waiters, needs, cmdStr)
					if err != nil {
						return err
					}
					notifyLocked(o.dir, w.pid, w.now())
					granted = &activeHold{w: w, o: o, rec: *h, unlock: u, measured: autoMeasured}
					return nil
				}
				if o.nowait {
					return refuse(1, "NO ROOM: %s; run: %s", summary, o.remedy("status"))
				}
				return nil
			}

			if waitSeq == 0 && o.consumer != "" {
				for _, h := range holds {
					if h.Consumer == o.consumer && h.BetweenUntil > 0 {
						if o.nowait {
							used, anyExcl := sumUnpaused(holds)
							if !fitsResources(needs, used, c.Resources, anyExcl || (o.exclusive && len(used) > 0)) {
								return refuse(1, "NO ROOM: %s; run: %s", summary, o.remedy("status"))
							}
						}
						lp := lockPath(o.dir, "holds", h.Seq)
						u, ok, err := w.lock(lp, true)
						if err != nil {
							return err
						}
						if ok {
							h.BetweenUntil, h.WaitingStart, h.Paused = 0, true, true
							h.PID, h.Since = w.pid, w.now().UTC().Format(time.RFC3339)
							h.Holder, h.Label, h.Priority = o.holder, o.label, o.priority
							h.Exclusive, h.Needs, h.Why, h.Cmd = o.exclusive, maps.Clone(needs), o.why, cmdStr
							if err := saveHoldLocked(o.dir, h, w.pid); err != nil {
								u()
								return err
							}
							contSeq, contUnlock = h.Seq, u
							if _, err = reconcileLocked(w, o.dir, c, holds, waiters); err != nil {
								return err
							}
							if !h.Paused {
								contUnlock = nil
								granted = &activeHold{w: w, o: o, rec: *h, unlock: u, measured: autoMeasured}
							}
							return nil
						}
					}
				}
			}

			used, anyExcl := sumUnpaused(holds)
			paused := slices.ContainsFunc(holds, func(h *holdRecord) bool { return h.Paused })
			isHead := waitSeq > 0 && !slices.ContainsFunc(waiters, func(wr *waiterRecord) bool { return wr.Seq < waitSeq })
			if !paused && fitsResources(needs, used, c.Resources, anyExcl || (o.exclusive && len(used) > 0)) && ((waitSeq == 0 && len(waiters) == 0) || isHead) {
				if waitSeq > 0 {
					removePair(seqFiles(o.dir, "line", waitSeq))
					waitUnlock()
					waitUnlock = nil
					waiters = slices.DeleteFunc(waiters, func(wr *waiterRecord) bool { return wr.Seq == waitSeq })
					waitSeq = 0
				}
				h, u, err := createHoldLocked(w, o, c, holds, waiters, needs, cmdStr)
				if err != nil {
					return err
				}
				granted = &activeHold{w: w, o: o, rec: *h, unlock: u, measured: autoMeasured}
				return nil
			}
			if o.nowait {
				return refuse(1, "NO ROOM: %s; run: %s", summary, o.remedy("status"))
			}
			if waitSeq == 0 {
				seq, err := nextSeqLocked(o.dir, holds, waiters, w.pid)
				if err != nil {
					return err
				}
				lp, jp := seqFiles(o.dir, "line", seq)
				u, ok, err := w.lock(lp, true)
				if err != nil || !ok {
					return cmp.Or[error](err, refuse(2, "lock collision on %s; run: %s", lp, o.remedy("status")))
				}
				wr := waiterRecord{
					Seq: seq, PID: w.pid, Since: w.now().UTC().Format(time.RFC3339),
					Holder: o.holder, Consumer: o.consumer, Label: o.label, Priority: o.priority,
					Exclusive: o.exclusive, Needs: maps.Clone(needs), Tags: reqTags, Why: o.why, Cmd: cmdStr,
				}
				b, _ := json.Marshal(wr) // ignored: waiterRecord contains only primitives, slices, and string-int map
				if err := writeRecord(jp, b, w.pid); err != nil {
					u()
					return err
				}
				waitSeq, waitUnlock = seq, u
			}
			return nil
		})

		if granted != nil {
			if len(granted.rec.CPUs) > 0 && o.verb == "run" {
				granted.pinWarn = w.pin(granted.rec.CPUs)
			}
			return granted, nil
		}
		if merr != nil && !errors.Is(merr, errTimedOut) {
			dropLockEntry(w, o.dir, "line", waitSeq, &waitUnlock, false)
			dropLockEntry(w, o.dir, "holds", contSeq, &contUnlock, true)
			return nil, merr
		}
		if !deadline.IsZero() && !w.now().Before(deadline) {
			dropLockEntry(w, o.dir, "line", waitSeq, &waitUnlock, false)
			dropLockEntry(w, o.dir, "holds", contSeq, &contUnlock, true)
			return nil, refuse(1, "no hold within %s: %s; run: %s", o.wait, cmp.Or(summary, "resources busy"), o.remedy("status"))
		}
		d := poll
		if !deadline.IsZero() {
			d = min(d, deadline.Sub(w.now()))
		}
		wt.wait(pids, d)
	}
}

func createHoldLocked(w world, o options, c capRecord, holds []*holdRecord, waiters []*waiterRecord, needs map[string]int, cmdStr string) (*holdRecord, func(), error) {
	seq, err := nextSeqLocked(o.dir, holds, waiters, w.pid)
	if err != nil {
		return nil, nil, err
	}
	lp := lockPath(o.dir, "holds", seq)
	u, ok, err := w.lock(lp, true)
	if err != nil || !ok {
		return nil, nil, cmp.Or[error](err, refuse(2, "lock collision on %s; run: %s", lp, o.remedy("status")))
	}
	cores := coresForHold(needs["cpu"], o.exclusive, c)
	cpus, uerr := assignHoldCPUs(w, cores, o.exclusive, holds, seq)
	if uerr != nil && o.verb == "run" {
		fmt.Fprintf(w.stderr, "nova-reserve: running unpinned: %v\n", uerr)
	}
	h := &holdRecord{
		Seq: seq, PID: w.pid, Since: w.now().UTC().Format(time.RFC3339),
		Holder: o.holder, Consumer: o.consumer, Label: o.label, Priority: o.priority,
		Exclusive: o.exclusive, Needs: maps.Clone(needs), Cores: cores, CPUs: cpus,
		Why: o.why, Cmd: cmdStr,
	}
	if err := saveHoldLocked(o.dir, h, w.pid); err != nil {
		u()
		return nil, nil, err
	}
	return h, u, nil
}

func usableCoreCount(w world) int {
	if u, err := w.usable(); err == nil && len(u) > 0 {
		return len(u)
	}
	return max(1, w.cpus)
}
