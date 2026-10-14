package main

import (
	"time"

	"github.com/mas-bandwidth/nova-tools/hook"
	"github.com/mas-bandwidth/nova-tools/pkg/tool"
)

// link is the hook client's own view of the heartbeat server, timed by this
// client's clock and never the server's (tla/HookBeat.tla, ClientTimeout): the
// server row's at moving is contact; no move for two of its intervals is a
// loss, said once into this session; the next contact ends it, said once, and
// the episode is reported to the server once it can be written.
type link struct {
	seen  bool          // a server has passed since this hook began watching
	at    time.Time     // the server row's at as last read
	fresh time.Time     // this client's clock when at last moved
	every time.Duration // the server's interval
	lost  bool
	since time.Time   // this client's clock at the last contact before the loss
	owed  []time.Time // an episode to report, from and until
}

// The link's events.
const (
	linkLost = "not-connected"
	linkBack = "connected"
)

// step is one read of the server row (read false: the store did not answer)
// at this client's now, and the event it makes, "" for none.
func (l *link) step(now time.Time, sv hook.Served, read bool) string {
	if read && sv.OK && (!l.seen || !sv.At.Equal(l.at)) {
		l.seen, l.at, l.every, l.fresh = true, sv.At, sv.Every, now
		if l.lost {
			l.lost, l.owed = false, []time.Time{l.since, now}
			return linkBack
		}
		return ""
	}
	if l.seen && !l.lost && now.Sub(l.fresh) > 2*l.every {
		l.lost, l.since = true, l.fresh
		return linkLost
	}
	return ""
}

// watchServer reads the server row, says a loss or a return once, and
// reports an owed episode until the server takes it.
func (w world) watchServer(c *tool.Call, s *session, role hook.Role, lk *link) {
	ctx, cancel := call()
	sv, err := s.hooks.Serve(ctx)
	cancel()
	switch lk.step(w.now(), sv, err == nil) {
	case linkLost:
		since := lk.since.Local().Format(time.RFC3339)
		line(c, tool.Done().As("STATE").Fact("state", linkLost).Fact("since", since).
			Note("judgment: NOT CONNECTED TO THE SERVER since "+since+"; answer one of: re-hook ("+hook.HookCommand(s.as, role)+") | check the network | tell the coordinator when reachable"))
	case linkBack:
		line(c, tool.Done().As("STATE").Fact("state", linkBack).Note("the heartbeat server is reachable again; the episode is reported to it"))
	}
	if lk.owed != nil && err == nil {
		ctx, cancel := call()
		err := s.hooks.Lost(ctx, s.as, lk.owed[0], lk.owed[1])
		cancel()
		if err == nil {
			lk.owed = nil
		}
	}
}
