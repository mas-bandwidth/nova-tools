package main

import (
	"github.com/mas-bandwidth/nova-tools/internal/friend"
	"github.com/mas-bandwidth/nova-tools/internal/tool"
)

// localPong is a native session's nonce-bound file answer inside its wall.
// The daemon alone sends the matching bus pong after validating the grant.
func (w world) localPong(c *tool.Call) *tool.Out {
	p := friend.NativePong{Friend: c.Str("as"), Session: c.Str("session"), Nonce: c.Str("nonce"),
		Capability: c.Str("capability"), Queue: c.Int("queue"), Working: c.Int("working"), Width: c.Int("width")}
	if err := friend.CheckNativePong(p); err != nil {
		return tool.Refuse(err.Error())
	}
	if c.DryRun() {
		return tool.Done().Fact("nonce", p.Nonce).Fact("state", "valid-local-envelope")
	}
	if err := friend.QueueNativePong(c.Str("dir"), p); err != nil {
		return tool.Refuse(err.Error())
	}
	return tool.Done().Fact("nonce", p.Nonce).Fact("state", "queued-for-daemon")
}
