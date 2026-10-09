package friend

import "errors"

// AntigravitySetupCapability reports provider contracts, not target readiness.
// It implements docs/antigravity-fleet-auth.md's surface and enrollment boundary.
// No native process or authentication store is inspected.
func AntigravitySetupCapability(surface string) ([]SetupFact, error) {
	var transport, transportState, enrollment string
	switch surface {
	case "antigravity":
		transportState = "existing-adapter"
		transport = "existing desktop AgentAPI mailbox adapter; CLI sign-in does not prove desktop control"
		enrollment = "desktop fleet enrollment is unestablished; existing desktop operation is unchanged"
	case "antigravity-cli":
		transportState = "documented"
		transport = "documented NDJSON text turns; static permission settings exist, interactive control_request/control_response are rejected"
		enrollment = "per-target native SSH OAuth URL and browser code; automated completion and auth-only status contracts are unestablished"
	default:
		return nil, errors.New("Antigravity capability wants antigravity or antigravity-cli; select the desktop or CLI surface explicitly")
	}
	return []SetupFact{
		{Name: "surface", State: surface},
		{Name: "transport", State: transportState, Detail: transport},
		{Name: "enrollment", State: "unestablished", Detail: enrollment},
		{Name: "portable-consumer-grant", State: "unestablished", Detail: "no supported export/import or cross-host renewal contract is established"},
		{Name: "authentication", State: "unknown", Detail: "capability does not inspect native account credentials"},
		{Name: "allowance", State: "unknown", Detail: "verify plan quota and AI Credit Overages=Never separately; no Gemini API fallback"},
		{Name: "readiness", State: "unestablished", Detail: "installation, runtime authentication and native control require separate evidence"},
	}, nil
}

// AntigravityCLIFleetSetupRefusal implements the CLI enrollment boundary in
// docs/antigravity-fleet-auth.md; it does not govern the desktop adapter.
func AntigravityCLIFleetSetupRefusal() error {
	return errors.New("Antigravity CLI fleet enrollment lacks a supported automated auth completion/status contract; complete the documented per-target agy SSH OAuth URL/browser-code flow in the target runtime context, then establish CLI control and included-only allowance before activation; preserve the existing desktop adapter")
}
