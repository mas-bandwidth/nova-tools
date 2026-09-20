// Package lifecycle is the durable attempt ledger nova-swarm launch owns.
// Pulse deals; this package claims, admits start, and records STARTED/UNKNOWN.
// Paths are pinned under an explicit swarm root. This package does not
// implement control/state.json and does not claim two-file atomicity: the
// event log is authoritative, the projection is rebuilt from it.
package lifecycle

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// Store is one lifecycle root. Mutations take the directory lock, reload the
// log, then append; a second Open on the same root linearizes the same way.
type Store struct {
	mu       sync.Mutex
	root     string
	cards    map[string]*Projection
	attempts map[string]string
	keys     map[string][]byte
	degraded bool
}

// Open opens the ledger under an explicitly named swarm root. An empty root
// is refused; the store does not guess a directory.
func Open(explicitRoot string) (*Store, error) {
	if explicitRoot == "" {
		return nil, fmt.Errorf("lifecycle: root is required; refusing to guess")
	}
	abs, err := filepath.Abs(explicitRoot)
	if err != nil {
		return nil, fmt.Errorf("lifecycle: root %q: %w", explicitRoot, err)
	}
	s := &Store{root: abs}
	if err := s.ensureLayout(); err != nil {
		return nil, err
	}
	if err := s.Replay(); err != nil {
		return nil, err
	}
	return s, nil
}

// Replay validates the log's complete valid prefix and rebuilds projections.
func (s *Store) Replay() error {
	if s == nil {
		return fmt.Errorf("lifecycle: store is nil")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	unlock, err := s.lockDir()
	if err != nil {
		return err
	}
	defer unlock()
	if err := s.reloadLocked(); err != nil {
		return err
	}
	for id := range s.cards {
		if err := s.writeProjection(id); err != nil {
			s.degraded = true
			return fmt.Errorf("%w: replay rebuilt memory but could not write projection for %s: %v", ErrDegraded, id, err)
		}
	}
	s.degraded = false
	return nil
}

// Lookup returns the current projection for a card.
func (s *Store) Lookup(card string) (Projection, bool) {
	if s == nil {
		return Projection{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.cards[card]
	if !ok || p == nil {
		return Projection{}, false
	}
	out := *p
	out.Limits = cloneLimits(p.Limits)
	out.Attempt = cloneString(p.Attempt)
	out.Generation = cloneInt(p.Generation)
	out.Bench = cloneString(p.Bench)
	out.Route = cloneString(p.Route)
	out.Source = cloneString(p.Source)
	out.Job = cloneString(p.Job)
	out.Lease = cloneString(p.Lease)
	out.Worker = cloneString(p.Worker)
	out.Reason = cloneString(p.Reason)
	out.Execution = cloneString(p.Execution)
	out.Nonce = cloneString(p.Nonce)
	out.ExitAttest = cloneString(p.ExitAttest)
	return out, true
}

func (s *Store) lifeDir() string {
	return filepath.Join(s.root, dirName)
}

func (s *Store) eventsPath() string {
	return filepath.Join(s.lifeDir(), eventsName)
}

func (s *Store) cardsDir() string {
	return filepath.Join(s.lifeDir(), cardsDir)
}

func (s *Store) attemptsDir() string {
	return filepath.Join(s.lifeDir(), attemptsDir)
}

func (s *Store) cardPath(card string) string {
	return filepath.Join(s.cardsDir(), card+".json")
}

func (s *Store) attemptDir(attempt string) string {
	return filepath.Join(s.attemptsDir(), attempt)
}

func (s *Store) ensureLayout() error {
	for _, dir := range []string{s.lifeDir(), s.cardsDir(), s.attemptsDir()} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	f, err := os.OpenFile(s.eventsPath(), os.O_WRONLY|os.O_CREATE, 0o644)
	if err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return fsyncDir(s.lifeDir())
}

func (s *Store) mutate(fn func() error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.degraded {
		return fmt.Errorf("%w: projection write failed; refusing admission", ErrDegraded)
	}
	unlock, err := s.lockDir()
	if err != nil {
		return err
	}
	defer unlock()
	if err := s.reloadLocked(); err != nil {
		return err
	}
	return fn()
}

func (s *Store) lockDir() (func(), error) {
	if err := os.MkdirAll(s.lifeDir(), 0o755); err != nil {
		return nil, err
	}
	path := filepath.Join(s.lifeDir(), lockName)
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return nil, fmt.Errorf("lifecycle: lock %s: %w", path, err)
	}
	if err := lockFile(f); err != nil {
		f.Close()
		return nil, fmt.Errorf("lifecycle: lock %s: %w", path, err)
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			unlockFile(f)
			f.Close()
		})
	}, nil
}

