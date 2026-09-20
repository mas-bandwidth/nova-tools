package harvest

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// Projection is the rebuildable card view the effect owner reads.
type Projection struct {
	Card       string `json:"card"`
	Attempt    string `json:"attempt"`
	FenceEpoch int    `json:"fence_epoch"`
	Attempts   int    `json:"attempts"`
	State      string `json:"state"`
}

// Cards is a read-only card projection. The effect owner never writes the log.
type Cards interface {
	Lookup(card string) (Projection, bool, error)
}

// Memory is an in-memory Cards used by tests and fakes.
type Memory struct {
	mu    sync.RWMutex
	cards map[string]Projection
}

// NewMemory returns an empty in-memory projection.
func NewMemory() *Memory {
	return &Memory{cards: make(map[string]Projection)}
}

// Set replaces the projection for p.Card.
func (m *Memory) Set(p Projection) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cards == nil {
		m.cards = make(map[string]Projection)
	}
	m.cards[p.Card] = p
}

// Lookup returns the current projection for card.
func (m *Memory) Lookup(card string) (Projection, bool, error) {
	if m == nil {
		return Projection{}, false, nil
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	p, ok := m.cards[card]
	return p, ok, nil
}

// DiskCards reads lifecycle/cards/<card>.json and never writes events.jsonl.
type DiskCards struct {
	dir string
}

// OpenCards opens a read-only projection rooted at an explicit lifecycle directory.
func OpenCards(lifecycleDir string) (*DiskCards, error) {
	if lifecycleDir == "" {
		return nil, fmt.Errorf("harvest: lifecycle directory is required; refusing to guess")
	}
	abs, err := filepath.Abs(lifecycleDir)
	if err != nil {
		return nil, fmt.Errorf("harvest: lifecycle directory %q: %w", lifecycleDir, err)
	}
	return &DiskCards{dir: abs}, nil
}

// Lookup reads the card projection. Missing is not an error.
func (d *DiskCards) Lookup(card string) (Projection, bool, error) {
	if d == nil {
		return Projection{}, false, fmt.Errorf("harvest: cards is nil")
	}
	if card == "" || card != filepath.Base(card) {
		return Projection{}, false, fmt.Errorf("%w: card", ErrCard)
	}
	raw, err := os.ReadFile(filepath.Join(d.dir, "cards", card+".json"))
	if err != nil {
		if os.IsNotExist(err) {
			return Projection{}, false, nil
		}
		return Projection{}, false, err
	}
	var p diskProjection
	if err := json.Unmarshal(raw, &p); err != nil {
		return Projection{}, false, fmt.Errorf("harvest: card %s: %w", card, err)
	}
	return Projection{
		Card:       p.Card,
		Attempt:    decodeAttempt(p.Attempt),
		FenceEpoch: p.FenceEpoch,
		Attempts:   p.Attempts,
		State:      p.State,
	}, true, nil
}

type diskProjection struct {
	Card       string          `json:"card"`
	Attempt    json.RawMessage `json:"attempt"`
	FenceEpoch int             `json:"fence_epoch"`
	Attempts   int             `json:"attempts"`
	State      string          `json:"state"`
}

func decodeAttempt(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	return ""
}
