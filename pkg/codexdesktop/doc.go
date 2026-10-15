// Package codexdesktop makes one bounded native IPC attempt to start a turn in
// an existing Codex desktop chat.
//
// The notice sent to the desktop is fixed, locally authored text (DeliverSaved
// names the saved event's path, never its body). External event bodies must
// remain outside this package; only a validated event ID reaches the notice.
package codexdesktop
