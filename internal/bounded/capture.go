package bounded

import (
	"context"
	"sync"
)

// Capture retains at most limit bytes and cancels a producer as soon as that limit
// is reached. Writes still consume their full input, so cancellation cannot turn
// a capped prefix into an apparently complete response.
type Capture struct {
	mu     sync.Mutex
	data   []byte
	limit  int
	hit    bool
	cancel context.CancelFunc
}

func NewCapture(limit int, cancel context.CancelFunc) *Capture {
	return &Capture{limit: limit, cancel: cancel}
}
func (c *Capture) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := len(p)
	remaining := c.limit - len(c.data)
	if remaining > len(p) {
		remaining = len(p)
	}
	if remaining > 0 {
		c.data = append(c.data, p[:remaining]...)
	}
	if len(c.data) >= c.limit && !c.hit {
		c.hit = true
		if c.cancel != nil {
			c.cancel()
		}
	}
	return n, nil
}
func (c *Capture) Bytes() []byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]byte(nil), c.data...)
}
func (c *Capture) Hit() bool { c.mu.Lock(); defer c.mu.Unlock(); return c.hit }
