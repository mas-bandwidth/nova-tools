package pulse

// Seat reservation for fill dealers (#2029 remainder).
//
// Capacity is an observation. Two dealers can both see free=1 before either
// claims. Rename protects the same card, not the seat. The reservation is a
// file created with O_EXCL under --launched/.fill-seats/<bench>/<n>, so the
// claim of a numbered seat is atomic and stays visible until a matching owned
// lease is positively observed. A known failure releases it. UNKNOWN is not
// freed. Process restart reloads the files; in-memory holds alone are not
// enough. It is not a lock taken around Launch and dropped when Launch returns.

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

// SeatHold is one reserved seat. Release frees a known-failed dispatch.
// KeepOwned / KeepUnknown persist the outcome; neither is dropped until
// ReconcileOwned sees a matching live lease label.
type SeatHold interface {
	Release()
	KeepOwned()
	KeepUnknown()
}

// SeatReserver claims one of the observed free seats on a bench. ok=false
// means another dealer already took them; the caller stands down.
// visible is the set of owned lease labels (and card-*.md aliases) just read.
type SeatReserver interface {
	Reserve(bench string, observedFree int, card string) (SeatHold, bool, error)
	ReconcileOwned(visible map[string]bool)
}

// fileSeats reserves numbered files under root/<bench>/<n>.
func fileSeats(root string) SeatReserver {
	return &fileReserver{root: root}
}

type fileReserver struct {
	root  string
	mu    sync.Mutex
	holds []*fileHold
}

type fileHold struct {
	r       *fileReserver
	path    string
	bench   string
	card    string
	pid     int
	mu      sync.Mutex
	outcome string
}

func (r *fileReserver) Reserve(bench string, observedFree int, card string) (SeatHold, bool, error) {
	if observedFree <= 0 {
		return nil, false, nil
	}
	dir := filepath.Join(r.root, bench)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, false, fmt.Errorf("cannot open seat directory %s: %w", dir, err)
	}
	pid := os.Getpid()
	for i := 0; i < observedFree; i++ {
		path := filepath.Join(dir, strconv.Itoa(i))
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if err != nil {
			if os.IsExist(err) {
				continue
			}
			return nil, false, err
		}
		h := &fileHold{
			r: r, path: path, bench: bench, card: card, pid: pid, outcome: "reserved",
		}
		if _, werr := f.WriteString(h.body()); werr != nil {
			f.Close()
			_ = os.Remove(path)
			return nil, false, werr
		}
		if cerr := f.Close(); cerr != nil {
			_ = os.Remove(path)
			return nil, false, cerr
		}
		r.mu.Lock()
		r.holds = append(r.holds, h)
		r.mu.Unlock()
		return h, true, nil
	}
	return nil, false, nil
}

func (r *fileReserver) loadPersisted() {
	benches, err := os.ReadDir(r.root)
	if err != nil {
		return
	}
	have := map[string]bool{}
	r.mu.Lock()
	for _, h := range r.holds {
		have[h.path] = true
	}
	r.mu.Unlock()
	for _, b := range benches {
		if !b.IsDir() {
			continue
		}
		dir := filepath.Join(r.root, b.Name())
		ents, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range ents {
			if e.IsDir() {
				continue
			}
			path := filepath.Join(dir, e.Name())
			if have[path] {
				continue
			}
			h, ok := readHoldFile(r, path)
			if !ok {
				continue
			}
			r.mu.Lock()
			r.holds = append(r.holds, h)
			r.mu.Unlock()
			have[path] = true
		}
	}
}

func readHoldFile(r *fileReserver, path string) (*fileHold, bool) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	h := &fileHold{r: r, path: path, pid: os.Getpid()}
	for _, line := range strings.Split(string(raw), "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok {
			continue
		}
		switch k {
		case "bench":
			h.bench = v
		case "card":
			h.card = v
		case "outcome":
			h.outcome = v
		}
	}
	if h.card == "" || h.outcome == "" || h.outcome == "released" {
		return nil, false
	}
	if h.bench == "" {
		h.bench = filepath.Base(filepath.Dir(path))
	}
	return h, true
}

func (r *fileReserver) ReconcileOwned(visible map[string]bool) {
	r.loadPersisted()
	r.mu.Lock()
	holds := append([]*fileHold(nil), r.holds...)
	r.mu.Unlock()
	for _, h := range holds {
		h.mu.Lock()
		card := h.card
		outcome := h.outcome
		h.mu.Unlock()
		if outcome == "released" {
			continue
		}
		if leaseVisible(visible, card) {
			h.Release()
		}
	}
}

func leaseVisible(visible map[string]bool, card string) bool {
	if len(visible) == 0 || card == "" {
		return false
	}
	if visible[card] {
		return true
	}
	base := strings.TrimSuffix(card, ".md")
	return visible[base]
}

func (h *fileHold) body() string {
	return fmt.Sprintf("bench=%s\ncard=%s\npid=%d\noutcome=%s\n", h.bench, h.card, h.pid, h.outcome)
}

func (h *fileHold) persist() {
	_ = os.WriteFile(h.path, []byte(h.body()), 0o644)
}

func (h *fileHold) Release() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.outcome == "released" {
		return
	}
	h.outcome = "released"
	_ = os.Remove(h.path)
}

func (h *fileHold) KeepOwned() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.outcome == "released" || h.outcome == "unknown" {
		return
	}
	h.outcome = "owned"
	h.persist()
}

func (h *fileHold) KeepUnknown() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.outcome == "released" {
		return
	}
	h.outcome = "unknown"
	h.persist()
}
