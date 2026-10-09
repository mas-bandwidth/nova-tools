package main

import (
	"github.com/mas-bandwidth/nova-tools/internal/friend"
	"github.com/mas-bandwidth/nova-tools/internal/tool"
)

// setupAntigravityCLI implements docs/antigravity-fleet-auth.md's unsupported
// automated enrollment boundary before generic setup can publish a PROFILE.
func setupAntigravityCLI(c *tool.Call) *tool.Out {
	facts, err := friend.AntigravitySetupCapability("antigravity-cli")
	if err != nil {
		return setupRemedy(tool.Refuse(err.Error()))
	}
	plan := friend.SetupPlan{Harness: "antigravity-cli", Facts: facts}
	return setupOut(plan, c.Bool("apply") && !c.DryRun(), setupRemedy(tool.Refuse(friend.AntigravityCLIFleetSetupRefusal().Error())))
}
