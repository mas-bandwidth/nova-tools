package harvest

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// FakeControl is the test double for fleet control until internal/control lands.
// Pause and Resume each mint a new generation. WithCoordinator holds one lock
// through verify and the effect.
type FakeControl struct {
	mu      sync.Mutex
	desired string
	gen     int
	expires time.Time
}

// NewFakeControl starts in RUN at generation.
func NewFakeControl(generation int, expires time.Time) *FakeControl {
	if generation < 1 {
		generation = 1
	}
	return &FakeControl{desired: "RUN", gen: generation, expires: expires}
}

// Generation is the current control generation. Tests use it after Resume.
func (f *FakeControl) Generation() int {
	if f == nil {
		return 0
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.gen
}

// Pause records PAUSE at generation n+1.
func (f *FakeControl) Pause() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.gen++
	f.desired = "PAUSE"
	f.expires = time.Time{}
}

// Resume records RUN at generation n+1 with a bounded expiry.
func (f *FakeControl) Resume(expires time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.gen++
	f.desired = "RUN"
	f.expires = expires
}

// WithCoordinator holds the coordinator lock for fn.
func (f *FakeControl) WithCoordinator(ctx context.Context, now time.Time, fn func(View) error) error {
	if f == nil {
		return fmt.Errorf("harvest: control is nil")
	}
	if fn == nil {
		return fmt.Errorf("harvest: coordinator fn is nil")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	return fn(view{desired: f.desired, gen: f.gen, expires: f.expires})
}

type view struct {
	desired string
	gen     int
	expires time.Time
}

func (v view) Desired() string    { return v.desired }
func (v view) Generation() int    { return v.gen }
func (v view) Expires() time.Time { return v.expires }
