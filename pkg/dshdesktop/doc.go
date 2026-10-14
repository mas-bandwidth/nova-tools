// Package dshdesktop delivers a closed notice into a running DeepSeek Harness
// desktop session through the harness's own hook bridge: a PostToolUse command
// hook surfaces a pending notice after every tool call, and a Stop command hook
// forces another step when a notice is still pending at the turn boundary.
//
// The notice passed to Deliver must be fixed, locally authored text. External
// event bodies must remain outside this package; only a validated event ID is
// appended to the notice, and the notice is coalesced so a burst of messages is
// one pending notice, never a flood (docs/SPEC-DSH-DESKTOP-WAKE.md).
package dshdesktop
