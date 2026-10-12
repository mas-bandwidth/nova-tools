package main

import (
	"github.com/mas-bandwidth/nova-tools/pkg/friend"
	"github.com/mas-bandwidth/nova-tools/pkg/tool"
)

// Queue delivery is unavailable even when called without CLI flag validation
// (SPEC-FRIEND.md, Codex). Refuse before opening the bus or changing launchd.
func (w world) runNotifications(c *tool.Call) *tool.Out {
	return tool.Refuse(friend.CodexPushUnavailable)
}

func (w world) installNotifications(c *tool.Call, a friend.Agent) *tool.Out {
	return tool.Refuse(friend.CodexPushUnavailable)
}
