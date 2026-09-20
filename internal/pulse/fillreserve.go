package pulse

// Seat reservation for fill dealers (#2029 remainder).
//
// Capacity is an observation. Two dealers can both see free=1 before either
// claims. Rename protects the same card, not the seat. The reservation is a
// file created with O_EXCL under --launched/.fill-seats/<bench>/<n>, so the
// claim of a numbered seat is atomic and stays visible until this dealer
// releases a known failure, reconciles owned execution on a later tick, or
// keeps an UNKNOWN launch. It is not a lock taken around Launch and dropped
// when Launch returns.

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"sync"
)

// SeatHold is one reserved seat. Release frees a known-failed dispatch.
// KeepOwned marks owned execution, dropped at this process's next tick so a
// raised share can fill. KeepUnknown must not be freed.
type SeatHold interface {
	Release()
	KeepOwned()
	KeepUnknown()
}

// SeatReserver claims one of the observed free seats on a bench. ok=false
// means another dealer already took them; the caller stands down.
type SeatReserver interface {
	Reserve(bench string, observedFree int, card string) (SeatHold, bool, error)
	ReconcileOwned()
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

func (r *fileReserver) ReconcileOwned() {
	r.mu.Lock()
	holds := append([]*fileHold(nil), r.holds...)
	r.mu.Unlock()
	for _, h := range holds {
		h.mu.Lock()
		owned := h.outcome == "owned"
		h.mu.Unlock()
		if owned {
			h.Release()
		}
	}
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
