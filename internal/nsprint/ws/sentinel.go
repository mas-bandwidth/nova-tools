package ws

import (
	"regexp"
	"strings"
)

// The stream sentinel (nova-tools #4318; Glenn 2026-09-26 ~11:55 AM ET:
// "every stream ends in a landed sentinel card"). Every stream has one
// sentinel card, task:<slug>:sentinel, created in the stream's waiting set
// when the stream is registered (its first push, or stream order;
// cm_register in fn/lua/02_card_move.lua) and landed by task land with the
// merge sha only when every other card of the stream is landed or done
// (TK.edge). It is never dealt. A dependency across streams is one
// horizontal edge from a card to another stream's sentinel: DEPENDS-ON
// <slug>:sentinel (or task:<slug>:sentinel) on the first card of the stream
// that waits, resolved by the waiting resolver like any task id.

// SentinelSuffix ends every sentinel id.
const SentinelSuffix = ":sentinel"

var (
	slugRE     = regexp.MustCompile(`[^a-z0-9]+`)
	sentinelRE = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*:sentinel$`)
)

// Slug is a stream name's slug: lower-cased, every run of other characters
// one '-', none at either end; empty when the name has no letter or digit.
// It is the rule of internal/nsprint/land/stream.Slug for one stream and of
// TK.slug in fn/lua/02_card_move.lua (the one writer of the sentinel).
func Slug(stream string) string {
	return strings.Trim(slugRE.ReplaceAllString(strings.ToLower(stream), "-"), "-")
}

// SentinelID is the stream's sentinel card id, <slug>:sentinel; empty when
// the stream has no slug.
func SentinelID(stream string) string {
	s := Slug(stream)
	if s == "" {
		return ""
	}
	return s + SentinelSuffix
}

// IsSentinel reports whether id is a stream sentinel's id.
func IsSentinel(id string) bool {
	return sentinelRE.MatchString(id)
}
