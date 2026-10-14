// Package dshdesktop makes one bounded HTTP attempt to deliver a notice into
// a running DeepSeek Harness desktop session, or to start a turn in it when
// idle.
//
// The notice passed to Deliver must be fixed, locally authored text. External
// event bodies must remain outside this package; only a validated event ID is
// appended to the notice. Delivery is the harness's own session.prompt Typert
// Remote method on the loopback gateway: steer interrupts the running turn,
// and followup queues a turn the idle agent starts (docs/SPEC-DSH-DESKTOP-WAKE.md).
package dshdesktop
