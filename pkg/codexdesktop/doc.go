// Package codexdesktop makes one bounded native IPC attempt to start a turn in
// an existing Codex desktop chat.
//
// The notice passed to Deliver must be fixed, locally authored text. External
// event bodies must remain outside this package; only a validated event ID is
// appended to the notice.
package codexdesktop
