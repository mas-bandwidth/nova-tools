package friend

import (
	"context"
	"crypto/rand"
	"fmt"
	"sync"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/bus"
)

// NoticeLimit bounds unsent coordinator messages in one daemon. Episode
// detectors enqueue once per transition; this queue only retries delivery
// (tla/FriendNotices.tla, Queue and Send; tla/BusSendOnce.tla).
const NoticeLimit = 256

// Notices retains coordinator messages until the bus returns a receipt. Each
// message keeps its token, including after a lost response, so retry cannot
// duplicate a message. The queue is volatile: a shutdown reports its unsent
// count. A full queue holds its caller until delivery frees space or shutdown
// cancels the call; the independent metadata beat continues.
// Libraries considered: bus.Message.Token supplies idempotency; sync.Mutex
// serializes the lane-output callbacks and the daemon's retry step.
type Notices struct {
	Bus     *bus.Bus
	To      func(context.Context) (string, error)
	Record  func(string)
	Now     func() time.Time           // nil uses the monotonic process clock
	Wait    func(context.Context) bool // nil waits one second; false means cancelled
	waiting int
	mu      sync.Mutex
	queue   []pendingNotice
}

type pendingNotice struct {
	message bus.Message
	attempt time.Time
	said    string
}

// Send records one episode's message and tries delivery immediately. A full
// queue applies backpressure without losing the caller's event (FriendNotices.Queue).
// False means shutdown cancelled admission; the unsent subject is recorded.
func (n *Notices) Send(ctx context.Context, m bus.Message) bool {
	waiting := false
	for {
		n.mu.Lock()
		if len(n.queue) < NoticeLimit {
			if waiting {
				n.waiting--
			}
			m.Token = "friend-notice-" + rand.Text()
			n.queue = append(n.queue, pendingNotice{message: m})
			n.flush(ctx)
			n.mu.Unlock()
			return true
		}
		if !waiting {
			waiting = true
			n.waiting++
			n.Record(fmt.Sprintf("coordinator notice waiting: %d messages await delivery; unsent: %s", NoticeLimit, m.Subject))
		}
		n.mu.Unlock()
		if !n.wait(ctx) {
			n.mu.Lock()
			n.waiting--
			n.mu.Unlock()
			n.Record("coordinator notice cancelled before admission; unsent: " + m.Subject)
			return false
		}
		n.Flush(ctx)
	}
}

// wait bounds full-queue retry work and releases the mutex while waiting, so
// delivery inspection and shutdown remain available (FriendNotices.Queue).
func (n *Notices) wait(ctx context.Context) bool {
	if n.Wait != nil {
		return n.Wait(ctx)
	}
	timer := time.NewTimer(time.Second)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

// Flush retries each pending message (FriendNotices.Send). A refused message
// cannot block independent episodes. Retries stop before the bus token can
// expire; an unknown old receipt stays visible for manual resolution. The target is
// fixed before the first send, so a retry cannot change a token's arguments.
func (n *Notices) Flush(ctx context.Context) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.flush(ctx)
}

func (n *Notices) flush(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, bus.CallTimeout)
	defer cancel()
	pending := n.queue
	n.queue = nil
	var retry []pendingNotice
	var recipient string
	var targetErr error
	targetRead := false
	for _, p := range pending {
		if ctx.Err() != nil {
			n.queue = append(n.queue, p)
			continue
		}
		now := time.Now()
		if n.Now != nil {
			now = n.Now()
		}
		m := &p.message
		why := ""
		if !p.attempt.IsZero() && now.Sub(p.attempt) >= bus.DefaultTokenLife {
			why = "receipt remains unknown past the token retry lifetime; inspect the bus record before retrying"
		} else {
			if len(m.To) == 0 {
				if !targetRead {
					recipient, targetErr = n.To(ctx)
					targetRead = true
				}
				if targetErr != nil {
					why = "reading coordinator: " + targetErr.Error()
				} else if recipient != "" {
					m.To = []string{recipient}
				} else {
					why = "no coordinator to tell"
				}
			}
			if why == "" {
				if p.attempt.IsZero() {
					p.attempt = now
				}
				if _, err := n.Bus.Send(ctx, *m); err != nil {
					why = err.Error()
				}
			}
		}
		if why != "" {
			line := "coordinator notice pending token=" + m.Token + ": " + why + ": " + m.Subject
			if line != p.said {
				n.Record(line)
				p.said = line
			}
			retry = append(retry, p)
		}
	}
	n.queue = append(n.queue, retry...)
}

// Unsent is the outstanding count, reported at shutdown because this queue
// deliberately carries no second persistent store of the bus's receipts.
func (n *Notices) Unsent() int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return len(n.queue) + n.waiting
}
