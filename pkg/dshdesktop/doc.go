// Package dshdesktop queues immutable, closed notices for a native DeepSeek
// Harness plugin pinned to one existing live session. The plugin wakes an idle
// agent with followup and steers a busy agent; it never creates a session.
// Ordinary records coalesce while a prior ordinary native inbox input is pending;
// a current locally verified challenge bypasses that hold with a fresh saved
// receiver path. Native acceptance is never model acknowledgment or hook proof.
// See docs/SPEC-DSH-DESKTOP-WAKE.md for mounting and failure boundaries.
package dshdesktop
