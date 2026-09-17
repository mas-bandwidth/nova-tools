package chat

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// SPEC-CHAT.md carries the one place this spec accepts a hand-written protocol
// implementation: the gateway behind --source gateway, work-list item 11. The
// poll path stays the fallback and source= says which ran. This test reads the
// spec the way internal/decide's doc test reads SPEC-DECIDE.md, so the section
// being cut back out of the doc is red before the gateway is ever built.
func TestSpecChatNamesTheGatewayBehindSourceGateway(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "SPEC-CHAT.md"))
	if err != nil {
		t.Fatalf("SPEC-CHAT.md is missing: %s", err)
	}
	doc := string(raw)
	for _, phrase := range []string{
		"`--source <poll|gateway>`",
		"`internal/chat/ws`",
		"RFC 6455",
		"handshake, framing, masking, ping/pong, close",
		"no third-party import",
		"Identify",
		"Heartbeat",
		"Resume",
		"the poll path stays the fallback",
		"source= says which ran",
		"first DM from an id the allow-list does not name",
		"sub-second",
	} {
		if !strings.Contains(doc, phrase) {
			t.Errorf("SPEC-CHAT.md does not name the gateway keyed by %q", phrase)
		}
	}
}
