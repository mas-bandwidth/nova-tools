// Package harness is the vocabulary of the harnesses a card's child runs under: the one
// word a route row, a packet and a launch name a harness by. It is a leaf (standard
// library only) so nova-config's route kind, the sprint's route and nova-worker's launcher
// all spell the words from one list.
//
// A harness is known by the name of its program. opencode is the harness every provider
// row of the providers table launches (internal/swarm providers.go); claude, codex and
// grok are the headless harnesses of the heavy tier (the owner, 2026-10-04): subscription
// logins on one machine, each run as a one-shot child printing its own usage, and the
// route row names which (docs/SPEC-WORKER.md, the headless harnesses).
package harness

import (
	"path/filepath"
	"slices"
	"strings"
)

// The harness words.
const (
	OpenCode = "opencode"
	Claude   = "claude"
	Codex    = "codex"
	Grok     = "grok"
)

// Kinds is every harness word a route row may carry, in the order a list prints them.
var Kinds = []string{OpenCode, Claude, Codex, Grok}

// Headless are the harnesses that run as a one-shot child and print their own usage.
var Headless = []string{Claude, Codex, Grok}

// IsHeadless reports whether kind is one of the headless harnesses.
func IsHeadless(kind string) bool { return slices.Contains(Headless, kind) }

// KindOf is the harness a binary is: the headless harness its program name spells
// (`claude`, `/Users/g/.local/bin/codex`, `grok.exe`), and opencode for any other program,
// which the providers table launches as it always has.
func KindOf(bin string) string {
	base := filepath.Base(bin)
	name := strings.TrimSuffix(base, filepath.Ext(base))
	if IsHeadless(name) {
		return name
	}
	return OpenCode
}

// ProviderPrefix begins the provider word of a headless harness's route.
const ProviderPrefix = "subscription-"

// ProviderOf is the provider word the routes of a headless harness carry:
// `subscription-claude`, `subscription-codex`, `subscription-grok`, and "" for any other
// kind. A rest is a provider's (internal/sprint route_rest.go), so one word per harness
// keeps one harness's expired login from resting the other two.
func ProviderOf(kind string) string {
	if !IsHeadless(kind) {
		return ""
	}
	return ProviderPrefix + kind
}
