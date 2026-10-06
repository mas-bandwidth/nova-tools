package up

import "fmt"

// The platform step: --local runs where a service manager it writes for is
// (launchd on darwin, systemd on linux); any other system is missing, with
// where to run it instead (docs/SPEC-UP.md "Steps", 1).
func init() {
	Register(Step{Name: "platform", Order: 10, Plan: planPlatform, Apply: func(*Env) error { return nil }})
}

func planPlatform(e *Env) Finding {
	switch e.GOOS {
	case "darwin":
		return Finding{OK, "darwin: loops are launchd agents"}
	case "linux":
		return Finding{OK, "linux: loops are systemd user units"}
	}
	return Finding{Missing, fmt.Sprintf("%s is not supported; nova-up --local runs on darwin or linux (on windows: wsl --install, then run it inside)", e.GOOS)}
}
