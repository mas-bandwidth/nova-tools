package main

import (
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/friend"
	"github.com/mas-bandwidth/nova-tools/internal/tool"
)

func nativeCommandArg(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// reply is deliberately local: a sandboxed native turn queues one envelope;
// only its daemon may resolve and send the reply on the bus.
func (w world) reply(c *tool.Call) *tool.Out {
	r := friend.NativeReply{
		Friend: c.Str("as"), Session: c.Str("session"), Re: c.Str("re"),
		Capability: c.Str("capability"), Subject: c.Str("subject"), Body: c.Str("body"),
	}
	if err := friend.CheckNativeReply(r); err != nil {
		return tool.Refuse(err.Error())
	}
	if c.DryRun() {
		return tool.Done().Fact("re", r.Re).Fact("state", "valid-local-envelope")
	}
	if err := friend.QueueNativeReply(c.Str("dir"), r); err != nil {
		return tool.Refuse(err.Error())
	}
	return tool.Done().Fact("re", r.Re).Fact("state", "queued-for-daemon")
}