func (s *Store) reloadLocked() error {
	s.cards = map[string]*Projection{}
	s.attempts = map[string]string{}
	s.keys = map[string][]byte{}
	data, err := os.ReadFile(s.eventsPath())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	lastRev := map[string]int{}
	for i, raw := range bytes.Split(data, []byte("\n")) {
		line := bytes.TrimSpace(raw)
		if len(line) == 0 {
			continue
		}
		var ev Event
		if err := json.Unmarshal(line, &ev); err != nil {
			return fmt.Errorf("%w: malformed event at line %d: %v", ErrMalformed, i+1, err)
		}
		if err := validateEvent(ev); err != nil {
			return fmt.Errorf("%w: invalid event at line %d: %v", ErrMalformed, i+1, err)
		}
		payload, err := marshalEvent(ev)
		if err != nil {
			return err
		}
		if prev, ok := s.keys[ev.Idempotency]; ok {
			if !bytes.Equal(prev, payload) {
				return fmt.Errorf("%w: idempotency key reused with a different payload at line %d", ErrMalformed, i+1)
			}
			continue
		}
		if ev.Rev != lastRev[ev.Card]+1 {
			return fmt.Errorf("%w: revision gap card=%s rev=%d line=%d", ErrMalformed, ev.Card, ev.Rev, i+1)
		}
		lastRev[ev.Card] = ev.Rev
		s.keys[ev.Idempotency] = payload
		s.apply(ev)
	}
	return nil
}

func (s *Store) commit(ev Event) error {
	if err := validateEvent(ev); err != nil {
		return err
	}
	payload, err := marshalEvent(ev)
	if err != nil {
		return err
	}
	if prev, ok := s.keys[ev.Idempotency]; ok {
		if bytes.Equal(prev, payload) {
			return nil
		}
		return fmt.Errorf("%w: idempotency key reused with a different payload", ErrRefused)
	}
	if err := appendLine(s.eventsPath(), payload); err != nil {
		return err
	}
	s.keys[ev.Idempotency] = append([]byte(nil), payload...)
	s.apply(ev)
	if err := s.writeProjection(ev.Card); err != nil {
		s.degraded = true
		return fmt.Errorf("%w: event committed, projection write failed: %v", ErrDegraded, err)
	}
	return nil
}

func (s *Store) apply(ev Event) {
	p := s.cards[ev.Card]
	if p == nil {
		p = &Projection{Card: ev.Card}
		s.cards[ev.Card] = p
	}
	p.State = ev.New
	p.Rev = ev.Rev
	if ev.Attempt != nil {
		p.Attempt = cloneString(ev.Attempt)
		s.attempts[*ev.Attempt] = ev.Card
	}
	if ev.Generation != nil {
		p.Generation = cloneInt(ev.Generation)
	}
	if ev.Bench != nil {
		p.Bench = cloneString(ev.Bench)
	}
	if ev.Route != nil {
		p.Route = cloneString(ev.Route)
	}
	if ev.Source != nil {
		p.Source = cloneString(ev.Source)
	}
	if ev.Job != nil {
		p.Job = cloneString(ev.Job)
	}
	if ev.Lease != nil {
		p.Lease = cloneString(ev.Lease)
	}
	if ev.Limits != nil {
		p.Limits = cloneLimits(ev.Limits)
		p.Attempts = ev.Limits.Attempts
	}
	if ev.Worker != nil {
		p.Worker = cloneString(ev.Worker)
	}
	if ev.FenceEpoch != 0 {
		p.FenceEpoch = ev.FenceEpoch
	}
	if ev.Raised != nil {
		p.Raised = *ev.Raised
	}
	if ev.Reason != nil {
		p.Reason = cloneString(ev.Reason)
	}
	if ev.Execution != nil {
		p.Execution = cloneString(ev.Execution)
	}
	if ev.Nonce != nil {
		p.Nonce = cloneString(ev.Nonce)
	}
	if ev.ExitAttest != nil {
		p.ExitAttest = cloneString(ev.ExitAttest)
	}
	p.Reserved = ev.New != Ready && ev.New != Harvested
}

func (s *Store) byAttempt(attempt string) (*Projection, bool) {
	card, ok := s.attempts[attempt]
	if !ok {
		return nil, false
	}
	p, ok := s.cards[card]
	return p, ok && p != nil
}

func (s *Store) writeProjection(card string) error {
	p, ok := s.cards[card]
	if !ok || p == nil {
		return fmt.Errorf("lifecycle: no projection for %s", card)
	}
	if err := os.MkdirAll(s.cardsDir(), 0o755); err != nil {
		return err
	}
	payload, err := marshalJSON(p)
	if err != nil {
		return err
	}
	return writeAtomic(s.cardPath(card), append(payload, '\n'))
}

func appendLine(name string, line []byte) error {
	f, err := os.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(line); err != nil {
		f.Close()
		return err
	}
	if _, err := f.Write([]byte("\n")); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return fsyncDir(filepath.Dir(name))
}

func writeAtomic(final string, content []byte) error {
	if err := os.MkdirAll(filepath.Dir(final), 0o755); err != nil {
		return err
	}
	tmp := final + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(content); err != nil {
		f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, final); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return fsyncDir(filepath.Dir(final))
}

func fsyncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return nil
	}
	defer d.Close()
	_ = d.Sync()
	return nil
}

func marshalJSON(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(buf.Bytes(), []byte("\n")), nil
}
